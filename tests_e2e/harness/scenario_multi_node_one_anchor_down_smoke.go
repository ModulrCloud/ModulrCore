package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func multiNodeOneAnchorDownSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_one_anchor_down_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-one-anchor-down-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 27000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 180*time.Second, "timeout for observing degraded quorum flow")
	if err := fs.Parse(args); err != nil {
		return err
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_one_anchor_down_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "16000",
		"-core-leadership-duration-ms", "2500",
		"-core-block-time-ms", "900",
		"-anchor-epoch-duration-ms", "16000",
		"-anchor-block-time-ms", "900",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario multi_node_one_anchor_down_smoke: starting nodes")
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

	downAnchor := anchorNodes[len(anchorNodes)-1]
	activeAnchors := anchorNodes[:len(anchorNodes)-1]
	fmt.Printf("scenario multi_node_one_anchor_down_smoke: stopping %s before epoch rotation\n", downAnchor.Name)
	if err := stopState(RunState{Nodes: []NodeState{downAnchor}}, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := waitForHealthURLUnavailable(downAnchor.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("%s still accepts health requests after stop: %w", downAnchor.Name, err)
	}

	coreNode := coreNodes[0]
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated epoch rotation proof sent for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	for _, anchorNode := range activeAnchors {
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s did not apply core quorum transition 0->1: %w", anchorNode.Name, err)
		}
	}

	anchorMajority := quorumMajority(anchorCount)
	ack, err := waitForCoreAnchorEpochAckProof(coreNode, 1, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if ack.EpochID != 0 || ack.NextEpochID != 1 {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("unexpected anchor epoch ACK proof range: got %d->%d, want 0->1", ack.EpochID, ack.NextEpochID)
	}
	if len(ack.Proofs) < anchorMajority {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("anchor epoch ACK proof has %d signatures, want majority %d", len(ack.Proofs), anchorMajority)
	}
	if len(ack.Proofs) >= anchorCount {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("anchor epoch ACK proof unexpectedly has all %d signatures while %s was down", len(ack.Proofs), downAnchor.Name)
	}
	for _, node := range append(coreNodes, activeAnchors...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorManifestNodes := findManifestNodesByRole(manifest, "anchor")
	if len(anchorManifestNodes) < anchorMajority {
		return fmt.Errorf("manifest has %d anchors, need recovery majority %d", len(anchorManifestNodes), anchorMajority)
	}

	recoveryAnchors := make([]NodeState, 0, anchorMajority)
	for idx := 0; idx < anchorMajority; idx++ {
		anchorManifestNode := anchorManifestNodes[idx]
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

	recoverySigners := make(map[string]struct{}, anchorMajority)
	for _, recoveryAnchor := range recoveryAnchors {
		signed, payload, err := waitForRecoveryLatestCoreQuorum(recoveryAnchor, *observeTimeout)
		if err != nil {
			printScenarioDiagnostics(state, 160)
			return err
		}
		if !cryptography.VerifySignature(string(signed.Payload), signed.PubKey, signed.Signature) {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned recovery response with invalid anchor signature", recoveryAnchor.Name)
		}
		if payload.Proof == nil {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned recovery payload with no proof", recoveryAnchor.Name)
		}
		if payload.Proof.NextEpochID < 1 || payload.Proof.NextEpochID != payload.Proof.EpochID+1 {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned unexpected recovery proof range %d->%d", recoveryAnchor.Name, payload.Proof.EpochID, payload.Proof.NextEpochID)
		}
		if payload.RecoveryViewEpoch != payload.Proof.NextEpochID {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s recovery view epoch mismatch: got %d, proof next epoch %d", recoveryAnchor.Name, payload.RecoveryViewEpoch, payload.Proof.NextEpochID)
		}
		if payload.RecoveryViewEpochDataHash == "" || payload.RecoveryViewEpochDataHash != payload.Proof.EpochDataHash {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s recovery view hash mismatch: got %q, proof hash %q", recoveryAnchor.Name, payload.RecoveryViewEpochDataHash, payload.Proof.EpochDataHash)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("collected recovery responses from %d unique anchors, want majority %d", len(recoverySigners), anchorMajority)
	}

	fmt.Printf("PASS multi_node_one_anchor_down_smoke: %s stayed down, ACK had %d/%d signatures, recovery majority=%d\n", downAnchor.Name, len(ack.Proofs), anchorCount, len(recoverySigners))
	return nil
}
