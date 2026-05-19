package tests

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

func TestApplyRecoveryTransitionStagesNewNetworkAndCleansRecoveryState(t *testing.T) {
	databases.STATE = openTempDB(t)

	mustPutJSONToStateDB(t, "existing-account", structures.Account{Balance: 10, Nonce: 1})
	mustPutJSONToStateDB(t, constants.DBKeyPrefixDelayedTransactions+"3", []map[string]string{{"type": "keep"}})
	mustPutJSONToStateDB(t, constants.DBKeyPrefixDelayedTransactions+"4", []map[string]string{{"type": "delete"}})
	if err := databases.STATE.Put([]byte(constants.DBKeyRecoveryActive), []byte("42"), nil); err != nil {
		t.Fatalf("failed to write recovery active marker: %v", err)
	}
	mustPutJSONToStateDB(t, constants.DBKeyPrefixRecoveryData+"42", structures.RecoveryData{LastAbsoluteHeight: 42})

	cursor := &structures.ChainCursor{
		NetworkId:         "old-network",
		CoreMajorVersion:  1,
		NetworkParameters: structures.NetworkParameters{QuorumSize: 1},
		EpochDataHandler:  structures.EpochDataHandler{Id: 3, Hash: "old-epoch"},
		Statistics:        &structures.Statistics{LastHeight: 42, BlocksGenerated: 9},
		EpochStatistics:   &structures.Statistics{LastHeight: 42, BlocksGenerated: 3},
	}
	genesisParams := structures.NetworkParameters{QuorumSize: 2, EpochDuration: 10_000, LeadershipDuration: 1_000}
	genesis := structures.Genesis{
		NetworkId:                "recovered-network",
		CoreMajorVersion:         7,
		FirstEpochStartTimestamp: 123456789,
		NetworkParameters:        genesisParams,
		Validators: []structures.ValidatorStorage{
			{
				Pubkey:          "validator-a",
				Percentage:      50,
				TotalStaked:     100,
				Stakers:         map[string]uint64{"validator-a": 100},
				ValidatorUrl:    "http://validator-a",
				WssValidatorUrl: "ws://validator-a",
			},
			{
				Pubkey:          "validator-b",
				Percentage:      50,
				TotalStaked:     100,
				Stakers:         map[string]uint64{"validator-b": 100},
				ValidatorUrl:    "http://validator-b",
				WssValidatorUrl: "ws://validator-b",
			},
		},
		State: map[string]structures.Account{
			"existing-account": {Balance: 999},
			"new-account":      {Balance: 25, Nonce: 2},
		},
	}
	plan := &structures.RecoveryData{
		LastEpochIndex:     3,
		LastAbsoluteHeight: 42,
		Genesis:            genesis,
	}

	batch := new(leveldb.Batch)
	if err := utils.ApplyRecoveryTransition(cursor, batch, plan); err != nil {
		t.Fatalf("expected recovery transition to apply: %v", err)
	}
	if err := databases.STATE.Write(batch, nil); err != nil {
		t.Fatalf("failed to commit recovery transition batch: %v", err)
	}

	if cursor.EpochOffset != 4 || cursor.NetworkId != genesis.NetworkId || cursor.CoreMajorVersion != genesis.CoreMajorVersion {
		t.Fatalf("unexpected cursor after recovery transition: %+v", cursor)
	}
	if !reflect.DeepEqual(cursor.NetworkParameters, genesisParams) {
		t.Fatalf("unexpected cursor network params: %+v", cursor.NetworkParameters)
	}
	if cursor.EpochDataHandler.Id != 0 || cursor.EpochDataHandler.Hash == "" ||
		len(cursor.EpochDataHandler.Quorum) != len(genesis.Validators) ||
		len(cursor.EpochDataHandler.LeadersSequence) != len(genesis.Validators) {
		t.Fatalf("unexpected recovery genesis epoch handler: %+v", cursor.EpochDataHandler)
	}
	if cursor.EpochStatistics == nil || cursor.EpochStatistics.LastHeight != cursor.Statistics.LastHeight || cursor.EpochStatistics.BlocksGenerated != 0 {
		t.Fatalf("unexpected reset epoch statistics: %+v", cursor.EpochStatistics)
	}

	var storedEpochStats structures.Statistics
	mustReadJSONFromStateDB(t, constants.DBKeyPrefixEpochStats+"3", &storedEpochStats)
	if storedEpochStats.BlocksGenerated != 3 {
		t.Fatalf("expected previous epoch stats to be staged, got %+v", storedEpochStats)
	}

	var storedSnapshot structures.EpochDataSnapshot
	mustReadJSONFromStateDB(t, constants.DBKeyPrefixEpochData+"4", &storedSnapshot)
	if storedSnapshot.Id != 0 || storedSnapshot.Hash != cursor.EpochDataHandler.Hash {
		t.Fatalf("unexpected stored recovery epoch snapshot: %+v", storedSnapshot)
	}

	var existing structures.Account
	mustReadJSONFromStateDB(t, "existing-account", &existing)
	if existing.Balance != 10 {
		t.Fatalf("existing account should not be overwritten by recovery genesis, got %+v", existing)
	}
	var created structures.Account
	mustReadJSONFromStateDB(t, "new-account", &created)
	if created.Balance != 25 || created.Nonce != 2 {
		t.Fatalf("expected new recovery genesis account to be staged, got %+v", created)
	}

	var storedValidator structures.ValidatorStorage
	mustReadJSONFromStateDB(t, constants.DBKeyPrefixValidatorStorage+"validator-a", &storedValidator)
	if storedValidator.Pubkey != "validator-a" || storedValidator.TotalStaked != 100 {
		t.Fatalf("expected recovery genesis validator in state, got %+v", storedValidator)
	}

	if _, err := databases.STATE.Get([]byte(constants.DBKeyPrefixDelayedTransactions+"3"), nil); err != nil {
		t.Fatalf("delayed tx before recovery boundary should remain: %v", err)
	}
	if _, err := databases.STATE.Get([]byte(constants.DBKeyPrefixDelayedTransactions+"4"), nil); err != leveldb.ErrNotFound {
		t.Fatalf("delayed tx after recovery boundary should be deleted, got err=%v", err)
	}
	if _, err := databases.STATE.Get([]byte(constants.DBKeyRecoveryActive), nil); err != leveldb.ErrNotFound {
		t.Fatalf("recovery active marker should be deleted, got err=%v", err)
	}
	if _, err := databases.STATE.Get([]byte(constants.DBKeyPrefixRecoveryData+strconv.FormatInt(plan.LastAbsoluteHeight, 10)), nil); err != leveldb.ErrNotFound {
		t.Fatalf("recovery data should be deleted, got err=%v", err)
	}
}

func TestApplyRecoveryTransitionRejectsMismatchedCursorHeight(t *testing.T) {
	cursor := &structures.ChainCursor{
		Statistics:      &structures.Statistics{LastHeight: 41},
		EpochStatistics: &structures.Statistics{LastHeight: 41},
	}
	err := utils.ApplyRecoveryTransition(cursor, new(leveldb.Batch), &structures.RecoveryData{LastAbsoluteHeight: 42})
	if err == nil {
		t.Fatalf("expected recovery transition to reject mismatched cursor height")
	}
}

func mustPutJSONToStateDB(t *testing.T, key string, value any) {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal %s: %v", key, err)
	}
	if err := databases.STATE.Put([]byte(key), raw, nil); err != nil {
		t.Fatalf("failed to write %s: %v", key, err)
	}
}

func mustReadJSONFromStateDB(t *testing.T, key string, out any) {
	t.Helper()

	raw, err := databases.STATE.Get([]byte(key), nil)
	if err != nil {
		t.Fatalf("failed to read %s: %v", key, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("failed to decode %s: %v", key, err)
	}
}
