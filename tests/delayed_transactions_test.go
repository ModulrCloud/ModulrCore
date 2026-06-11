package tests

import (
	"container/list"
	"encoding/json"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"path/filepath"
	"strconv"
	"testing"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/system_contracts"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

func setupApprovementHandler(t *testing.T, network structures.NetworkParameters) {
	handlers.APPROVEMENT_THREAD_METADATA.Handler = structures.ApprovementThreadMetadataHandler{
		NetworkParameters: network,
		EpochDataHandler:  structures.EpochDataHandler{},
	}

	// Isolate tests from each other: contract helpers prioritize ValidatorsTouched over cache,
	// so we must reset touched sets + cache bookkeeping between tests.
	utils.ResetApprovementTouchedSets()
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRU = list.New()
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRUIndex = make(map[string]*list.Element)

	// Ensure DB handle is always valid (some getters fall back to DB on cache misses).
	databases.APPROVEMENT_THREAD_METADATA = openTempDB(t)
}

func openTempDB(t *testing.T) *leveldb.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to open temp db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestCreateValidatorAddsNewValidatorToApprovementCache(t *testing.T) {
	setupApprovementHandler(t, structures.NetworkParameters{})

	delayedTx := map[string]string{
		"creator":         "validator1",
		"percentage":      "75",
		"validatorURL":    "http://validator",
		"wssValidatorURL": "ws://validator",
	}

	if !system_contracts.CreateValidator(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected CreateValidator to succeed")
	}

	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	stored := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key]
	if stored == nil {
		t.Fatalf("expected validator to be stored in cache")
	}
	if stored.Pubkey != "validator1" || stored.Percentage != 75 || stored.ValidatorUrl != "http://validator" || stored.WssValidatorUrl != "ws://validator" {
		t.Fatalf("unexpected validator data in cache: %+v", stored)
	}
}

func TestUpdateValidatorUpdatesExistingValidatorInApprovementCache(t *testing.T) {
	setupApprovementHandler(t, structures.NetworkParameters{})
	existing := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      50,
		TotalStaked:     0,
		Stakers:         map[string]uint64{"validator1": 0},
		ValidatorUrl:    "old",
		WssValidatorUrl: "old-wss",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key] = existing

	delayedTx := map[string]string{
		"creator":         "validator1",
		"percentage":      "65",
		"validatorURL":    "http://new",
		"wssValidatorURL": "ws://new",
	}

	if !system_contracts.UpdateValidator(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected UpdateValidator to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key]
	if updated.Percentage != 65 || updated.ValidatorUrl != "http://new" || updated.WssValidatorUrl != "ws://new" {
		t.Fatalf("unexpected updated validator data: %+v", updated)
	}
}

func TestStakeAddsStakeAndRegistersValidator(t *testing.T) {
	setupApprovementHandler(t, structures.NetworkParameters{
		ValidatorRequiredStake: 100,
		MinimalStakePerStaker:  10,
	})

	validator := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      80,
		TotalStaked:     90,
		Stakers:         map[string]uint64{"alice": 90},
		ValidatorUrl:    "http://validator",
		WssValidatorUrl: "ws://validator",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key] = validator

	delayedTx := map[string]string{
		"staker":          "alice",
		"validatorPubKey": "validator1",
		"amount":          "20",
	}

	if !system_contracts.Stake(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected Stake to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key]
	if updated.TotalStaked != 110 {
		t.Fatalf("expected total staked 110, got %d", updated.TotalStaked)
	}
	if updated.Stakers["alice"] != 110 {
		t.Fatalf("expected staker balance 110, got %d", updated.Stakers["alice"])
	}
	if len(handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry) != 1 {
		t.Fatalf("expected validator to be registered")
	}
}

func TestUnstakeRemovesValidatorFromRegistryWhenBelowRequiredStake(t *testing.T) {
	setupApprovementHandler(t, structures.NetworkParameters{
		ValidatorRequiredStake: 100,
		MinimalStakePerStaker:  10,
	})

	validator := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      80,
		TotalStaked:     120,
		Stakers:         map[string]uint64{"alice": 120},
		ValidatorUrl:    "http://validator",
		WssValidatorUrl: "ws://validator",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key] = validator
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = []string{"validator1"}

	delayedTx := map[string]string{
		"unstaker":        "alice",
		"validatorPubKey": "validator1",
		"amount":          "30",
	}

	if !system_contracts.Unstake(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected Unstake to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[key]
	if updated.TotalStaked != 90 {
		t.Fatalf("expected total staked 90, got %d", updated.TotalStaked)
	}
	if updated.Stakers["alice"] != 90 {
		t.Fatalf("expected staker balance 90, got %d", updated.Stakers["alice"])
	}
	if len(handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry) != 0 {
		t.Fatalf("expected validator to be removed from registry")
	}
}

func TestVotingAcceptUpdatesApprovementCoreVersionWithQuorumMajority(t *testing.T) {
	quorum := testQuorum(t, 4)
	setupApprovementHandler(t, structures.NetworkParameters{})
	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = 0
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = structures.EpochDataHandler{
		Id:     7,
		Hash:   "epoch-hash",
		Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
	}

	delayedTx := testVersionVoteTx(t, handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler, 1, quorum[:3])

	if !system_contracts.VotingAccept(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected VotingAccept to succeed")
	}

	if handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion != 1 {
		t.Fatalf("expected approvement core version 1, got %d", handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion)
	}
}

func TestVotingAcceptUpdatesExecutionCoreVersionWithQuorumMajority(t *testing.T) {
	quorum := testQuorum(t, 4)
	handlers.EXECUTION_THREAD_METADATA.ChainCursor = structures.ChainCursor{
		CoreMajorVersion: 0,
		EpochDataHandler: structures.EpochDataHandler{
			Id:     7,
			Hash:   "epoch-hash",
			Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
		},
		Statistics:      &structures.Statistics{LastHeight: -1},
		EpochStatistics: &structures.Statistics{LastHeight: -1},
	}

	delayedTx := testVersionVoteTx(t, handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler, 1, quorum[:3])

	if !system_contracts.VotingAccept(delayedTx, constants.ContextExecutionThread) {
		t.Fatalf("expected VotingAccept to succeed")
	}

	if handlers.EXECUTION_THREAD_METADATA.ChainCursor.CoreMajorVersion != 1 {
		t.Fatalf("expected execution core version 1, got %d", handlers.EXECUTION_THREAD_METADATA.ChainCursor.CoreMajorVersion)
	}
}

func TestVotingAcceptRejectsVersionUpdateWithoutQuorumMajority(t *testing.T) {
	quorum := testQuorum(t, 4)
	setupApprovementHandler(t, structures.NetworkParameters{})
	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = 0
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = structures.EpochDataHandler{
		Id:     7,
		Hash:   "epoch-hash",
		Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
	}

	delayedTx := testVersionVoteTx(t, handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler, 1, quorum[:2])

	if system_contracts.VotingAccept(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected VotingAccept to fail without majority")
	}

	if handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion != 0 {
		t.Fatalf("expected approvement core version to remain 0, got %d", handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion)
	}
}

func TestVotingAcceptUpdatesApprovementNetworkParameterWithQuorumMajority(t *testing.T) {
	quorum := testQuorum(t, 4)
	setupApprovementHandler(t, structures.NetworkParameters{QuorumSize: 4})
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = structures.EpochDataHandler{
		Id:     7,
		Hash:   "epoch-hash",
		Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
	}

	delayedTx := testParametersVoteTx(t, handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler, "QUORUM_SIZE", "8", quorum[:3])

	if !system_contracts.VotingAccept(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected VotingAccept parameter update to succeed")
	}

	if handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize != 8 {
		t.Fatalf("expected approvement quorum size 8, got %d", handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize)
	}
}

func TestVotingAcceptUpdatesExecutionNetworkParameterWithQuorumMajority(t *testing.T) {
	quorum := testQuorum(t, 4)
	handlers.EXECUTION_THREAD_METADATA.ChainCursor = structures.ChainCursor{
		NetworkParameters: structures.NetworkParameters{EpochDuration: 1000},
		EpochDataHandler: structures.EpochDataHandler{
			Id:     7,
			Hash:   "epoch-hash",
			Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
		},
		Statistics:      &structures.Statistics{LastHeight: -1},
		EpochStatistics: &structures.Statistics{LastHeight: -1},
	}

	delayedTx := testParametersVoteTx(t, handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochDataHandler, "EPOCH_DURATION", "2500", quorum[:3])

	if !system_contracts.VotingAccept(delayedTx, constants.ContextExecutionThread) {
		t.Fatalf("expected VotingAccept parameter update to succeed")
	}

	if handlers.EXECUTION_THREAD_METADATA.ChainCursor.NetworkParameters.EpochDuration != 2500 {
		t.Fatalf("expected execution epoch duration 2500, got %d", handlers.EXECUTION_THREAD_METADATA.ChainCursor.NetworkParameters.EpochDuration)
	}
}

func TestVotingAcceptRejectsUnsupportedNetworkParameterUpdate(t *testing.T) {
	quorum := testQuorum(t, 4)
	setupApprovementHandler(t, structures.NetworkParameters{QuorumSize: 4})
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = structures.EpochDataHandler{
		Id:     7,
		Hash:   "epoch-hash",
		Quorum: []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub},
	}

	delayedTx := testParametersVoteTx(t, handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler, "UNKNOWN_FIELD", "8", quorum[:3])

	if system_contracts.VotingAccept(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected VotingAccept parameter update to fail for unsupported field")
	}

	if handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize != 4 {
		t.Fatalf("expected approvement quorum size to remain 4, got %d", handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize)
	}
}

func testQuorum(t *testing.T, size int) []cryptography.Ed25519Box {
	t.Helper()

	quorum := make([]cryptography.Ed25519Box, size)
	for i := range quorum {
		quorum[i] = cryptography.GenerateKeyPair("", "", nil)
	}

	return quorum
}

func testVersionVoteTx(t *testing.T, epoch structures.EpochDataHandler, newMajorVersion int, signers []cryptography.Ed25519Box) map[string]string {
	t.Helper()

	dataToSign, ok := system_contracts.BuildVotingAcceptSigningPayload(&epoch, "version", newMajorVersion)
	if !ok {
		t.Fatalf("failed to build voting accept signing payload")
	}

	payload := map[string]string{
		"type":            "votingAccept",
		"votingType":      "version",
		"newMajorVersion": strconv.Itoa(newMajorVersion),
	}

	agreements := make(map[string]string, len(signers))
	for _, signer := range signers {
		agreements[signer.Pub] = cryptography.GenerateSignature(signer.Prv, dataToSign)
	}

	rawAgreements, err := json.Marshal(agreements)
	if err != nil {
		t.Fatalf("failed to marshal voting agreements: %v", err)
	}
	payload["agreements"] = string(rawAgreements)

	return payload
}

func testParametersVoteTx(t *testing.T, epoch structures.EpochDataHandler, updateField string, newValue string, signers []cryptography.Ed25519Box) map[string]string {
	t.Helper()

	dataToSign, ok := system_contracts.BuildVotingAcceptParametersSigningPayload(&epoch, updateField, newValue)
	if !ok {
		t.Fatalf("failed to build voting accept parameter signing payload")
	}

	payload := map[string]string{
		"type":        "votingAccept",
		"votingType":  "parameters",
		"updateField": updateField,
		"newValue":    newValue,
	}

	agreements := make(map[string]string, len(signers))
	for _, signer := range signers {
		agreements[signer.Pub] = cryptography.GenerateSignature(signer.Prv, dataToSign)
	}

	rawAgreements, err := json.Marshal(agreements)
	if err != nil {
		t.Fatalf("failed to marshal voting agreements: %v", err)
	}
	payload["agreements"] = string(rawAgreements)

	return payload
}
