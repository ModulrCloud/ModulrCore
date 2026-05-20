package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

type Manifest struct {
	Name  string         `json:"name"`
	Nodes []ManifestNode `json:"nodes"`
}

type ManifestNode struct {
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	RepoPath      string            `json:"repoPath"`
	WorkDir       string            `json:"workDir,omitempty"`
	Command       []string          `json:"command"`
	ChaindataPath string            `json:"chaindataPath"`
	Env           map[string]string `json:"env,omitempty"`
}

type GeneratedNetwork struct {
	RunID       string `json:"runId"`
	RootDir     string `json:"rootDir"`
	Manifest    string `json:"manifest"`
	CoreCount   int    `json:"coreCount"`
	AnchorCount int    `json:"anchorCount"`
	CreatedAt   string `json:"createdAt"`
}

type RunState struct {
	RunID       string      `json:"runId"`
	Manifest    string      `json:"manifest"`
	StartedAt   string      `json:"startedAt"`
	RunDir      string      `json:"runDir"`
	LogsDir     string      `json:"logsDir"`
	Nodes       []NodeState `json:"nodes"`
	HarnessNote string      `json:"harnessNote,omitempty"`
}

type NodeState struct {
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	PID           int      `json:"pid"`
	RepoPath      string   `json:"repoPath"`
	WorkDir       string   `json:"workDir"`
	Command       []string `json:"command"`
	ChaindataPath string   `json:"chaindataPath"`
	StdoutLog     string   `json:"stdoutLog"`
	StderrLog     string   `json:"stderrLog"`
	StartedAt     string   `json:"startedAt"`
}

func main() {
	if len(os.Args) < 2 {
		usageAndExit()
	}

	var err error
	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return
	case "prepare":
		err = prepareCmd(os.Args[2:])
	case "start":
		err = startCmd(os.Args[2:])
	case "status":
		err = statusCmd(os.Args[2:])
	case "logs":
		err = logsCmd(os.Args[2:])
	case "stop":
		err = stopCmd(os.Args[2:])
	default:
		usageAndExit()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

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
		anchorNodeCommand = []string{"go", "run", anchorsRepoAbs}
	}

	coreKeys := generateKeys(*coreCount)
	anchorKeys := generateKeys(*anchorCount)
	now := uint64(time.Now().Add(2 * time.Second).UnixMilli())

	coreHTTPBase := *basePort
	coreWSBase := *basePort + 1000
	anchorHTTPBase := *basePort + 2000
	anchorWSBase := *basePort + 3000

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
			"validatorURL":    fmt.Sprintf("http://localhost:%d", httpPort),
			"wssValidatorURL": fmt.Sprintf("ws://localhost:%d", wsPort),
		})
		coreState[key.Pub] = map[string]any{"balance": uint64(20_000_000_000), "nonce": 0}
	}

	anchors := make([]map[string]any, 0, *anchorCount)
	for idx, key := range anchorKeys {
		anchors = append(anchors, map[string]any{
			"pubkey":       key.Pub,
			"anchorURL":    fmt.Sprintf("http://localhost:%d", anchorHTTPBase+idx),
			"wssAnchorURL": fmt.Sprintf("ws://localhost:%d", anchorWSBase+idx),
		})
	}

	coreGenesis := map[string]any{
		"NETWORK_ID":                  randomHex(32),
		"CORE_MAJOR_VERSION":          0,
		"FIRST_EPOCH_START_TIMESTAMP": now,
		"NETWORK_PARAMETERS":          coreNetworkParams(*coreCount),
		"VALIDATORS":                  coreValidators,
		"STATE":                       coreState,
	}
	anchorGenesis := map[string]any{
		"NETWORK_ID":                  randomHex(32),
		"FIRST_EPOCH_START_TIMESTAMP": now,
		"NETWORK_PARAMETERS":          anchorNetworkParams(*anchorCount),
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
		coreBootstrapNodes = append(coreBootstrapNodes, fmt.Sprintf("http://localhost:%d", coreHTTPBase+idx))
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
			"POINT_OF_DISTRIBUTION_WS":         fmt.Sprintf("ws://localhost:%d", coreWSBase+idx),
			"ANCHORS_POINT_OF_DISTRIBUTION_WS": fmt.Sprintf("ws://localhost:%d", anchorWSBase),
			"DISABLE_POD_OUTBOX":               true,
			"EXTRA_DATA_TO_BLOCK":              map[string]string{"e2e": "true", "node": nodeName},
			"TXS_MEMPOOL_SIZE":                 300000,
			"BOOTSTRAP_NODES":                  coreBootstrapNodes,
			"MY_HOSTNAME":                      fmt.Sprintf("http://localhost:%d", coreHTTPBase+idx),
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
			"POINT_OF_DISTRIBUTION": fmt.Sprintf("ws://localhost:%d", anchorWSBase+idx),
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
			WorkDir:       absOrOriginal(chaindata),
			ChaindataPath: absOrOriginal(chaindata),
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

func startCmd(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	manifestPath := fs.String("manifest", "", "path to E2E manifest JSON")
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs"), "directory for run state and logs")
	runID := fs.String("run-id", time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		return errors.New("missing -manifest")
	}

	manifest, err := loadManifest(*manifestPath)
	if err != nil {
		return err
	}
	if len(manifest.Nodes) == 0 {
		return errors.New("manifest has no nodes")
	}

	runDir := filepath.Join(*runRoot, *runID)
	logsDir := filepath.Join(runDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return err
	}

	state := RunState{
		RunID:       *runID,
		Manifest:    absOrOriginal(*manifestPath),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		RunDir:      absOrOriginal(runDir),
		LogsDir:     absOrOriginal(logsDir),
		HarnessNote: "manifest-driven process harness; config generation and scenarios are intentionally separate next steps",
	}

	for _, node := range manifest.Nodes {
		nodeState, err := startNode(node, logsDir)
		if err != nil {
			_ = stopState(state, 2*time.Second)
			return fmt.Errorf("start %s: %w", node.Name, err)
		}
		state.Nodes = append(state.Nodes, nodeState)
	}

	if err := writeState(runDir, state); err != nil {
		_ = stopState(state, 2*time.Second)
		return err
	}
	if err := writeLatestPointer(*runRoot, runDir); err != nil {
		return err
	}

	fmt.Printf("started run %s\nstate: %s\nlogs: %s\n", state.RunID, filepath.Join(runDir, "state.json"), logsDir)
	return nil
}

func statusCmd(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	runDir := fs.String("run-dir", filepath.Join("tests_e2e", "runs", "latest"), "run directory or latest symlink")
	if err := fs.Parse(args); err != nil {
		return err
	}

	state, err := loadState(*runDir)
	if err != nil {
		return err
	}

	fmt.Printf("run: %s\nstarted: %s\n", state.RunID, state.StartedAt)
	for _, node := range state.Nodes {
		status := "stopped"
		if processAlive(node.PID) {
			status = "running"
		}
		fmt.Printf("%-20s %-10s pid=%-8d %s\n", node.Name, node.Role, node.PID, status)
	}
	return nil
}

func logsCmd(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	runDir := fs.String("run-dir", filepath.Join("tests_e2e", "runs", "latest"), "run directory or latest symlink")
	nodeName := fs.String("node", "", "node name")
	stream := fs.String("stream", "stdout", "stdout or stderr")
	lines := fs.Int("lines", 120, "number of last lines to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *nodeName == "" {
		return errors.New("missing -node")
	}

	state, err := loadState(*runDir)
	if err != nil {
		return err
	}
	for _, node := range state.Nodes {
		if node.Name != *nodeName {
			continue
		}
		path := node.StdoutLog
		if *stream == "stderr" {
			path = node.StderrLog
		}
		return printLastLines(path, *lines)
	}
	return fmt.Errorf("node %q not found", *nodeName)
}

func stopCmd(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	runDir := fs.String("run-dir", filepath.Join("tests_e2e", "runs", "latest"), "run directory or latest symlink")
	timeout := fs.Duration("timeout", 5*time.Second, "graceful shutdown timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	state, err := loadState(*runDir)
	if err != nil {
		return err
	}
	if err := stopState(state, *timeout); err != nil {
		return err
	}
	fmt.Printf("stopped run %s\n", state.RunID)
	return nil
}

func startNode(node ManifestNode, logsDir string) (NodeState, error) {
	if node.Name == "" {
		return NodeState{}, errors.New("node name is empty")
	}
	if len(node.Command) == 0 {
		return NodeState{}, errors.New("node command is empty")
	}
	if node.RepoPath == "" {
		return NodeState{}, errors.New("node repoPath is empty")
	}
	if node.ChaindataPath == "" {
		return NodeState{}, errors.New("node chaindataPath is empty")
	}
	workDir := node.WorkDir
	if workDir == "" {
		workDir = node.RepoPath
	}

	stdoutPath := filepath.Join(logsDir, node.Name+".stdout.log")
	stderrPath := filepath.Join(logsDir, node.Name+".stderr.log")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		return NodeState{}, err
	}
	defer stdout.Close()
	stderr, err := os.Create(stderrPath)
	if err != nil {
		return NodeState{}, err
	}
	defer stderr.Close()

	cmd := exec.Command(node.Command[0], node.Command[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), "CHAINDATA_PATH="+node.ChaindataPath)
	for key, value := range node.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return NodeState{}, err
	}

	return NodeState{
		Name:          node.Name,
		Role:          node.Role,
		PID:           cmd.Process.Pid,
		RepoPath:      absOrOriginal(node.RepoPath),
		WorkDir:       absOrOriginal(workDir),
		Command:       node.Command,
		ChaindataPath: absOrOriginal(node.ChaindataPath),
		StdoutLog:     absOrOriginal(stdoutPath),
		StderrLog:     absOrOriginal(stderrPath),
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func stopState(state RunState, timeout time.Duration) error {
	for _, node := range state.Nodes {
		if !processAlive(node.PID) {
			continue
		}
		_ = syscall.Kill(-node.PID, syscall.SIGTERM)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allStopped := true
		for _, node := range state.Nodes {
			if processAlive(node.PID) {
				allStopped = false
				break
			}
		}
		if allStopped {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	for _, node := range state.Nodes {
		if processAlive(node.PID) {
			_ = syscall.Kill(-node.PID, syscall.SIGKILL)
		}
	}
	return nil
}

func loadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func loadState(runDir string) (RunState, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, "state.json"))
	if err != nil {
		return RunState{}, err
	}
	var state RunState
	if err := json.Unmarshal(raw, &state); err != nil {
		return RunState{}, err
	}
	return state, nil
}

func writeState(runDir string, state RunState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, "state.json"), raw, 0644)
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0644)
}

func writeLatestPointer(runRoot string, runDir string) error {
	if err := os.MkdirAll(runRoot, 0755); err != nil {
		return err
	}
	latest := filepath.Join(runRoot, "latest")
	_ = os.Remove(latest)
	if err := os.Symlink(filepath.Base(runDir), latest); err == nil {
		return nil
	}
	return os.WriteFile(filepath.Join(runRoot, "latest.txt"), []byte(runDir+"\n"), 0644)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func printLastLines(path string, n int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	content := strings.TrimRight(string(raw), "\n")
	if content == "" {
		return nil
	}
	parts := strings.Split(content, "\n")
	if n > 0 && len(parts) > n {
		parts = parts[len(parts)-n:]
	}
	for _, line := range parts {
		fmt.Println(line)
	}
	return nil
}

func absOrOriginal(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
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

func coreNetworkParams(quorumSize int) map[string]any {
	return map[string]any{
		"VALIDATOR_REQUIRED_STAKE": uint64(50_000_000_000),
		"MINIMAL_STAKE_PER_STAKER": uint64(2_000_000_000),
		"QUORUM_SIZE":              quorumSize,
		"EPOCH_DURATION":           int64(30_000),
		"LEADERSHIP_DURATION":      int64(5_000),
		"BLOCK_TIME":               int64(1_000),
		"MAX_BLOCK_SIZE_IN_BYTES":  int64(12_288_000),
		"TXS_LIMIT_PER_BLOCK":      30_000,
	}
}

func anchorNetworkParams(quorumSize int) map[string]any {
	return map[string]any{
		"QUORUM_SIZE":                             quorumSize,
		"EPOCH_DURATION":                          int64(30_000),
		"BLOCK_TIME":                              int64(1_000),
		"MAX_BLOCK_SIZE_IN_BYTES":                 int64(12_288_000),
		"TXS_LIMIT_PER_BLOCK":                     30_000,
		"MAX_EPOCHS_TO_SUPPORT":                   16,
		"BLOCK_CREATORS_HEALTH_CHECK_INTERVAL_MS": int64(5_000),
	}
}

func usageAndExit() {
	printUsage(os.Stderr)
	os.Exit(2)
}

func printUsage(out io.Writer) {
	fmt.Fprintf(out, `usage:
  go run ./tests_e2e/harness prepare [-core 1] [-anchors 1]
  go run ./tests_e2e/harness start  -manifest tests_e2e/manifests/example.json
  go run ./tests_e2e/harness status [-run-dir tests_e2e/runs/latest]
  go run ./tests_e2e/harness logs   -node core-1 [-stream stdout] [-lines 120]
  go run ./tests_e2e/harness stop   [-run-dir tests_e2e/runs/latest]
  go run ./tests_e2e/harness help

manifest node command examples:
  ["go", "run", "."]
  ["/absolute/path/to/modulr"]

`)
}
