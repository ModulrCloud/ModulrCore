package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

const longRunning21ProgressInterval = 15 * time.Second

func longRunning21ValidatorLivenessScenario(args []string) error {
	fs := flag.NewFlagSet("scenario long_running_21_validator_liveness", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "long-running-21-validator-liveness-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	podRepo := fs.String("pod-repo", "../point-of-distribution", "path to point-of-distribution repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 3*time.Minute, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 8*time.Minute, "timeout for observing 21-validator liveness")
	targetEpoch := fs.Int("target-epoch", 6, "core epoch to reach before final liveness checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetEpoch < 2 {
		return errors.New("-target-epoch must be at least 2")
	}

	const coreCount = 21
	const anchorCount = 7
	const coreEpochDurationMs = 220_000
	const coreLeadershipDurationMs = 10_000
	const coreBlockTimeMs = 500
	const anchorEpochDurationMs = 220_000
	const anchorBlockTimeMs = 500

	selectedBasePort := *basePort
	if selectedBasePort == 0 {
		var err error
		selectedBasePort, err = findAvailableGeneratedBasePortWithPod(41000, 5000, coreCount, anchorCount)
		if err != nil {
			return err
		}
		fmt.Printf("scenario long_running_21_validator_liveness: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}
	if err := ensureGeneratedPodPortAvailable(selectedBasePort); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario long_running_21_validator_liveness: preparing %d core + %d anchors run %s to epoch %d\n", coreCount, anchorCount, *runID, *targetEpoch)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-pod-repo", *podRepo,
		"-base-port", fmt.Sprint(selectedBasePort),
		"-core-epoch-duration-ms", fmt.Sprint(coreEpochDurationMs),
		"-core-leadership-duration-ms", fmt.Sprint(coreLeadershipDurationMs),
		"-core-block-time-ms", fmt.Sprint(coreBlockTimeMs),
		"-anchor-epoch-duration-ms", fmt.Sprint(anchorEpochDurationMs),
		"-anchor-block-time-ms", fmt.Sprint(anchorBlockTimeMs),
		"-pod",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario long_running_21_validator_liveness: starting network")
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
		_ = stopState(state, 10*time.Second)
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

	initialHeights, err := waitForCoreHeightSnapshot(coreNodes, 0, 90*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	fmt.Printf("scenario long_running_21_validator_liveness: initial core heights %d..%d across %d nodes\n", initialHeights.Min, initialHeights.Max, len(initialHeights.ByNode))

	fmt.Printf("scenario long_running_21_validator_liveness: waiting for anchors to apply core transitions through epoch %d (core majority=%d/%d, anchor majority=%d/%d)\n", *targetEpoch, quorumMajority(coreCount), coreCount, quorumMajority(anchorCount), anchorCount)
	for epoch := 1; epoch <= *targetEpoch; epoch++ {
		if err := waitForAnchorCoreTransitionWithProgress(state, coreNodes, anchorNodes, epoch-1, epoch, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 180)
			return err
		}
		fmt.Printf("scenario long_running_21_validator_liveness: anchors applied core transition %d->%d (%d/%d)\n", epoch-1, epoch, epoch, *targetEpoch)
	}

	finalHeights, err := waitForCoreHeightGrowth(coreNodes, initialHeights.ByNode, 90*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	fmt.Printf("scenario long_running_21_validator_liveness: core heights advanced after transitions %d..%d -> %d..%d\n", initialHeights.Min, initialHeights.Max, finalHeights.Min, finalHeights.Max)
	sustainedHeights, err := waitForCoreHeightGrowth(coreNodes, finalHeights.ByNode, 90*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	fmt.Printf("scenario long_running_21_validator_liveness: core heights sustained growth %d..%d -> %d..%d\n", finalHeights.Min, finalHeights.Max, sustainedHeights.Min, sustainedHeights.Max)

	anchorMajority := quorumMajority(anchorCount)
	coreMajority := quorumMajority(coreCount)
	for epoch := 1; epoch <= *targetEpoch; epoch++ {
		fmt.Printf("scenario long_running_21_validator_liveness: checking anchor ACK proof %d->%d\n", epoch-1, epoch)
		ack, err := waitForAnyCoreAnchorEpochAckProof(coreNodes, epoch, 60*time.Second)
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
		fmt.Printf("scenario long_running_21_validator_liveness: anchor ACK proof %d->%d ok (%d/%d signatures)\n", ack.EpochID, ack.NextEpochID, len(ack.Proofs), anchorCount)
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

	if err := stopState(state, 10*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	recoveryManifestNodes := anchorManifestNodes[:anchorMajority]
	fmt.Printf("scenario long_running_21_validator_liveness: checking recovery latest quorum with anchor majority (%d/%d)\n", len(recoveryManifestNodes), anchorCount)
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
		if len(payload.Proof.Proofs) < coreMajority {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery proof has %d core signatures, want majority %d", recoveryAnchor.Name, len(payload.Proof.Proofs), coreMajority)
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

	fmt.Printf("PASS long_running_21_validator_liveness: %d core + %d anchors reached epoch %d, executed heights %d..%d -> %d..%d -> %d..%d, ACKs 0->1 through %d->%d had majority signatures, recovery majority=%d/%d latest=%s hash %s\n", coreCount, anchorCount, *targetEpoch, initialHeights.Min, initialHeights.Max, finalHeights.Min, finalHeights.Max, sustainedHeights.Min, sustainedHeights.Max, *targetEpoch-1, *targetEpoch, len(recoverySigners), anchorCount, expectedRange, expectedHash)
	return nil
}

func waitForAnchorCoreTransitionWithProgress(
	state RunState,
	coreNodes []NodeState,
	anchorNodes []NodeState,
	fromEpoch int,
	toEpoch int,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	startedAt := time.Now()
	nextProgressAt := startedAt
	applied := make(map[string]struct{}, len(anchorNodes))
	pattern := regexp.MustCompile(fmt.Sprintf(`Core quorum catch-up: applied epoch rotation proof %d -> %d`, fromEpoch, toEpoch))

	fmt.Printf("scenario long_running_21_validator_liveness: waiting for core transition %d->%d on %d anchors (timeout=%s)\n", fromEpoch, toEpoch, len(anchorNodes), timeout)
	for time.Now().Before(deadline) {
		for _, anchorNode := range anchorNodes {
			if _, ok := applied[anchorNode.Name]; ok {
				continue
			}
			raw, err := os.ReadFile(anchorNode.StdoutLog)
			if err == nil && pattern.FindIndex(raw) != nil {
				applied[anchorNode.Name] = struct{}{}
				fmt.Printf("scenario long_running_21_validator_liveness: anchor %s applied core transition %d->%d (%d/%d, elapsed=%s)\n", anchorNode.Name, fromEpoch, toEpoch, len(applied), len(anchorNodes), time.Since(startedAt).Round(time.Second))
			}
		}
		if len(applied) == len(anchorNodes) {
			return nil
		}

		now := time.Now()
		if !now.Before(nextProgressAt) {
			printLongRunning21TransitionProgress(state, coreNodes, anchorNodes, applied, fromEpoch, toEpoch, startedAt, deadline)
			nextProgressAt = now.Add(longRunning21ProgressInterval)
		}
		time.Sleep(500 * time.Millisecond)
	}

	pending := pendingNodeNames(anchorNodes, applied)
	return fmt.Errorf("anchors did not all apply core transition %d->%d within %s: applied=%d/%d pending=%s", fromEpoch, toEpoch, timeout, len(applied), len(anchorNodes), strings.Join(pending, ","))
}

func printLongRunning21TransitionProgress(
	state RunState,
	coreNodes []NodeState,
	anchorNodes []NodeState,
	applied map[string]struct{},
	fromEpoch int,
	toEpoch int,
	startedAt time.Time,
	deadline time.Time,
) {
	pending := pendingNodeNames(anchorNodes, applied)
	coreSummary := summarizeCoreNodesForProgress(coreNodes)
	anchorSummary := summarizeAnchorLogsForProgress(anchorNodes, fromEpoch, toEpoch)
	podSummary := summarizePodLogsForProgress(state)
	fmt.Printf(
		"scenario long_running_21_validator_liveness: progress %d->%d elapsed=%s remaining=%s applied=%d/%d pending=%s | core=%s | anchors=%s | pods=%s\n",
		fromEpoch,
		toEpoch,
		time.Since(startedAt).Round(time.Second),
		time.Until(deadline).Round(time.Second),
		len(applied),
		len(anchorNodes),
		strings.Join(pending, ","),
		coreSummary,
		anchorSummary,
		podSummary,
	)
}

func pendingNodeNames(nodes []NodeState, applied map[string]struct{}) []string {
	pending := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if _, ok := applied[node.Name]; !ok {
			pending = append(pending, node.Name)
		}
	}
	if len(pending) == 0 {
		return []string{"-"}
	}
	return pending
}

func summarizeCoreNodesForProgress(coreNodes []NodeState) string {
	readable := 0
	minHeight := int64(0)
	maxHeight := int64(0)
	ackMissing := 0
	podTimeouts := 0
	alfpCollected := 0

	for _, node := range coreNodes {
		if height, err := fetchNodeLastHeight(node); err == nil {
			if readable == 0 || height < minHeight {
				minHeight = height
			}
			if readable == 0 || height > maxHeight {
				maxHeight = height
			}
			readable++
		}
		raw, err := os.ReadFile(node.StdoutLog)
		if err != nil {
			continue
		}
		ackMissing += bytes.Count(raw, []byte("anchor_epoch_ack_missing"))
		podTimeouts += bytes.Count(raw, []byte("PoD websocket read failed"))
		alfpCollected += bytes.Count(raw, []byte("ALFP collected & leader finalized"))
	}

	if readable == 0 {
		return fmt.Sprintf("heights=unreadable ackMissing=%d podTimeouts=%d alfp=%d", ackMissing, podTimeouts, alfpCollected)
	}
	return fmt.Sprintf("heights=%d..%d readable=%d/%d ackMissing=%d podTimeouts=%d alfp=%d", minHeight, maxHeight, readable, len(coreNodes), ackMissing, podTimeouts, alfpCollected)
}

func summarizeAnchorLogsForProgress(anchorNodes []NodeState, fromEpoch int, toEpoch int) string {
	missingPattern := []byte(fmt.Sprintf("Core quorum catch-up: missing epoch rotation proof for epoch %d -> %d", fromEpoch, toEpoch))
	missing := 0
	applied := 0
	podTimeouts := 0
	podFailures := 0
	alfpIncludedBlocks := 0

	for _, node := range anchorNodes {
		raw, err := os.ReadFile(node.StdoutLog)
		if err != nil {
			continue
		}
		missing += bytes.Count(raw, missingPattern)
		applied += bytes.Count(raw, []byte(fmt.Sprintf("Core quorum catch-up: applied epoch rotation proof %d -> %d", fromEpoch, toEpoch)))
		podTimeouts += bytes.Count(raw, []byte("Anchors-PoD read failed"))
		podFailures += bytes.Count(raw, []byte("ANCHORS-CORE: failed to send message to Anchors-PoD"))
		alfpIncludedBlocks += bytes.Count(raw, []byte("ALFPs="))
	}

	return fmt.Sprintf("appliedLogs=%d missing=%d podTimeouts=%d podFailures=%d alfpBlocks=%d", applied, missing, podTimeouts, podFailures, alfpIncludedBlocks)
}

func summarizePodLogsForProgress(state RunState) string {
	podNodes := findNodesByRole(state, "pod")
	if len(podNodes) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(podNodes))
	for _, node := range podNodes {
		stderrBytes, _ := os.ReadFile(node.StderrLog)
		stdoutBytes, _ := os.ReadFile(node.StdoutLog)
		parts = append(parts, fmt.Sprintf(
			"%s(stdout=%dB stderr=%dB errors=%d)",
			node.Name,
			len(stdoutBytes),
			len(stderrBytes),
			bytes.Count(stderrBytes, []byte("error"))+bytes.Count(stderrBytes, []byte("panic"))+bytes.Count(stderrBytes, []byte("fatal")),
		))
	}
	return strings.Join(parts, ",")
}
