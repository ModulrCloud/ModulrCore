package main

import (
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"time"
)

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
