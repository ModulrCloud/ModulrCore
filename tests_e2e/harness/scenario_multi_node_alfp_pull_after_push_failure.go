package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

func multiNodeAlfpPullAfterPushFailureScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_alfp_pull_after_push_failure", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-alfp-pull-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 35000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 180*time.Second, "timeout for observing proactive ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_alfp_pull_after_push_failure: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "12000",
		"-core-leadership-duration-ms", "2000",
		"-core-block-time-ms", "900",
		"-anchor-epoch-duration-ms", "12000",
		"-anchor-block-time-ms", "900",
		"-overwrite",
	}); err != nil {
		return err
	}

	targetAnchorName := "anchor-4"
	targetAnchorHTTPURL := fmt.Sprintf("http://127.0.0.1:%d", *basePort+2000+3)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(targetAnchorHTTPURL, true)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorHTTPURLForAll(runDir, targetAnchorHTTPURL, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario multi_node_alfp_pull_after_push_failure: blocking ALFP POSTs to %s via proxy %s -> %s\n", targetAnchorName, proxyURL, targetAnchorHTTPURL)
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

	coreNodes := findNodesByRole(state, "core")
	anchorNodes := findNodesByRole(state, "anchor")
	if len(coreNodes) != coreCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d core nodes, got %d", coreCount, len(coreNodes))
	}
	if len(anchorNodes) != anchorCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d anchor nodes, got %d", anchorCount, len(anchorNodes))
	}
	targetAnchor, err := findNodeByName(state, targetAnchorName)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	coreNode := coreNodes[0]
	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	proactiveLogEnd, err := waitForLogPatternFrom(targetAnchor.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	if _, err := waitForLogPatternFrom(targetAnchor.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}

	ack, err := waitForCoreAnchorEpochAckProof(coreNode, 1, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if len(ack.Proofs) < quorumMajority(anchorCount) {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("anchor epoch ACK proof has %d signatures, want majority %d", len(ack.Proofs), quorumMajority(anchorCount))
	}
	for _, node := range append(coreNodes, anchorNodes...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}

	fmt.Printf("PASS multi_node_alfp_pull_after_push_failure: %s built ALFP from core quorum after blocked core push; ACK had %d/%d signatures\n", targetAnchorName, len(ack.Proofs), anchorCount)
	return nil
}
