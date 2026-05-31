package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

func recoveryFullCycleSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario recovery_full_cycle_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "recovery-full-cycle-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 240*time.Second, "timeout for observing recovery full cycle")
	targetEpoch := fs.Int("target-epoch", 1, "core epoch to reach before collecting anchor recovery majority")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetEpoch < 1 {
		return errors.New("-target-epoch must be at least 1")
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
		fmt.Printf("scenario recovery_full_cycle_smoke: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario recovery_full_cycle_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(selectedBasePort),
		"-core-epoch-duration-ms", "10000",
		"-core-leadership-duration-ms", "1800",
		"-core-block-time-ms", "800",
		"-anchor-epoch-duration-ms", "10000",
		"-anchor-block-time-ms", "800",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario recovery_full_cycle_smoke: starting original network")
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

	fmt.Printf("scenario recovery_full_cycle_smoke: waiting for anchors to know core epoch %d\n", *targetEpoch)
	for _, anchorNode := range anchorNodes {
		pattern := regexp.MustCompile(fmt.Sprintf(`Core quorum catch-up: applied epoch rotation proof %d -> %d`, *targetEpoch-1, *targetEpoch))
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, pattern, 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s did not apply core quorum transition %d->%d: %w", anchorNode.Name, *targetEpoch-1, *targetEpoch, err)
		}
	}
	recoveryHeights, err := waitForCoreHeightSnapshot(coreNodes, 0, 30*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	recoveryHeight := recoveryHeights.Max

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	coreManifestNodes := findManifestNodesByRole(manifest, "core")
	anchorManifestNodes := findManifestNodesByRole(manifest, "anchor")
	if len(coreManifestNodes) != coreCount {
		return fmt.Errorf("manifest has %d core nodes, want %d", len(coreManifestNodes), coreCount)
	}
	if len(anchorManifestNodes) != anchorCount {
		return fmt.Errorf("manifest has %d anchors, want %d", len(anchorManifestNodes), anchorCount)
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	anchorMajority := quorumMajority(anchorCount)
	recoveryManifestNodes := anchorManifestNodes[:anchorMajority]
	fmt.Printf("scenario recovery_full_cycle_smoke: collecting recovery majority (%d/%d)\n", len(recoveryManifestNodes), anchorCount)
	recoveryAnchors := make([]NodeState, 0, len(recoveryManifestNodes))
	for _, anchorManifestNode := range recoveryManifestNodes {
		if err := setAnchorRecoveryMode(runDir, anchorManifestNode.Name, true); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		recoveryNode, err := startNode(anchorManifestNode, state.LogsDir)
		if err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		recoveryAnchors = append(recoveryAnchors, recoveryNode)
	}
	if err := waitForHealthChecks(recoveryAnchors, *healthTimeout); err != nil {
		printScenarioDiagnostics(RunState{Nodes: recoveryAnchors}, 120)
		return err
	}
	state.Nodes = recoveryAnchors

	var recoveryPayload recoveryCoreQuorumPayload
	recoverySigners := make(map[string]struct{}, anchorMajority)
	var expectedRange string
	var expectedHash string
	for _, recoveryAnchor := range recoveryAnchors {
		signed, payload, err := waitForRecoveryLatestCoreQuorumAtLeast(recoveryAnchor, *targetEpoch, *observeTimeout)
		if err != nil {
			printScenarioDiagnostics(state, 180)
			return err
		}
		if payload.Proof == nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s returned empty recovery proof", recoveryAnchor.Name)
		}
		rangeLabel := fmt.Sprintf("%d->%d", payload.Proof.EpochID, payload.Proof.NextEpochID)
		if expectedRange == "" {
			expectedRange = rangeLabel
			expectedHash = payload.Proof.EpochDataHash
			recoveryPayload = payload
		} else if rangeLabel != expectedRange || payload.Proof.EpochDataHash != expectedHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery latest mismatch: got %s/%s want %s/%s", recoveryAnchor.Name, rangeLabel, payload.Proof.EpochDataHash, expectedRange, expectedHash)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("recovery majority has %d unique signers, want %d", len(recoverySigners), anchorMajority)
	}
	if recoveryPayload.Proof == nil {
		return errors.New("missing recovery payload after recovery majority collection")
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	recoveryGenesis, teamKey, err := buildRecoveryGenesisFromCoreNode(coreNodes[0], *runID)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	fmt.Printf("scenario recovery_full_cycle_smoke: registering recovery plans at heights %d..%d and network %s\n", recoveryHeights.Min, recoveryHeights.Max, recoveryGenesis.NetworkId)
	for _, coreNode := range coreNodes {
		nodeRecoveryHeight, ok := recoveryHeights.ByNode[coreNode.Name]
		if !ok {
			printScenarioDiagnostics(state, 120)
			return fmt.Errorf("missing recovery height for %s", coreNode.Name)
		}
		recoveryData, err := buildSignedRecoveryData(recoveryPayload.Proof.NextEpochID, nodeRecoveryHeight, recoveryGenesis, teamKey)
		if err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		if err := writeCoreRecoveryPlan(coreNode, recoveryData); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		if err := writeJSON(filepath.Join(coreNode.ChaindataPath, "genesis.json"), recoveryGenesis); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}
	for _, anchorManifestNode := range anchorManifestNodes {
		if err := setAnchorRecoveryMode(runDir, anchorManifestNode.Name, false); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		if err := resetAnchorRuntimeState(anchorManifestNode); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		if err := updateAnchorGenesisForRecovery(filepath.Join(runDir, "network", anchorManifestNode.Name, "genesis.json"), recoveryGenesis); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		if err := writeJSON(filepath.Join(runDir, "network", anchorManifestNode.Name, "core_genesis.json"), recoveryGenesis); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}

	fmt.Println("scenario recovery_full_cycle_smoke: starting recovered core + anchors")
	recoveredNodes := make([]NodeState, 0, len(coreManifestNodes)+len(anchorManifestNodes))
	for _, manifestNode := range append(coreManifestNodes, anchorManifestNodes...) {
		node, err := startNode(manifestNode, state.LogsDir)
		if err != nil {
			printScenarioDiagnostics(RunState{Nodes: recoveredNodes}, 120)
			return err
		}
		recoveredNodes = append(recoveredNodes, node)
	}
	state.Nodes = recoveredNodes
	if err := waitForHealthChecks(recoveredNodes, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	coreNodes = findNodesByRole(state, "core")
	anchorNodes = findNodesByRole(state, "anchor")

	for _, coreNode := range coreNodes {
		if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Recovery transition applied on startup`), 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s did not apply recovery transition: %w", coreNode.Name, err)
		}
		if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`network id mismatch`), 0, 2*time.Second); err == nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s logged network id mismatch after recovery transition", coreNode.Name)
		}
	}

	recoveredCoreNode, recoveredHeight, err := waitForAnyCoreHeight(coreNodes, recoveryHeight, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	for _, anchorNode := range anchorNodes {
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s did not apply recovered core quorum transition 0->1: %w", anchorNode.Name, err)
		}
	}
	ack, err := waitForAnyCoreAnchorEpochAckProof(coreNodes, 1, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	if ack.EpochID != 0 || ack.NextEpochID != 1 || len(ack.Proofs) < anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("unexpected recovered anchor ACK proof: range %d->%d signatures=%d majority=%d", ack.EpochID, ack.NextEpochID, len(ack.Proofs), anchorMajority)
	}

	fmt.Printf("PASS recovery_full_cycle_smoke: anchor majority %d/%d agreed on %s at original heights %d..%d; recovered core network %s advanced %s global height to %d and collected ACK %d->%d (%d signatures)\n", len(recoverySigners), anchorCount, expectedRange, recoveryHeights.Min, recoveryHeights.Max, recoveryGenesis.NetworkId, recoveredCoreNode.Name, recoveredHeight, ack.EpochID, ack.NextEpochID, len(ack.Proofs))
	return nil
}
