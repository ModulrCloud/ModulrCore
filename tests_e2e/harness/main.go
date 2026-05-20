package main

import (
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
)

type Manifest struct {
	Name  string         `json:"name"`
	Nodes []ManifestNode `json:"nodes"`
}

type ManifestNode struct {
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	RepoPath      string            `json:"repoPath"`
	Command       []string          `json:"command"`
	ChaindataPath string            `json:"chaindataPath"`
	Env           map[string]string `json:"env,omitempty"`
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
	cmd.Dir = node.RepoPath
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

func usageAndExit() {
	printUsage(os.Stderr)
	os.Exit(2)
}

func printUsage(out io.Writer) {
	fmt.Fprintf(out, `usage:
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
