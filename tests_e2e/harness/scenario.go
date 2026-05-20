package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func scenarioCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("missing scenario name")
	}

	switch args[0] {
	case "bootstrap_smoke":
		return bootstrapSmokeScenario(args[1:])
	default:
		return fmt.Errorf("unknown scenario %q", args[0])
	}
}

func bootstrapSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario bootstrap_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "bootstrap-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 12*time.Second, "timeout for observing core height growth")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario bootstrap_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario bootstrap_smoke: starting nodes")
	if err := startCmd([]string{
		"-manifest", manifestPath,
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-health-timeout", healthTimeout.String(),
	}); err != nil {
		if state, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(state, 80)
		}
		return err
	}

	state, err := loadState(runDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = stopState(state, 5*time.Second)
	}()

	coreNode, err := findNodeByRole(state, "core")
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}

	initialHeight, err := waitForCoreHeight(coreNode, -1, 5*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	nextHeight, err := waitForCoreHeight(coreNode, initialHeight, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if ok, err := checkHealthURL(http.Client{Timeout: 750 * time.Millisecond}, anchorNode.HealthURL); !ok {
		printScenarioDiagnostics(state, 80)
		return fmt.Errorf("anchor health check failed after startup: %v", err)
	}

	fmt.Printf("PASS bootstrap_smoke: core height advanced from %d to %d; anchor is healthy\n", initialHeight, nextHeight)
	return nil
}

func waitForCoreHeight(node NodeState, minExclusive int64, timeout time.Duration) (int64, error) {
	if node.HealthURL == "" {
		return -1, fmt.Errorf("%s has no health URL", node.Name)
	}
	lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return -1, err
		}
		height, err := fetchLastHeight(client, lastHeightURL)
		if err == nil && height > minExclusive {
			return height, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	return -1, fmt.Errorf("core height did not advance beyond %d within %s: %v", minExclusive, timeout, lastErr)
}

func fetchLastHeight(client http.Client, lastHeightURL string) (int64, error) {
	var payload struct {
		LastHeight int64 `json:"lastHeight"`
	}
	if err := fetchJSON(client, lastHeightURL, &payload); err != nil {
		return -1, err
	}
	return payload.LastHeight, nil
}

func findNodeByRole(state RunState, role string) (NodeState, error) {
	for _, node := range state.Nodes {
		if node.Role == role {
			return node, nil
		}
	}
	return NodeState{}, fmt.Errorf("node with role %q not found", role)
}

func assertNodeAlive(node NodeState) error {
	if !processAlive(node.PID) {
		return fmt.Errorf("%s is not running (pid=%d)", node.Name, node.PID)
	}
	return nil
}

func printScenarioDiagnostics(state RunState, lines int) {
	fmt.Fprintln(os.Stderr, "scenario diagnostics:")
	for _, node := range state.Nodes {
		fmt.Fprintf(os.Stderr, "--- %s stdout ---\n", node.Name)
		_ = printLastLinesTo(os.Stderr, node.StdoutLog, lines)
		fmt.Fprintf(os.Stderr, "--- %s stderr ---\n", node.Name)
		_ = printLastLinesTo(os.Stderr, node.StderrLog, lines)
	}
}
