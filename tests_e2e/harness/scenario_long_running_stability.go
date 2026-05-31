package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func longRunningStabilityScenario(args []string) error {
	fs := flag.NewFlagSet("scenario long_running_stability", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "long-running-stability-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 5*time.Minute, "timeout for observing long-running stability")
	targetEpoch := fs.Int("target-epoch", 10, "core epoch to reach before final stability checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetEpoch < 2 {
		return errors.New("-target-epoch must be at least 2")
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
		fmt.Printf("scenario long_running_stability: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario long_running_stability: preparing %d core + %d anchors run %s to epoch %d\n", coreCount, anchorCount, *runID, *targetEpoch)
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

	fmt.Println("scenario long_running_stability: starting network")
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

	initialHeights, err := waitForCoreHeightSnapshot(coreNodes, 0, 30*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	fmt.Printf("scenario long_running_stability: waiting for anchors to apply core transitions through epoch %d\n", *targetEpoch)
	for epoch := 1; epoch <= *targetEpoch; epoch++ {
		for _, anchorNode := range anchorNodes {
			pattern := regexp.MustCompile(fmt.Sprintf(`Core quorum catch-up: applied epoch rotation proof %d -> %d`, epoch-1, epoch))
			if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, pattern, 0, *observeTimeout); err != nil {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s did not apply core quorum transition %d->%d: %w", anchorNode.Name, epoch-1, epoch, err)
			}
		}
		fmt.Printf("scenario long_running_stability: anchors applied core transition %d->%d (%d/%d)\n", epoch-1, epoch, epoch, *targetEpoch)
	}

	finalHeights, err := waitForCoreHeightGrowth(coreNodes, initialHeights.ByNode, 45*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	sustainedHeights, err := waitForCoreHeightGrowth(coreNodes, finalHeights.ByNode, 45*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	anchorMajority := quorumMajority(anchorCount)
	for epoch := 1; epoch <= *targetEpoch; epoch++ {
		ack, err := waitForAnyCoreAnchorEpochAckProof(coreNodes, epoch, 20*time.Second)
		if err != nil {
			printScenarioDiagnostics(state, 180)
			return err
		}
		if ack.EpochID != epoch-1 || ack.NextEpochID != epoch {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("unexpected anchor ACK proof for lookup epoch %d: got %d->%d", epoch, ack.EpochID, ack.NextEpochID)
		}
		if len(ack.Proofs) < anchorMajority {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("anchor ACK proof %d->%d has %d signatures, want majority %d", ack.EpochID, ack.NextEpochID, len(ack.Proofs), anchorMajority)
		}
	}

	for _, node := range append(coreNodes, anchorNodes...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorManifestNodes := findManifestNodesByRole(manifest, "anchor")
	if len(anchorManifestNodes) != anchorCount {
		return fmt.Errorf("manifest has %d anchors, want %d", len(anchorManifestNodes), anchorCount)
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	recoveryManifestNodes := anchorManifestNodes[:anchorMajority]
	fmt.Printf("scenario long_running_stability: checking recovery latest quorum with anchor majority (%d/%d)\n", len(recoveryManifestNodes), anchorCount)
	recoveryAnchors := make([]NodeState, 0, len(recoveryManifestNodes))
	for _, anchorManifestNode := range recoveryManifestNodes {
		if err := enableAnchorRecoveryMode(runDir, anchorManifestNode.Name); err != nil {
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
	state.Nodes = recoveryAnchors
	if err := waitForHealthChecks(recoveryAnchors, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	recoverySigners := make(map[string]struct{}, anchorMajority)
	var expectedRange string
	var expectedHash string
	for _, recoveryAnchor := range recoveryAnchors {
		signed, payload, err := waitForRecoveryLatestCoreQuorumAtLeast(recoveryAnchor, *targetEpoch, *observeTimeout)
		if err != nil {
			printScenarioDiagnostics(state, 180)
			return err
		}
		if !cryptography.VerifySignature(string(signed.Payload), signed.PubKey, signed.Signature) {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s returned recovery response with invalid anchor signature", recoveryAnchor.Name)
		}
		if payload.Proof == nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s returned recovery payload with no proof", recoveryAnchor.Name)
		}
		if len(payload.Proof.Proofs) < quorumMajority(coreCount) {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery proof has %d core signatures, want majority %d", recoveryAnchor.Name, len(payload.Proof.Proofs), quorumMajority(coreCount))
		}
		if payload.RecoveryViewEpoch != payload.Proof.NextEpochID {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery view epoch mismatch: got %d, proof next epoch %d", recoveryAnchor.Name, payload.RecoveryViewEpoch, payload.Proof.NextEpochID)
		}
		if payload.RecoveryViewEpochDataHash == "" || payload.RecoveryViewEpochDataHash != payload.Proof.EpochDataHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery view hash mismatch: got %q, proof hash %q", recoveryAnchor.Name, payload.RecoveryViewEpochDataHash, payload.Proof.EpochDataHash)
		}
		rangeLabel := fmt.Sprintf("%d->%d", payload.Proof.EpochID, payload.Proof.NextEpochID)
		if expectedRange == "" {
			expectedRange = rangeLabel
			expectedHash = payload.Proof.EpochDataHash
		} else if rangeLabel != expectedRange || payload.Proof.EpochDataHash != expectedHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s disagreed on recovery latest view: got %s hash %s, want %s hash %s", recoveryAnchor.Name, rangeLabel, payload.Proof.EpochDataHash, expectedRange, expectedHash)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("collected recovery responses from %d unique anchors, want majority %d", len(recoverySigners), anchorMajority)
	}

	fmt.Printf("PASS long_running_stability: %d core + %d anchors reached epoch %d, executed heights %d..%d -> %d..%d -> %d..%d, ACKs 0->1 through %d->%d had majority signatures, recovery majority=%d/%d latest=%s hash %s\n", coreCount, anchorCount, *targetEpoch, initialHeights.Min, initialHeights.Max, finalHeights.Min, finalHeights.Max, sustainedHeights.Min, sustainedHeights.Max, *targetEpoch-1, *targetEpoch, len(recoverySigners), anchorCount, expectedRange, expectedHash)
	return nil
}
