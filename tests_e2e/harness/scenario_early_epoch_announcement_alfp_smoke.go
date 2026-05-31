package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

func earlyEpochAnnouncementAlfpSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario early_epoch_announcement_alfp_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "early-epoch-announcement-alfp-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 75*time.Second, "timeout for observing early announcement and ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	coreCount, anchorCount := 1, 1
	selectedBasePort := *basePort
	if selectedBasePort == 0 {
		var err error
		selectedBasePort, err = findAvailableGeneratedBasePort(35000, 4000, coreCount, anchorCount)
		if err != nil {
			return err
		}
		fmt.Printf("scenario early_epoch_announcement_alfp_smoke: auto-selected base-port %d\n", selectedBasePort)
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario early_epoch_announcement_alfp_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(selectedBasePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	anchorHTTPURL := fmt.Sprintf("http://localhost:%d", selectedBasePort+2000)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL, false)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorsHTTPURL(runDir, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario early_epoch_announcement_alfp_smoke: blocking direct core ALFP POSTs via proxy %s -> %s\n", proxyURL, anchorHTTPURL)
	if err := startCmd([]string{
		"-manifest", manifestPath,
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-health-timeout", healthTimeout.String(),
	}); err != nil {
		if state, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(state, 160)
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
		printScenarioDiagnostics(state, 160)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Epoch announcement proof collected for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}

	earlyStart, earlyEnd, err := waitForLogMatchRangeFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied early epoch announcement proof 0 -> 1`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if found, err := logPatternExistsBefore(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), earlyStart); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	} else if found {
		printScenarioDiagnostics(state, 200)
		return errors.New("anchor applied AERP 0->1 before early epoch announcement proof")
	}

	proactiveLogEnd, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool \(epoch=1`), earlyEnd, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), earlyEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}

	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	fmt.Println("PASS early_epoch_announcement_alfp_smoke: anchor applied early core epoch announcement before AERP, proactively collected epoch 1 ALFP, and later applied AERP 0->1")
	return nil
}
