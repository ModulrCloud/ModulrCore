package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func multiNodeLaggingAnchorCatchupScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_lagging_anchor_catchup", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-lagging-anchor-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 41000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 240*time.Second, "timeout for observing lagging anchor recovery catch-up")
	targetEpoch := fs.Int("target-epoch", 2, "core epoch to reach before restoring a lagging anchor snapshot")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetEpoch < 2 {
		return errors.New("-target-epoch must be at least 2")
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_lagging_anchor_catchup: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "10000",
		"-core-leadership-duration-ms", "1800",
		"-core-block-time-ms", "800",
		"-anchor-epoch-duration-ms", "10000",
		"-anchor-block-time-ms", "800",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario multi_node_lagging_anchor_catchup: starting nodes")
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

	coreNode := coreNodes[0]
	laggingAnchor := anchorNodes[len(anchorNodes)-1]
	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(laggingAnchor.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("%s did not apply baseline core quorum transition 0->1: %w", laggingAnchor.Name, err)
	}

	fmt.Printf("scenario multi_node_lagging_anchor_catchup: snapshotting %s after epoch 1\n", laggingAnchor.Name)
	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	laggingSnapshotDir := filepath.Join(runDir, "snapshots", laggingAnchor.Name+"-epoch-1")
	if err := os.RemoveAll(laggingSnapshotDir); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := copyDir(laggingAnchor.ChaindataPath, laggingSnapshotDir); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	fmt.Printf("scenario multi_node_lagging_anchor_catchup: restarting full network and advancing to epoch %d\n", *targetEpoch)
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
	state, err = loadState(runDir)
	if err != nil {
		return err
	}
	coreNodes = findNodesByRole(state, "core")
	anchorNodes = findNodesByRole(state, "anchor")
	if len(coreNodes) != coreCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d core nodes after restart, got %d", coreCount, len(coreNodes))
	}
	if len(anchorNodes) != anchorCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d anchor nodes after restart, got %d", anchorCount, len(anchorNodes))
	}
	coreNode = coreNodes[0]
	laggingAnchor = anchorNodes[len(anchorNodes)-1]

	if err := waitForCoreEpochAtLeast(coreNode, *targetEpoch, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	for _, anchorNode := range anchorNodes {
		pattern := regexp.MustCompile(fmt.Sprintf(`Core quorum catch-up: applied epoch rotation proof %d -> %d`, *targetEpoch-1, *targetEpoch))
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, pattern, 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s did not apply core quorum transition %d->%d: %w", anchorNode.Name, *targetEpoch-1, *targetEpoch, err)
		}
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := os.RemoveAll(laggingAnchor.ChaindataPath); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := copyDir(laggingSnapshotDir, laggingAnchor.ChaindataPath); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
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

	var laggingManifestNode ManifestNode
	peerManifestNodes := make([]ManifestNode, 0, anchorCount-1)
	for _, anchorManifestNode := range anchorManifestNodes {
		if anchorManifestNode.Name == laggingAnchor.Name {
			laggingManifestNode = anchorManifestNode
			continue
		}
		peerManifestNodes = append(peerManifestNodes, anchorManifestNode)
	}
	if laggingManifestNode.Name == "" {
		return fmt.Errorf("manifest node for lagging anchor %s not found", laggingAnchor.Name)
	}
	if len(peerManifestNodes) < quorumMajority(anchorCount)-1 {
		return fmt.Errorf("manifest has %d recovery peers, need at least %d", len(peerManifestNodes), quorumMajority(anchorCount)-1)
	}

	recoveryNodes := make([]NodeState, 0, anchorCount)
	for _, peerManifestNode := range peerManifestNodes {
		if err := enableAnchorRecoveryMode(runDir, peerManifestNode.Name); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		recoveryNode, err := startNode(peerManifestNode, state.LogsDir)
		if err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
		recoveryNodes = append(recoveryNodes, recoveryNode)
	}
	state.Nodes = recoveryNodes
	if err := waitForHealthChecks(recoveryNodes, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if err := enableAnchorRecoveryMode(runDir, laggingManifestNode.Name); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	laggingRecoveryAnchor, err := startNode(laggingManifestNode, state.LogsDir)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	state.Nodes = append(state.Nodes, laggingRecoveryAnchor)
	if err := waitForHealthChecks([]NodeState{laggingRecoveryAnchor}, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	signed, payload, err := waitForRecoveryCoreQuorum(laggingRecoveryAnchor, *targetEpoch, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	if !cryptography.VerifySignature(string(signed.Payload), signed.PubKey, signed.Signature) {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s returned recovery response with invalid anchor signature", laggingRecoveryAnchor.Name)
	}
	if signed.PubKey == "" || payload.Proof == nil {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s returned incomplete recovery response", laggingRecoveryAnchor.Name)
	}
	if payload.Proof.NextEpochID < *targetEpoch {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovered only to epoch %d, want at least %d", laggingRecoveryAnchor.Name, payload.Proof.NextEpochID, *targetEpoch)
	}
	if payload.RecoveryViewFromEpoch != 1 {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovery view started from epoch %d, want lagging durable epoch 1", laggingRecoveryAnchor.Name, payload.RecoveryViewFromEpoch)
	}
	if payload.RecoveryViewSource != "memory_catchup" {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovery view source %q, want memory_catchup", laggingRecoveryAnchor.Name, payload.RecoveryViewSource)
	}
	if payload.RecoveryViewEpoch != payload.Proof.NextEpochID {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovery view epoch mismatch: got %d, proof next epoch %d", laggingRecoveryAnchor.Name, payload.RecoveryViewEpoch, payload.Proof.NextEpochID)
	}
	if payload.RecoveryViewEpochDataHash == "" || payload.RecoveryViewEpochDataHash != payload.Proof.EpochDataHash {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovery view hash mismatch: got %q, proof hash %q", laggingRecoveryAnchor.Name, payload.RecoveryViewEpochDataHash, payload.Proof.EpochDataHash)
	}
	if len(payload.Proof.Proofs) < quorumMajority(coreCount) {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s recovered proof has %d core signatures, want majority %d", laggingRecoveryAnchor.Name, len(payload.Proof.Proofs), quorumMajority(coreCount))
	}

	fmt.Printf("PASS multi_node_lagging_anchor_catchup: %s restarted from durable epoch 1 and recovered latest core quorum %d->%d via in-memory catch-up\n", laggingRecoveryAnchor.Name, payload.Proof.EpochID, payload.Proof.NextEpochID)
	return nil
}
