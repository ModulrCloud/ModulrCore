package utils

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type RecoveryGenesisValidatorGetter map[string]*structures.ValidatorStorage

func (getter RecoveryGenesisValidatorGetter) Get(pubkey string) *structures.ValidatorStorage {
	return getter[pubkey]
}

func ApplyRecoveryTransition(cursor *structures.ChainCursor, stateBatch *leveldb.Batch, plan *structures.RecoveryData) error {
	if plan == nil {
		return fmt.Errorf("recovery plan is nil")
	}
	if cursor == nil {
		return fmt.Errorf("chain cursor is nil")
	}
	if cursor.Statistics == nil {
		return fmt.Errorf("CHAIN_CURSOR.statistics is nil")
	}
	if cursor.Statistics.LastHeight != plan.LastAbsoluteHeight {
		return fmt.Errorf("cursor height %d does not match recovery height %d", cursor.Statistics.LastHeight, plan.LastAbsoluteHeight)
	}
	if cursor.EpochStatistics == nil {
		return fmt.Errorf("CHAIN_CURSOR.epochStatistics is nil")
	}

	statsBytes, err := json.Marshal(cursor.EpochStatistics)
	if err != nil {
		return fmt.Errorf("marshal epoch statistics: %w", err)
	}
	stateBatch.Put([]byte(constants.DBKeyPrefixEpochStats+strconv.Itoa(plan.LastEpochIndex)), statsBytes)

	if err := SettleAndDeleteDelayedTransactions(stateBatch); err != nil {
		return err
	}

	nextEpochHandler, err := BuildRecoveryGenesisEpochHandler(plan.Genesis)
	if err != nil {
		return err
	}

	cursor.HeightOffset = plan.LastAbsoluteHeight + 1
	cursor.EpochOffset = plan.LastEpochIndex + 1
	cursor.LastExecutedLocalHeight = -1
	cursor.NetworkId = plan.Genesis.NetworkId
	cursor.CoreMajorVersion = plan.Genesis.CoreMajorVersion
	cursor.NetworkParameters = plan.Genesis.NetworkParameters.CopyNetworkParameters()
	cursor.EpochDataHandler = *nextEpochHandler
	cursor.EpochStatistics = &structures.Statistics{LastHeight: -1}

	snapshot := structures.EpochDataSnapshot{
		EpochDataHandler:  *nextEpochHandler,
		NetworkParameters: plan.Genesis.NetworkParameters.CopyNetworkParameters(),
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal recovery epoch snapshot: %w", err)
	}
	stateBatch.Put([]byte(constants.DBKeyPrefixEpochData+strconv.Itoa(cursor.EpochOffset)), snapshotBytes)

	if err := StageRecoveryGenesisState(stateBatch, plan.Genesis); err != nil {
		return err
	}

	stateBatch.Delete([]byte(constants.DBKeyRecoveryActive))
	stateBatch.Delete([]byte(constants.DBKeyPrefixRecoveryData + strconv.FormatInt(plan.LastAbsoluteHeight, 10)))

	return nil
}

func BuildRecoveryGenesisEpochHandler(genesis structures.Genesis) (*structures.EpochDataHandler, error) {
	initEpochHash := Blake3(constants.ZeroHash + genesis.NetworkId)
	epochHandler := &structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		ValidatorsRegistry: make([]string, 0, len(genesis.Validators)),
		StartTimestamp:     genesis.FirstEpochStartTimestamp,
		Quorum:             []string{},
		LeadersSequence:    []string{},
		CurrentLeaderIndex: 0,
	}

	validatorsByPubkey := make(RecoveryGenesisValidatorGetter, len(genesis.Validators))
	for i := range genesis.Validators {
		validator := genesis.Validators[i]
		epochHandler.ValidatorsRegistry = append(epochHandler.ValidatorsRegistry, validator.Pubkey)
		validatorsByPubkey[validator.Pubkey] = &genesis.Validators[i]
	}

	epochHandler.Quorum = GetCurrentEpochQuorum(epochHandler, genesis.NetworkParameters.QuorumSize, initEpochHash, validatorsByPubkey.Get)
	SetLeadersSequence(epochHandler, initEpochHash, validatorsByPubkey.Get)

	if len(epochHandler.Quorum) == 0 || len(epochHandler.LeadersSequence) == 0 {
		return nil, fmt.Errorf("recovery genesis produced empty quorum/leaders")
	}

	return epochHandler, nil
}

func StageRecoveryGenesisState(stateBatch *leveldb.Batch, genesis structures.Genesis) error {
	for accountPubkey, accountData := range genesis.State {
		if _, err := databases.STATE.Get([]byte(accountPubkey), nil); err == nil {
			continue
		}

		serialized, err := json.Marshal(accountData)
		if err != nil {
			return fmt.Errorf("marshal recovery genesis account: %w", err)
		}
		stateBatch.Put([]byte(accountPubkey), serialized)
	}

	for _, validatorStorage := range genesis.Validators {
		stateKey := constants.DBKeyPrefixValidatorStorage + validatorStorage.Pubkey
		if _, err := databases.STATE.Get([]byte(stateKey), nil); err == nil {
			continue
		}

		serialized, err := json.Marshal(validatorStorage)
		if err != nil {
			return fmt.Errorf("marshal recovery genesis validator: %w", err)
		}
		stateBatch.Put([]byte(stateKey), serialized)

		validatorCopy := validatorStorage
		PutExecValidatorCache(stateKey, &validatorCopy)
	}

	return nil
}

func SettleAndDeleteDelayedTransactions(batch *leveldb.Batch) error {
	prefix := []byte(constants.DBKeyPrefixDelayedTransactions)
	it := databases.STATE.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()

	for it.Next() {
		var delayedTransactions []map[string]string
		if err := json.Unmarshal(it.Value(), &delayedTransactions); err == nil {
			for _, delayedTx := range delayedTransactions {
				if err := refundPendingStakeDelayedTransaction(batch, delayedTx); err != nil {
					return err
				}
			}
		}

		keyCopy := append([]byte(nil), it.Key()...)
		batch.Delete(keyCopy)
	}
	if err := it.Error(); err != nil {
		return fmt.Errorf("iterate delayed transactions: %w", err)
	}

	return nil
}

func refundPendingStakeDelayedTransaction(batch *leveldb.Batch, delayedTx map[string]string) error {
	if delayedTx["type"] != "stake" {
		return nil
	}

	staker := delayedTx["staker"]
	if staker == "" {
		return nil
	}

	amount, err := strconv.ParseUint(delayedTx["amount"], 10, 64)
	if err != nil || amount == 0 {
		return nil
	}

	account := GetAccountFromExecThreadState(staker)
	account.Balance += amount

	rawAccount, err := json.Marshal(account)
	if err != nil {
		return fmt.Errorf("marshal refunded stake account %s: %w", staker, err)
	}
	batch.Put([]byte(staker), rawAccount)

	return nil
}
