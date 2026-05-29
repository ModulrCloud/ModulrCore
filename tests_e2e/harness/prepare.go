package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func prepareCmd(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	coreCount := fs.Int("core", 1, "number of modulr-core validator nodes")
	anchorCount := fs.Int("anchors", 1, "number of modulr-anchors-core nodes")
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs"), "directory for generated network data")
	runID := fs.String("run-id", time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	coreCommand := fs.String("core-command", "go run .", "command used to start each core node")
	anchorCommand := fs.String("anchor-command", "go run .", "command used to start each anchor node")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	overwrite := fs.Bool("overwrite", false, "remove an existing generated run directory before preparing")
	coreEpochDurationMs := fs.Int64("core-epoch-duration-ms", 30_000, "core epoch duration in milliseconds")
	coreLeadershipDurationMs := fs.Int64("core-leadership-duration-ms", 5_000, "core leadership duration in milliseconds")
	coreBlockTimeMs := fs.Int64("core-block-time-ms", 1_000, "core block time in milliseconds")
	anchorEpochDurationMs := fs.Int64("anchor-epoch-duration-ms", 30_000, "anchor epoch duration in milliseconds")
	anchorBlockTimeMs := fs.Int64("anchor-block-time-ms", 1_000, "anchor block time in milliseconds")
	anchorHealthCheckIntervalMs := fs.Int64("anchor-health-check-interval-ms", 5_000, "anchor block creator health check interval in milliseconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *coreCount <= 0 {
		return errors.New("-core must be positive")
	}
	if *anchorCount <= 0 {
		return errors.New("-anchors must be positive")
	}

	runDir := filepath.Join(*runRoot, *runID)
	if err := prepareRunDir(runDir, *overwrite); err != nil {
		return err
	}
	networkDir := filepath.Join(runDir, "network")
	if err := os.MkdirAll(networkDir, 0755); err != nil {
		return err
	}
	coreRepoAbs := absOrOriginal(*coreRepo)
	anchorsRepoAbs := absOrOriginal(*anchorsRepo)
	coreNodeCommand := splitCommand(*coreCommand)
	if strings.TrimSpace(*coreCommand) == "go run ." {
		coreNodeCommand = []string{"go", "run", coreRepoAbs}
	}
	anchorNodeCommand := splitCommand(*anchorCommand)
	if strings.TrimSpace(*anchorCommand) == "go run ." {
		anchorNodeCommand = []string{"go", "run", "."}
	}

	coreKeys := generateKeys(*coreCount)
	anchorKeys := generateKeys(*anchorCount)
	now := uint64(time.Now().Add(2 * time.Second).UnixMilli())

	coreHTTPBase := *basePort
	coreWSBase := *basePort + 1000
	anchorHTTPBase := *basePort + 2000
	anchorWSBase := *basePort + 3000
	loopbackHost := "127.0.0.1"

	coreValidators := make([]map[string]any, 0, *coreCount)
	coreState := make(map[string]any, *coreCount)
	for idx, key := range coreKeys {
		httpPort := coreHTTPBase + idx
		wsPort := coreWSBase + idx
		coreValidators = append(coreValidators, map[string]any{
			"pubkey":          key.Pub,
			"percentage":      percentageForIndex(idx, *coreCount),
			"totalStaked":     uint64(50_000_000_000),
			"stakers":         map[string]uint64{key.Pub: 50_000_000_000},
			"validatorURL":    fmt.Sprintf("http://%s:%d", loopbackHost, httpPort),
			"wssValidatorURL": fmt.Sprintf("ws://%s:%d", loopbackHost, wsPort),
		})
		coreState[key.Pub] = map[string]any{"balance": uint64(20_000_000_000), "nonce": 0}
	}

	anchors := make([]map[string]any, 0, *anchorCount)
	for idx, key := range anchorKeys {
		anchors = append(anchors, map[string]any{
			"pubkey":       key.Pub,
			"anchorURL":    fmt.Sprintf("http://%s:%d", loopbackHost, anchorHTTPBase+idx),
			"wssAnchorURL": fmt.Sprintf("ws://%s:%d", loopbackHost, anchorWSBase+idx),
		})
	}

	coreGenesis := map[string]any{
		"NETWORK_ID":                  randomHex(32),
		"CORE_MAJOR_VERSION":          0,
		"FIRST_EPOCH_START_TIMESTAMP": now,
		"NETWORK_PARAMETERS":          coreNetworkParams(*coreCount, *coreEpochDurationMs, *coreLeadershipDurationMs, *coreBlockTimeMs),
		"VALIDATORS":                  coreValidators,
		"STATE":                       coreState,
	}
	anchorGenesis := map[string]any{
		"NETWORK_ID":                  coreGenesis["NETWORK_ID"],
		"FIRST_EPOCH_START_TIMESTAMP": now,
		"NETWORK_PARAMETERS":          anchorNetworkParams(*anchorCount, *anchorEpochDurationMs, *anchorBlockTimeMs, *anchorHealthCheckIntervalMs),
		"ANCHORS":                     anchors,
	}
	coreGenesisForAnchors := map[string]any{
		"NETWORK_ID":                  coreGenesis["NETWORK_ID"],
		"CORE_MAJOR_VERSION":          coreGenesis["CORE_MAJOR_VERSION"],
		"FIRST_EPOCH_START_TIMESTAMP": coreGenesis["FIRST_EPOCH_START_TIMESTAMP"],
		"NETWORK_PARAMETERS":          coreGenesis["NETWORK_PARAMETERS"],
		"VALIDATORS":                  coreGenesis["VALIDATORS"],
	}

	coreBootstrapNodes := make([]string, 0, *coreCount)
	for idx := range coreKeys {
		coreBootstrapNodes = append(coreBootstrapNodes, fmt.Sprintf("http://%s:%d", loopbackHost, coreHTTPBase+idx))
	}
	anchorNodes := make([]ManifestNode, 0, *anchorCount)
	coreNodes := make([]ManifestNode, 0, *coreCount)

	for idx, key := range coreKeys {
		nodeName := fmt.Sprintf("core-%d", idx+1)
		chaindata := filepath.Join(networkDir, nodeName)
		if err := os.MkdirAll(chaindata, 0755); err != nil {
			return err
		}
		config := map[string]any{
			"PUBLIC_KEY":                       key.Pub,
			"PRIVATE_KEY":                      key.Prv,
			"RECOVERY_MODE":                    false,
			"POINT_OF_DISTRIBUTION_WS":         fmt.Sprintf("ws://%s:%d", loopbackHost, coreWSBase+idx),
			"ANCHORS_POINT_OF_DISTRIBUTION_WS": fmt.Sprintf("ws://%s:%d", loopbackHost, anchorWSBase),
			"DISABLE_POD_OUTBOX":               true,
			"EXTRA_DATA_TO_BLOCK":              map[string]string{"e2e": "true", "node": nodeName},
			"TXS_MEMPOOL_SIZE":                 300000,
			"BOOTSTRAP_NODES":                  coreBootstrapNodes,
			"MY_HOSTNAME":                      fmt.Sprintf("http://%s:%d", loopbackHost, coreHTTPBase+idx),
			"INTERFACE":                        "127.0.0.1",
			"PORT":                             coreHTTPBase + idx,
			"WEBSOCKET_INTERFACE":              "127.0.0.1",
			"WEBSOCKET_PORT":                   coreWSBase + idx,
		}
		if err := writeJSON(filepath.Join(chaindata, "configs.json"), config); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(chaindata, "genesis.json"), coreGenesis); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(chaindata, "anchors.json"), anchors); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(chaindata, "version.txt"), []byte("0\n"), 0644); err != nil {
			return err
		}
		coreNodes = append(coreNodes, ManifestNode{
			Name:          nodeName,
			Role:          "core",
			RepoPath:      coreRepoAbs,
			WorkDir:       absOrOriginal(chaindata),
			ChaindataPath: absOrOriginal(chaindata),
			HealthURL:     fmt.Sprintf("http://%s:%d/live_stats", loopbackHost, coreHTTPBase+idx),
			Command:       coreNodeCommand,
		})
	}

	for idx, key := range anchorKeys {
		nodeName := fmt.Sprintf("anchor-%d", idx+1)
		chaindata := filepath.Join(networkDir, nodeName)
		if err := os.MkdirAll(chaindata, 0755); err != nil {
			return err
		}
		config := map[string]any{
			"PUBLIC_KEY":            key.Pub,
			"PRIVATE_KEY":           key.Prv,
			"RECOVERY_MODE":         false,
			"DISABLE_POD_OUTBOX":    true,
			"EXTRA_DATA_TO_BLOCK":   map[string]string{"e2e": "true", "node": nodeName},
			"INTERFACE":             "127.0.0.1",
			"PORT":                  anchorHTTPBase + idx,
			"WEBSOCKET_INTERFACE":   "127.0.0.1",
			"WEBSOCKET_PORT":        anchorWSBase + idx,
			"POINT_OF_DISTRIBUTION": fmt.Sprintf("ws://%s:%d", loopbackHost, anchorWSBase+idx),
			"CORE_BOOTSTRAP_NODES":  coreBootstrapNodes,
		}
		if err := writeJSON(filepath.Join(chaindata, "configs.json"), config); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(chaindata, "genesis.json"), anchorGenesis); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(chaindata, "core_genesis.json"), coreGenesisForAnchors); err != nil {
			return err
		}
		anchorNodes = append(anchorNodes, ManifestNode{
			Name:          nodeName,
			Role:          "anchor",
			RepoPath:      anchorsRepoAbs,
			WorkDir:       anchorsRepoAbs,
			ChaindataPath: absOrOriginal(chaindata),
			HealthURL:     fmt.Sprintf("http://%s:%d/core/quorum_state", loopbackHost, anchorHTTPBase+idx),
			Command:       anchorNodeCommand,
		})
	}

	manifest := Manifest{
		Name:  "generated-" + *runID,
		Nodes: append(coreNodes, anchorNodes...),
	}
	manifestPath := filepath.Join(runDir, "manifest.json")
	if err := writeJSON(manifestPath, manifest); err != nil {
		return err
	}

	generated := GeneratedNetwork{
		RunID:       *runID,
		RootDir:     absOrOriginal(runDir),
		Manifest:    absOrOriginal(manifestPath),
		CoreCount:   *coreCount,
		AnchorCount: *anchorCount,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(filepath.Join(runDir, "generated.json"), generated); err != nil {
		return err
	}
	if err := writeLatestPointer(*runRoot, runDir); err != nil {
		return err
	}

	fmt.Printf("prepared E2E network %s\nmanifest: %s\nnetwork: %s\n", *runID, manifestPath, networkDir)
	fmt.Printf("start with:\n  go run ./tests_e2e/harness start -manifest %s\n", manifestPath)
	return nil
}

func prepareRunDir(runDir string, overwrite bool) error {
	if _, err := os.Stat(runDir); err == nil {
		if !overwrite {
			return fmt.Errorf("run directory %q already exists; use a new -run-id or pass -overwrite", runDir)
		}
		if state, loadErr := loadState(runDir); loadErr == nil {
			_ = stopState(state, 5*time.Second)
		}
		return os.RemoveAll(runDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func generateKeys(count int) []cryptography.Ed25519Box {
	keys := make([]cryptography.Ed25519Box, 0, count)
	for i := 0; i < count; i++ {
		keys = append(keys, cryptography.GenerateKeyPair("", "", nil))
	}
	return keys
}

func randomHex(bytesCount int) string {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return strings.Repeat("0", bytesCount*2)
	}
	return hex.EncodeToString(raw)
}

func splitCommand(command string) []string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return []string{"go", "run", "."}
	}
	return fields
}

func percentageForIndex(index int, total int) uint8 {
	if total <= 0 {
		return 100
	}
	base := 100 / total
	remainder := 100 % total
	value := base
	if index < remainder {
		value++
	}
	return uint8(value)
}

func coreNetworkParams(quorumSize int, epochDurationMs, leadershipDurationMs, blockTimeMs int64) map[string]any {
	return map[string]any{
		"VALIDATOR_REQUIRED_STAKE": uint64(50_000_000_000),
		"MINIMAL_STAKE_PER_STAKER": uint64(2_000_000_000),
		"QUORUM_SIZE":              quorumSize,
		"EPOCH_DURATION":           epochDurationMs,
		"LEADERSHIP_DURATION":      leadershipDurationMs,
		"BLOCK_TIME":               blockTimeMs,
		"MAX_BLOCK_SIZE_IN_BYTES":  int64(12_288_000),
		"TXS_LIMIT_PER_BLOCK":      30_000,
	}
}

func anchorNetworkParams(quorumSize int, epochDurationMs, blockTimeMs, healthCheckIntervalMs int64) map[string]any {
	return map[string]any{
		"QUORUM_SIZE":                             quorumSize,
		"EPOCH_DURATION":                          epochDurationMs,
		"BLOCK_TIME":                              blockTimeMs,
		"MAX_BLOCK_SIZE_IN_BYTES":                 int64(12_288_000),
		"TXS_LIMIT_PER_BLOCK":                     30_000,
		"MAX_EPOCHS_TO_SUPPORT":                   16,
		"BLOCK_CREATORS_HEALTH_CHECK_INTERVAL_MS": healthCheckIntervalMs,
	}
}
