package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/syndtr/goleveldb/leveldb"
	"lukechampine.com/blake3"
)

type recoverySignedResponse struct {
	PubKey    string          `json:"pubKey"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

type recoveryCoreQuorumPayload struct {
	Proof                     *recoveryAggregatedEpochRotationProof `json:"proof"`
	ValidatorEndpoints        map[string]recoveryValidatorEndpoints `json:"validatorEndpoints"`
	RecoveryViewEpoch         int                                   `json:"recoveryViewEpoch"`
	RecoveryViewEpochDataHash string                                `json:"recoveryViewEpochDataHash"`
	RecoveryViewSource        string                                `json:"recoveryViewSource"`
	RecoveryViewFromEpoch     int                                   `json:"recoveryViewFromEpoch"`
	RecoveryViewVerifiedAtMs  int64                                 `json:"recoveryViewVerifiedAtMs"`
}

type recoveryValidatorEndpoints struct {
	ValidatorURL    string `json:"validatorUrl"`
	WssValidatorURL string `json:"wssValidatorUrl"`
}

type recoveryAggregatedEpochRotationProof struct {
	EpochID       int               `json:"epochId"`
	NextEpochID   int               `json:"nextEpochId"`
	EpochDataHash string            `json:"epochDataHash"`
	Proofs        map[string]string `json:"proofs"`
}

func waitForRecoveryLatestCoreQuorum(node NodeState, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	if node.HealthURL == "" {
		return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s has no health URL", node.Name)
	}
	endpoint := strings.TrimSuffix(node.HealthURL, "/core/quorum_state") + "/recovery/latest_core_quorum"
	return waitForRecoveryCoreQuorumEndpoint(node, endpoint, timeout)
}

func waitForRecoveryLatestCoreQuorumAtLeast(node NodeState, minEpochID int, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		signed, payload, err := waitForRecoveryLatestCoreQuorum(node, 6*time.Second)
		if err == nil && payload.Proof != nil && payload.Proof.NextEpochID >= minEpochID {
			return signed, payload, nil
		}
		if err != nil {
			lastErr = err
		} else if payload.Proof == nil {
			lastErr = errors.New("latest recovery payload had no proof")
		} else {
			lastErr = fmt.Errorf("latest recovery epoch %d is below %d", payload.Proof.NextEpochID, minEpochID)
		}
		time.Sleep(500 * time.Millisecond)
	}

	return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s did not expose recovery latest core quorum >= %d within %s: %v", node.Name, minEpochID, timeout, lastErr)
}

func waitForRecoveryCoreQuorum(node NodeState, epochID int, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	if node.HealthURL == "" {
		return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s has no health URL", node.Name)
	}
	endpoint := strings.TrimSuffix(node.HealthURL, "/core/quorum_state") + fmt.Sprintf("/recovery/core_quorum/%d", epochID)
	return waitForRecoveryCoreQuorumEndpoint(node, endpoint, timeout)
}

func waitForRecoveryCoreQuorumEndpoint(node NodeState, endpoint string, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 5 * time.Second}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, err
		}
		var signed recoverySignedResponse
		if err := fetchJSONStatusOK(client, endpoint, &signed); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var payload recoveryCoreQuorumPayload
		if err := json.Unmarshal(signed.Payload, &payload); err != nil {
			return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, err
		}
		return signed, payload, nil
	}

	return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("anchor did not expose recovery core quorum at %s within %s: %v", endpoint, timeout, lastErr)
}

func enableAnchorRecoveryMode(runDir string, anchorName string) error {
	return setAnchorRecoveryMode(runDir, anchorName, true)
}

func setAnchorRecoveryMode(runDir string, anchorName string, enabled bool) error {
	path := filepath.Join(runDir, "network", anchorName, "configs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config["RECOVERY_MODE"] = enabled
	return writeJSON(path, config)
}

func buildRecoveryGenesisFromCoreNode(coreNode NodeState, runID string) (structures.Genesis, cryptography.Ed25519Box, error) {
	raw, err := os.ReadFile(filepath.Join(coreNode.ChaindataPath, "genesis.json"))
	if err != nil {
		return structures.Genesis{}, cryptography.Ed25519Box{}, err
	}
	var genesis structures.Genesis
	if err := json.Unmarshal(raw, &genesis); err != nil {
		return structures.Genesis{}, cryptography.Ed25519Box{}, err
	}
	if len(genesis.Validators) == 0 {
		return structures.Genesis{}, cryptography.Ed25519Box{}, errors.New("recovery genesis has no validators")
	}
	genesis.NetworkId = randomHex(32)
	genesis.FirstEpochStartTimestamp = uint64(time.Now().Add(2 * time.Second).UnixMilli())
	if genesis.State == nil {
		genesis.State = make(map[string]structures.Account)
	}
	teamKey := cryptography.GenerateKeyPair("", "", nil)
	genesis.State[teamKey.Pub] = structures.Account{Balance: 1_000_000_000, Nonce: 0}

	return genesis, teamKey, nil
}

func buildSignedRecoveryData(lastEpochIndex int, lastAbsoluteHeight int64, genesis structures.Genesis, teamKey cryptography.Ed25519Box) (structures.RecoveryData, error) {
	payload, err := buildRecoveryPayloadForHarness(lastEpochIndex, lastAbsoluteHeight, genesis)
	if err != nil {
		return structures.RecoveryData{}, err
	}
	return structures.RecoveryData{
		LastEpochIndex:     lastEpochIndex,
		LastAbsoluteHeight: lastAbsoluteHeight,
		Genesis:            genesis,
		TeamSig:            cryptography.GenerateSignature(teamKey.Prv, payload),
	}, nil
}

func buildRecoveryPayloadForHarness(lastEpochIndex int, lastAbsoluteHeight int64, genesis structures.Genesis) (string, error) {
	raw, err := json.Marshal(genesis)
	if err != nil {
		return "", fmt.Errorf("marshal recovery genesis: %w", err)
	}
	hashBytes := blake3.Sum256(raw)
	genesisHash := hex.EncodeToString(hashBytes[:])
	return fmt.Sprintf("RECOVERY_RESTART:%d:%d:%s", lastEpochIndex, lastAbsoluteHeight, genesisHash), nil
}

func writeCoreRecoveryPlan(coreNode NodeState, recoveryData structures.RecoveryData) error {
	stateDB, err := leveldb.OpenFile(filepath.Join(coreNode.ChaindataPath, "STATE"), nil)
	if err != nil {
		return err
	}
	defer stateDB.Close()

	recoveryDataBytes, err := json.Marshal(recoveryData)
	if err != nil {
		return err
	}
	height := fmt.Sprintf("%d", recoveryData.LastAbsoluteHeight)
	batch := new(leveldb.Batch)
	batch.Put([]byte(constants.DBKeyPrefixRecoveryData+height), recoveryDataBytes)
	batch.Put([]byte(constants.DBKeyRecoveryActive), []byte(height))
	return stateDB.Write(batch, nil)
}

func resetAnchorRuntimeState(anchor ManifestNode) error {
	return os.RemoveAll(filepath.Join(anchor.ChaindataPath, "DATABASES"))
}

func updateAnchorGenesisForRecovery(path string, recoveryGenesis structures.Genesis) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var genesis map[string]any
	if err := json.Unmarshal(raw, &genesis); err != nil {
		return err
	}
	genesis["NETWORK_ID"] = recoveryGenesis.NetworkId
	genesis["FIRST_EPOCH_START_TIMESTAMP"] = recoveryGenesis.FirstEpochStartTimestamp
	return writeJSON(path, genesis)
}

func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}
