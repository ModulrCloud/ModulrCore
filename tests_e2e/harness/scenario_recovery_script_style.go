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

func recoveryScriptStyleScenario(args []string) error {
	fs := flag.NewFlagSet("scenario recovery_script_style", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "recovery-script-style-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 45000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 240*time.Second, "timeout for observing recovery script flow")
	targetEpoch := fs.Int("target-epoch", 2, "latest core epoch the recovery client should collect")
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
	fmt.Printf("scenario recovery_script_style: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	fmt.Println("scenario recovery_script_style: starting full network")
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

	laggingAnchor := anchorNodes[len(anchorNodes)-1]
	if _, err := waitForLogPatternFrom(laggingAnchor.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("%s did not apply baseline core quorum transition 0->1: %w", laggingAnchor.Name, err)
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
	for _, anchorManifestNode := range anchorManifestNodes {
		if anchorManifestNode.Name == laggingAnchor.Name {
			laggingManifestNode = anchorManifestNode
			break
		}
	}
	if laggingManifestNode.Name == "" {
		return fmt.Errorf("manifest node for lagging anchor %s not found", laggingAnchor.Name)
	}

	fmt.Printf("scenario recovery_script_style: snapshotting stale recovery participant %s after epoch 1\n", laggingAnchor.Name)
	if err := stopState(RunState{Nodes: []NodeState{laggingAnchor}}, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := waitForHealthURLUnavailable(laggingAnchor.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("%s health URL is still available after stop: %w", laggingAnchor.Name, err)
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

	restartedLaggingAnchor, err := startNode(laggingManifestNode, state.LogsDir)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := waitForHealthChecks([]NodeState{restartedLaggingAnchor}, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	for idx, node := range state.Nodes {
		if node.Name == restartedLaggingAnchor.Name {
			state.Nodes[idx] = restartedLaggingAnchor
			break
		}
	}

	fmt.Printf("scenario recovery_script_style: advancing live network to epoch %d\n", *targetEpoch)
	anchorNodes = findNodesByRole(state, "anchor")
	if len(anchorNodes) != anchorCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d anchor nodes after lagging restart, got %d", anchorCount, len(anchorNodes))
	}
	laggingAnchor = anchorNodes[len(anchorNodes)-1]
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

	recoveryManifestNodes := make([]ManifestNode, 0, quorumMajority(anchorCount))
	for _, anchorManifestNode := range anchorManifestNodes {
		if anchorManifestNode.Name == laggingAnchor.Name {
			continue
		}
		if len(recoveryManifestNodes) < quorumMajority(anchorCount)-1 {
			recoveryManifestNodes = append(recoveryManifestNodes, anchorManifestNode)
		}
	}
	recoveryManifestNodes = append(recoveryManifestNodes, laggingManifestNode)
	anchorMajority := quorumMajority(anchorCount)
	if len(recoveryManifestNodes) != anchorMajority {
		return fmt.Errorf("selected %d recovery anchors, want majority %d", len(recoveryManifestNodes), anchorMajority)
	}

	fmt.Printf("scenario recovery_script_style: starting recovery majority (%d/%d), including stale %s\n", len(recoveryManifestNodes), anchorCount, laggingAnchor.Name)
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
	var expectedEpoch int
	var expectedHash string
	var expectedRange string
	var laggingCaughtUp bool
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
		if payload.Proof.NextEpochID < *targetEpoch {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s returned recovery epoch %d, want at least %d", recoveryAnchor.Name, payload.Proof.NextEpochID, *targetEpoch)
		}
		if payload.RecoveryViewEpoch != payload.Proof.NextEpochID {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery view epoch mismatch: got %d, proof next epoch %d", recoveryAnchor.Name, payload.RecoveryViewEpoch, payload.Proof.NextEpochID)
		}
		if payload.RecoveryViewEpochDataHash == "" || payload.RecoveryViewEpochDataHash != payload.Proof.EpochDataHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery view hash mismatch: got %q, proof hash %q", recoveryAnchor.Name, payload.RecoveryViewEpochDataHash, payload.Proof.EpochDataHash)
		}
		if len(payload.Proof.Proofs) < quorumMajority(coreCount) {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery proof has %d core signatures, want majority %d", recoveryAnchor.Name, len(payload.Proof.Proofs), quorumMajority(coreCount))
		}
		rangeLabel := fmt.Sprintf("%d->%d", payload.Proof.EpochID, payload.Proof.NextEpochID)
		if expectedHash == "" {
			expectedEpoch = payload.Proof.NextEpochID
			expectedHash = payload.Proof.EpochDataHash
			expectedRange = rangeLabel
		} else if payload.Proof.NextEpochID != expectedEpoch || payload.Proof.EpochDataHash != expectedHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s disagreed on recovery latest view: got %s hash %s, want %s hash %s", recoveryAnchor.Name, rangeLabel, payload.Proof.EpochDataHash, expectedRange, expectedHash)
		}
		if recoveryAnchor.Name == laggingAnchor.Name {
			if payload.RecoveryViewFromEpoch != 1 {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s recovery view started from epoch %d, want stale durable epoch 1", recoveryAnchor.Name, payload.RecoveryViewFromEpoch)
			}
			if payload.RecoveryViewSource != "memory_catchup" {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s recovery view source %q, want memory_catchup", recoveryAnchor.Name, payload.RecoveryViewSource)
			}
			laggingCaughtUp = true
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("collected recovery responses from %d unique anchors, want majority %d", len(recoverySigners), anchorMajority)
	}
	if !laggingCaughtUp {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("lagging anchor %s was not part of recovery majority responses", laggingAnchor.Name)
	}

	fmt.Printf("PASS recovery_script_style: recovery majority=%d/%d converged on latest core quorum %s hash %s; stale %s caught up in memory\n", len(recoverySigners), anchorCount, expectedRange, expectedHash, laggingAnchor.Name)
	return nil
}
