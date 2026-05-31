package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

func anchorRotationAarpSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario anchor_rotation_aarp_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "anchor-rotation-aarp-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
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
		fmt.Printf("scenario anchor_rotation_aarp_smoke: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario anchor_rotation_aarp_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	fmt.Println("scenario anchor_rotation_aarp_smoke: starting nodes")
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

	coreNodes := findNodesByRole(state, "core")
	anchorNodes := findNodesByRole(state, "anchor")
	if len(coreNodes) != coreCount {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("expected %d core nodes, got %d", coreCount, len(coreNodes))
	}
	if len(anchorNodes) != anchorCount {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("expected %d anchor nodes, got %d", anchorCount, len(anchorNodes))
	}

	downAnchor := anchorNodes[0]
	activeAnchors := anchorNodes[1:]
	coreNode := coreNodes[0]

	if _, err := waitForLogPatternFrom(downAnchor.StdoutLog, regexp.MustCompile(`Approved height for epoch .*0 .*is .*0 `), 0, 45*time.Second); err != nil {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s did not finalize its first anchor block before stop: %w", downAnchor.Name, err)
	}

	fmt.Printf("scenario anchor_rotation_aarp_smoke: stopping active anchor %s after its first finalized block to force AARP rotation\n", downAnchor.Name)
	if err := stopState(RunState{Nodes: []NodeState{downAnchor}}, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if err := waitForHealthURLUnavailable(downAnchor.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("%s still accepts health requests after stop: %w", downAnchor.Name, err)
	}

	aarpCollectedPattern := regexp.MustCompile(`Anchor rotation: collected [0-9]+ signatures for .* in epoch 0`)
	aarpIncludedPattern := regexp.MustCompile(`New block generated 0:.*\| AARPs=[1-9][0-9]*`)
	coreAcceptedPattern := regexp.MustCompile(`Anchor rotation monitor: accepted AARP chain for epoch 0 anchorIndex=0 -> foundInAnchorIndex=1`)
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
			return fmt.Errorf("%s did not apply core quorum transition 0->1 after AARP rotation: %w", anchorNode.Name, err)
		}
	}

	for _, node := range append(coreNodes, activeAnchors...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 160)
			return err
		}
	}

	fmt.Printf("PASS anchor_rotation_aarp_smoke: %s stopped, active anchors collected and included AARP, core accepted anchor rotation chain, and core transition 0->1 completed\n", downAnchor.Name)
	return nil
}
