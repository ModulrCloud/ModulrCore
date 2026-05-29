package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

type recoveryData struct {
	LastEpochIndex     int                `json:"lastEpochIndex"`
	LastAbsoluteHeight int64              `json:"lastAbsoluteHeight"`
	Genesis            structures.Genesis `json:"genesis"`
	TeamSig            string             `json:"teamSig"`
}

func main() {
	chaindataPath := flag.String("chaindata", "", "absolute path to modulr-core chaindata directory")
	teamPubkey := flag.String("team-pubkey", "", "team public key used to verify recovery.json")
	recoveryJSONPath := flag.String("recovery-json", "scripts/recovery/recovery.json", "path to recovery.json")
	flag.Parse()

	if err := run(*chaindataPath, *teamPubkey, *recoveryJSONPath); err != nil {
		fmt.Fprintln(os.Stderr, "recovery failed:", err)
		os.Exit(1)
	}
}

func run(chaindataPath, teamPubkey, recoveryJSONPath string) error {
	if chaindataPath == "" {
		return fmt.Errorf("missing -chaindata")
	}
	if teamPubkey == "" {
		return fmt.Errorf("missing -team-pubkey")
	}
	if !cryptography.IsValidPubKey(teamPubkey) {
		return fmt.Errorf("invalid team pubkey")
	}

	data, err := loadRecoveryData(recoveryJSONPath)
	if err != nil {
		return err
	}
	if err := validateRecoveryData(data, teamPubkey); err != nil {
		return err
	}

	statePath := filepath.Join(chaindataPath, "STATE")
	stateDB, err := leveldb.OpenFile(statePath, nil)
	if err != nil {
		return fmt.Errorf("open STATE db at %s: %w", statePath, err)
	}
	defer stateDB.Close()

	cursor, err := loadChainCursor(stateDB)
	if err != nil {
		return err
	}
	if err := validateLocalCursor(cursor, data); err != nil {
		return err
	}

	if err := writeRecoveryPlan(stateDB, data); err != nil {
		return err
	}

	fmt.Println("Recovery plan registered successfully")
	fmt.Printf("  lastEpochIndex: %d\n", data.LastEpochIndex)
	fmt.Printf("  lastAbsoluteHeight: %d\n", data.LastAbsoluteHeight)
	fmt.Printf("  next network id: %s\n", data.Genesis.NetworkId)
	fmt.Printf("  transition key: %s%d\n", constants.DBKeyPrefixRecoveryData, data.LastAbsoluteHeight)

	return nil
}

func loadRecoveryData(path string) (recoveryData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return recoveryData{}, fmt.Errorf("read recovery json: %w", err)
	}

	var data recoveryData
	if err := json.Unmarshal(raw, &data); err != nil {
		return recoveryData{}, fmt.Errorf("parse recovery json: %w", err)
	}

	return data, nil
}

func validateRecoveryData(data recoveryData, teamPubkey string) error {
	recoveryDataForValidation := structures.RecoveryData{
		LastEpochIndex:     data.LastEpochIndex,
		LastAbsoluteHeight: data.LastAbsoluteHeight,
		Genesis:            data.Genesis,
		TeamSig:            data.TeamSig,
	}
	return utils.ValidateRecoveryData(&recoveryDataForValidation, teamPubkey)
}

func loadChainCursor(stateDB *leveldb.DB) (structures.ChainCursor, error) {
	raw, err := stateDB.Get([]byte(constants.DBKeyChainCursor), nil)
	if err != nil {
		return structures.ChainCursor{}, fmt.Errorf("load %s: %w", constants.DBKeyChainCursor, err)
	}

	var cursor structures.ChainCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return structures.ChainCursor{}, fmt.Errorf("parse %s: %w", constants.DBKeyChainCursor, err)
	}

	return cursor, nil
}

func validateLocalCursor(cursor structures.ChainCursor, data recoveryData) error {
	if cursor.Statistics == nil {
		return fmt.Errorf("CHAIN_CURSOR.statistics is nil")
	}

	if cursor.Statistics.LastHeight > data.LastAbsoluteHeight {
		return fmt.Errorf(
			"local node is above recovery height: local=%d required=%d; restore/rollback chaindata to the recovery point first",
			cursor.Statistics.LastHeight,
			data.LastAbsoluteHeight,
		)
	}

	localAbsoluteEpoch := cursor.EpochOffset + cursor.EpochDataHandler.Id
	if localAbsoluteEpoch < data.LastEpochIndex {
		return fmt.Errorf(
			"local node is below recovery epoch: local=%d required=%d; start core again and wait for synchronization",
			localAbsoluteEpoch,
			data.LastEpochIndex,
		)
	}
	if localAbsoluteEpoch > data.LastEpochIndex {
		return fmt.Errorf(
			"local node is above recovery epoch: local=%d required=%d; restore/rollback chaindata to the recovery point first",
			localAbsoluteEpoch,
			data.LastEpochIndex,
		)
	}

	if data.Genesis.NetworkId == cursor.NetworkId {
		return fmt.Errorf("recovery genesis must belong to a new network: cursor=%q genesis=%q", cursor.NetworkId, data.Genesis.NetworkId)
	}

	return nil
}

func writeRecoveryPlan(stateDB *leveldb.DB, data recoveryData) error {
	batch := new(leveldb.Batch)

	dataBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal recovery data: %w", err)
	}

	height := strconv.FormatInt(data.LastAbsoluteHeight, 10)
	batch.Put([]byte(constants.DBKeyPrefixRecoveryData+height), dataBytes)
	batch.Put([]byte(constants.DBKeyRecoveryActive), []byte(height))

	if err := stateDB.Write(batch, &opt.WriteOptions{Sync: true}); err != nil {
		return fmt.Errorf("write recovery registry batch: %w", err)
	}

	return nil
}
