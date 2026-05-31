package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

func anchorRotationAarpNoInitialBlockSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario anchor_rotation_aarp_no_initial_block_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "anchor-rotation-aarp-no-initial-block-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 4*time.Minute, "timeout for observing anchor rotation and AARP propagation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	const coreCount = 4
	const anchorCount = 4
	selectedBasePort := *basePort
	if selectedBasePort == 0 {
		var err error
		selectedBasePort, err = findAvailableGeneratedBasePort(35000, 4000, coreCount, anchorCount)
		if err != nil {
			return err
		}
		fmt.Printf("scenario anchor_rotation_aarp_no_initial_block_smoke: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario anchor_rotation_aarp_no_initial_block_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(selectedBasePort),
		"-core-epoch-duration-ms", "60000",
		"-core-leadership-duration-ms", "15000",
		"-core-block-time-ms", "500",
		"-anchor-epoch-duration-ms", "60000",
		"-anchor-block-time-ms", "500",
		"-anchor-health-check-interval-ms", "5000",
		"-overwrite",
	}); err != nil {
		return err
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	downAnchor, err := manifestNodeByName(manifest, "anchor-1")
	if err != nil {
		return err
	}

	fmt.Println("scenario anchor_rotation_aarp_no_initial_block_smoke: starting nodes without anchor-1")
	state, err := startManifestSubset(manifestPath, *runRoot, *runID, map[string]bool{"anchor-1": true}, *healthTimeout)
	if err != nil {
		if loadedState, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(loadedState, 160)
		}
		return err
	}
	defer func() {
		_ = stopState(state, 5*time.Second)
	}()

	coreNodes := findNodesByRole(state, "core")
	anchorNodes := findNodesByRole(state, "anchor")
	if len(coreNodes) != coreCount {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("expected %d core nodes, got %d", coreCount, len(coreNodes))
	}
	if len(anchorNodes) != anchorCount-1 {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("expected %d active anchor nodes, got %d", anchorCount-1, len(anchorNodes))
	}
	if err := waitForHealthURLUnavailable(downAnchor.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("%s unexpectedly accepts health requests: %w", downAnchor.Name, err)
	}

	coreNode := coreNodes[0]
	activeAnchors := anchorNodes

	aarpCollectedPattern := regexp.MustCompile(`Anchor rotation: collected [0-9]+ signatures for .* in epoch 0`)
	aarpIncludedPattern := regexp.MustCompile(`New block generated 0:.*\| AARPs=[1-9][0-9]*`)
	coreAcceptedPattern := regexp.MustCompile(`Anchor rotation monitor: accepted AARP chain for epoch 0 anchorIndex=0 -> foundInAnchorIndex=1 lastBlockIndex=-1`)
	leaderAlignedPattern := regexp.MustCompile(`Sequence alignment: last block for leader .* in epoch 0`)

	if _, err := waitForAnyLogPattern(activeAnchors, aarpCollectedPattern, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 220)
		return err
	}
	if _, err := waitForAnyLogPattern(activeAnchors, aarpIncludedPattern, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 220)
		return err
	}
	if _, err := waitForAnyLogPattern(coreNodes, coreAcceptedPattern, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 220)
		return err
	}
	if _, err := waitForAnyLogPattern(coreNodes, leaderAlignedPattern, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 220)
		return err
	}

	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated epoch rotation proof sent for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 220)
		return err
	}
	for _, anchorNode := range activeAnchors {
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 220)
			return fmt.Errorf("%s did not apply core quorum transition 0->1 after zero-block AARP rotation: %w", anchorNode.Name, err)
		}
	}

	baselineHeights := heightsByNode(coreNodes)
	if _, err := waitForCoreHeightGrowth(coreNodes, baselineHeights, 45*time.Second); err != nil {
		printScenarioDiagnostics(state, 220)
		return fmt.Errorf("core block execution did not continue after zero-block AARP rotation: %w", err)
	}

	for _, node := range append(coreNodes, activeAnchors...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 160)
			return err
		}
	}

	fmt.Printf("PASS anchor_rotation_aarp_no_initial_block_smoke: %s was never started, active anchors included zero-block AARP, core accepted lastBlockIndex=-1, core transition 0->1 completed, and block execution continued\n", downAnchor.Name)
	return nil
}
