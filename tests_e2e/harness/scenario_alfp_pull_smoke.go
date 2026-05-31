package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

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
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL, false)
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
