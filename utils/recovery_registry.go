package utils

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
)

func BuildRecoveryPayload(lastEpochIndex int, lastAbsoluteHeight int64, genesis structures.Genesis) (string, error) {
	genesisHash, err := HashRecoveryGenesis(genesis)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("RECOVERY_RESTART:%d:%d:%s", lastEpochIndex, lastAbsoluteHeight, genesisHash), nil
}

func HashRecoveryGenesis(genesis structures.Genesis) (string, error) {
	raw, err := json.Marshal(genesis)
	if err != nil {
		return "", fmt.Errorf("marshal recovery genesis: %w", err)
	}

	return Blake3(string(raw)), nil
}

func ValidateRecoveryData(data *structures.RecoveryData, teamPubkey string) error {
	if data == nil {
		return fmt.Errorf("recovery data is nil")
	}
	if data.LastEpochIndex < 0 {
		return fmt.Errorf("lastEpochIndex must be non-negative")
	}
	if data.LastAbsoluteHeight < 0 {
		return fmt.Errorf("lastAbsoluteHeight must be non-negative")
	}
	if data.Genesis.NetworkId == "" {
		return fmt.Errorf("recovery genesis network id is empty")
	}
	if data.TeamSig == "" {
		return fmt.Errorf("teamSig is empty")
	}
	if teamPubkey == "" {
		return fmt.Errorf("team pubkey is empty")
	}
	if !cryptography.IsValidPubKey(teamPubkey) {
		return fmt.Errorf("invalid team pubkey")
	}

	payload, err := BuildRecoveryPayload(data.LastEpochIndex, data.LastAbsoluteHeight, data.Genesis)
	if err != nil {
		return err
	}
	if !cryptography.VerifySignature(payload, teamPubkey, data.TeamSig) {
		return fmt.Errorf("invalid team signature for payload %q", payload)
	}

	return nil
}

func LoadActiveRecoveryData() (*structures.RecoveryData, error) {
	rawHeight, err := databases.STATE.Get([]byte(constants.DBKeyRecoveryActive), nil)
	if err == leveldb.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", constants.DBKeyRecoveryActive, err)
	}

	height, err := strconv.ParseInt(string(rawHeight), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", constants.DBKeyRecoveryActive, err)
	}

	rawData, err := databases.STATE.Get([]byte(constants.DBKeyPrefixRecoveryData+strconv.FormatInt(height, 10)), nil)
	if err != nil {
		return nil, fmt.Errorf("load %s%d: %w", constants.DBKeyPrefixRecoveryData, height, err)
	}

	var data structures.RecoveryData
	if err := json.Unmarshal(rawData, &data); err != nil {
		return nil, fmt.Errorf("parse recovery data: %w", err)
	}

	return &data, nil
}
