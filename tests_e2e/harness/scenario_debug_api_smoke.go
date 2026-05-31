package main

import (
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

func debugAPISmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario debug_api_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "debug-api-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
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
	fmt.Printf("scenario debug_api_smoke: preparing run %s\n", *runID)
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

	fmt.Println("scenario debug_api_smoke: starting nodes")
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

	initialHeight, err := waitForCoreHeight(coreNode, -1, 5*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	nextHeight, err := waitForCoreHeight(coreNode, initialHeight, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	baseURL := strings.TrimSuffix(coreNode.HealthURL, "/live_stats")
	client := http.Client{Timeout: 2 * time.Second}

	var pipeline map[string]any
	if err := fetchJSONStatusOK(client, baseURL+"/debug/pipeline_state", &pipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug pipeline_state failed: %w", err)
	}
	if err := assertDebugPipelineShape(pipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	executionNext, err := extractDebugExecutionNext(pipeline)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	var heightProbe map[string]any
	if err := fetchJSONStatusOK(client, fmt.Sprintf("%s/debug/height_probe/%d", baseURL, executionNext), &heightProbe); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe failed: %w", err)
	}
	if err := assertNestedMap(heightProbe, "core"); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe malformed: %w", err)
	}
	if err := assertNumberKey(heightProbe, "ahpNext"); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe malformed: %w", err)
	}

	epochID, leaderIndex, err := extractDebugEpochAndLeader(pipeline)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	var leaderPipeline map[string]any
	if err := fetchJSONStatusOK(client, fmt.Sprintf("%s/debug/leader_pipeline/%d/%d", baseURL, epochID, leaderIndex), &leaderPipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug leader_pipeline failed: %w", err)
	}
	for _, key := range []string{"nodePublicKey", "epochId", "leaderIndex", "leader", "localBlocks", "alfp", "lastMileTracker", "lastMileRelation", "ahpTracker", "ahpRelation"} {
		if _, ok := leaderPipeline[key]; !ok {
			printScenarioDiagnostics(state, 120)
			return fmt.Errorf("debug leader_pipeline missing %q", key)
		}
	}

	var outbox map[string]any
	if err := fetchJSONStatusOK(client, baseURL+"/debug/pod_outbox_state", &outbox); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug pod_outbox_state failed: %w", err)
	}
	for _, key := range []string{"pendingCount", "countsByType", "sampleIds"} {
		if _, ok := outbox[key]; !ok {
			printScenarioDiagnostics(state, 120)
			return fmt.Errorf("debug pod_outbox_state missing %q", key)
		}
	}

	fmt.Printf("PASS debug_api_smoke: height advanced from %d to %d; debug API shapes are valid\n", initialHeight, nextHeight)
	return nil
}
