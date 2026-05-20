package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	case "alfp_pull_smoke":
		return alfpPullSmokeScenario(args[1:])
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

func alfpPullSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario alfp_pull_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "alfp-pull-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 60*time.Second, "timeout for observing proactive ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario alfp_pull_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	anchorHTTPURL := fmt.Sprintf("http://localhost:%d", *basePort+2000)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorsHTTPURL(runDir, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario alfp_pull_smoke: blocking core ALFP POSTs via proxy %s -> %s\n", proxyURL, anchorHTTPURL)
	if err := startCmd([]string{
		"-manifest", manifestPath,
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-health-timeout", healthTimeout.String(),
	}); err != nil {
		if state, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(state, 120)
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
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	proactiveLogEnd, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	fmt.Println("PASS alfp_pull_smoke: anchor built ALFP from core quorum and included it in an anchor block")
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

func waitForCoreEpochAtLeast(node NodeState, minEpoch int, timeout time.Duration) error {
	if node.HealthURL == "" {
		return fmt.Errorf("%s has no health URL", node.Name)
	}
	liveStatsURL := node.HealthURL
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return err
		}
		epochID, err := fetchLiveStatsEpoch(client, liveStatsURL)
		if err == nil && epochID >= minEpoch {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("core epoch did not reach %d within %s: %v", minEpoch, timeout, lastErr)
}

func fetchLiveStatsEpoch(client http.Client, liveStatsURL string) (int, error) {
	var payload map[string]any
	if err := fetchJSON(client, liveStatsURL, &payload); err != nil {
		return -1, err
	}
	epoch, ok := payload["epoch"].(map[string]any)
	if !ok {
		return -1, errors.New("live_stats response has no epoch object")
	}
	for _, key := range []string{"id", "Id"} {
		if raw, ok := epoch[key].(float64); ok {
			return int(raw), nil
		}
	}
	return -1, errors.New("live_stats epoch has no id")
}

func waitForLogPatternFrom(path string, pattern *regexp.Regexp, from int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) >= from {
			if match := pattern.FindIndex(raw[from:]); match != nil {
				return from + match[1], nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("log %s did not match %q within %s", path, pattern.String(), timeout)
}

func startAlfpBlockingProxy(targetRawURL string) (string, func(), error) {
	targetURL, err := url.Parse(targetRawURL)
	if err != nil {
		return "", nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/accept_aggregated_leader_finalization_proof" {
				if shouldBlockAlfpPost(r) {
					http.Error(w, "ALFP POST intentionally blocked by alfp_pull_smoke", http.StatusServiceUnavailable)
					return
				}
			}
			proxy.ServeHTTP(w, r)
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()

	closeFn := func() {
		_ = server.Close()
	}
	return "http://" + listener.Addr().String(), closeFn, nil
}

func shouldBlockAlfpPost(r *http.Request) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return true
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var payload struct {
		LeaderFinalizations []struct {
			EpochIndex int `json:"epochIndex"`
		} `json:"leaderFinalizations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return true
	}
	for _, proof := range payload.LeaderFinalizations {
		if proof.EpochIndex > 0 {
			return true
		}
	}
	return false
}

func rewriteCoreAnchorsHTTPURL(runDir string, proxyURL string) error {
	path := filepath.Join(runDir, "network", "core-1", "anchors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var anchors []map[string]any
	if err := json.Unmarshal(raw, &anchors); err != nil {
		return err
	}
	if len(anchors) == 0 {
		return errors.New("core anchors.json has no anchors")
	}
	anchors[0]["anchorURL"] = proxyURL
	return writeJSON(path, anchors)
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
