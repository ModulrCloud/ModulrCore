package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func longRunningStabilityWithTemporaryValidatorDownScenario(args []string) error {
	fs := flag.NewFlagSet("scenario long_running_stability_with_temporary_validator_down", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "long-running-validator-down-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 5*time.Minute, "timeout for observing temporary validator downtime")
	targetEpoch := fs.Int("target-epoch", 16, "core epoch to reach before final stability checks")
	downAtEpoch := fs.Int("down-at-epoch", 3, "core epoch after which to stop the first validator")
	downEpochs := fs.Int("down-epochs", 3, "number of core transitions to keep the first validator down")
	validatorName := fs.String("validator", "core-4", "first core validator node to stop temporarily")
	secondValidatorName := fs.String("second-validator", "core-3", "second core validator to stop after the first one recovers")
	secondDownEpochs := fs.Int("second-down-epochs", 3, "number of core transitions to keep the second validator down")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *downAtEpoch < 1 {
		return errors.New("-down-at-epoch must be at least 1")
	}
	if *downEpochs < 1 {
		return errors.New("-down-epochs must be at least 1")
	}
	if *secondDownEpochs < 1 {
		return errors.New("-second-down-epochs must be at least 1")
	}
	if *validatorName == *secondValidatorName {
		return errors.New("-validator and -second-validator must be different")
	}
	firstRestartEpoch := *downAtEpoch + *downEpochs
	secondDownAtEpoch := firstRestartEpoch + 2
	secondRestartEpoch := secondDownAtEpoch + *secondDownEpochs
	if *targetEpoch <= secondRestartEpoch+1 {
		return fmt.Errorf("-target-epoch must be greater than second restart epoch + 1 (got target=%d secondRestart=%d)", *targetEpoch, secondRestartEpoch)
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
		fmt.Printf("scenario long_running_stability_with_temporary_validator_down: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario long_running_stability_with_temporary_validator_down: preparing %d core + %d anchors run %s to epoch %d\n", coreCount, anchorCount, *runID, *targetEpoch)
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

	fmt.Println("scenario long_running_stability_with_temporary_validator_down: starting network")
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
	firstDownNode, err := findNodeByNameInList(coreNodes, *validatorName)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	firstDownPubKey, err := readNodePublicKey(firstDownNode)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	secondDownNode, err := findNodeByNameInList(coreNodes, *secondValidatorName)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	secondDownPubKey, err := readNodePublicKey(secondDownNode)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	firstDownManifestNode, err := findManifestNodeByName(manifest, firstDownNode.Name)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	secondDownManifestNode, err := findManifestNodeByName(manifest, secondDownNode.Name)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	initialHeights, err := waitForCoreHeightSnapshot(coreNodes, 0, 30*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	fmt.Printf("scenario long_running_stability_with_temporary_validator_down: initial executed heights %d..%d; first=%s pubkey=%s second=%s pubkey=%s\n", initialHeights.Min, initialHeights.Max, firstDownNode.Name, firstDownPubKey, secondDownNode.Name, secondDownPubKey)

	anchorMajority := quorumMajority(anchorCount)
	coreMajority := quorumMajority(coreCount)
	firstValidatorStopped := false
	firstValidatorRestarted := false
	secondValidatorStopped := false
	secondValidatorRestarted := false
	var firstStoppedHeight int64
	var firstRestartedHeight int64
	var secondStoppedHeight int64
	var secondRestartedHeight int64
	var sustainedHeights coreHeightSnapshot

	fmt.Printf("scenario long_running_stability_with_temporary_validator_down: waiting for core transitions through epoch %d; %s down after epoch %d for %d transitions, then %s down after epoch %d for %d transitions\n", *targetEpoch, firstDownNode.Name, *downAtEpoch, *downEpochs, secondDownNode.Name, secondDownAtEpoch, *secondDownEpochs)
	for epoch := 1; epoch <= *targetEpoch; epoch++ {
		for _, anchorNode := range anchorNodes {
			pattern := regexp.MustCompile(fmt.Sprintf(`Core quorum catch-up: applied epoch rotation proof %d -> %d`, epoch-1, epoch))
			if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, pattern, 0, *observeTimeout); err != nil {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s did not apply core quorum transition %d->%d: %w", anchorNode.Name, epoch-1, epoch, err)
			}
		}
		fmt.Printf("scenario long_running_stability_with_temporary_validator_down: anchors applied core transition %d->%d (%d/%d)\n", epoch-1, epoch, epoch, *targetEpoch)

		ack, err := waitForAnyCoreAnchorEpochAckProof(activeCoreNodes(coreNodes), epoch, 20*time.Second)
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

		if epoch == *downAtEpoch && !firstValidatorStopped {
			heightBeforeStop, ok := initialHeights.ByNode[firstDownNode.Name]
			if !ok {
				heightBeforeStop, _ = fetchNodeLastHeight(firstDownNode)
			}
			if currentHeight, err := fetchNodeLastHeight(firstDownNode); err == nil {
				heightBeforeStop = currentHeight
			}
			firstStoppedHeight = heightBeforeStop
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: stopping %s at epoch %d with executed height %d; waiting %d transitions before restart\n", firstDownNode.Name, epoch, firstStoppedHeight, *downEpochs)
			if err := stopState(RunState{Nodes: []NodeState{firstDownNode}}, 5*time.Second); err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			if err := waitForHealthURLUnavailable(firstDownNode.HealthURL, 5*time.Second); err != nil {
				printScenarioDiagnostics(state, 120)
				return fmt.Errorf("%s health URL is still available after stop: %w", firstDownNode.Name, err)
			}
			firstValidatorStopped = true
			coreNodes = replaceNode(coreNodes, firstDownNode)
			state.Nodes = replaceNode(state.Nodes, firstDownNode)
			liveCoreNames := nodeNames(nodesExcept(coreNodes, firstDownNode.Name))
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: %s is down; continuing with validators %s and watching epoch progress\n", firstDownNode.Name, strings.Join(liveCoreNames, ","))
		}

		if epoch == firstRestartEpoch && firstValidatorStopped && !firstValidatorRestarted {
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: restarting %s at epoch %d after %d down transitions; last known executed height before stop was %d\n", firstDownNode.Name, epoch, *downEpochs, firstStoppedHeight)
			restartedNode, err := startNode(firstDownManifestNode, state.LogsDir)
			if err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			if err := waitForHealthChecks([]NodeState{restartedNode}, *healthTimeout); err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			firstDownNode = restartedNode
			coreNodes = replaceNode(coreNodes, restartedNode)
			state.Nodes = replaceNode(state.Nodes, restartedNode)
			firstValidatorRestarted = true
			restartHeights, err := waitForCoreHeightGrowth([]NodeState{restartedNode}, map[string]int64{restartedNode.Name: firstStoppedHeight}, 90*time.Second)
			if err != nil {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s did not resume execution after restart: %w", restartedNode.Name, err)
			}
			firstRestartedHeight = restartHeights.ByNode[restartedNode.Name]
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: %s restarted and resumed execution: height %d -> %d\n", restartedNode.Name, firstStoppedHeight, firstRestartedHeight)
		}

		if epoch == secondDownAtEpoch && firstValidatorRestarted && !secondValidatorStopped {
			heightBeforeStop, ok := heightsByNode([]NodeState{secondDownNode})[secondDownNode.Name]
			if !ok {
				heightBeforeStop = -1
			}
			secondStoppedHeight = heightBeforeStop
			firstHeightBeforeSecondDown, _ := fetchNodeLastHeight(firstDownNode)
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: stopping %s at epoch %d with executed height %d; %s has recovered to height %d and is now needed for 3/4 quorum\n", secondDownNode.Name, epoch, secondStoppedHeight, firstDownNode.Name, firstHeightBeforeSecondDown)
			if err := stopState(RunState{Nodes: []NodeState{secondDownNode}}, 5*time.Second); err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			if err := waitForHealthURLUnavailable(secondDownNode.HealthURL, 5*time.Second); err != nil {
				printScenarioDiagnostics(state, 120)
				return fmt.Errorf("%s health URL is still available after stop: %w", secondDownNode.Name, err)
			}
			secondValidatorStopped = true
			coreNodes = replaceNode(coreNodes, secondDownNode)
			state.Nodes = replaceNode(state.Nodes, secondDownNode)
			activeAfterSecondStop := nodesExcept(coreNodes, secondDownNode.Name)
			if _, err := findNodeByNameInList(activeAfterSecondStop, firstDownNode.Name); err != nil {
				printScenarioDiagnostics(state, 120)
				return fmt.Errorf("%s is not active after %s stopped; recovered validator is required for this phase: %w", firstDownNode.Name, secondDownNode.Name, err)
			}
			activeNames := nodeNames(activeAfterSecondStop)
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: %s is down; recovered %s remains in the active validator set %s, watching epoch progress\n", secondDownNode.Name, firstDownNode.Name, strings.Join(activeNames, ","))
		}

		if epoch == secondRestartEpoch && secondValidatorStopped && !secondValidatorRestarted {
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: restarting %s at epoch %d after %d down transitions; last known executed height before stop was %d\n", secondDownNode.Name, epoch, *secondDownEpochs, secondStoppedHeight)
			restartedNode, err := startNode(secondDownManifestNode, state.LogsDir)
			if err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			if err := waitForHealthChecks([]NodeState{restartedNode}, *healthTimeout); err != nil {
				printScenarioDiagnostics(state, 120)
				return err
			}
			secondDownNode = restartedNode
			coreNodes = replaceNode(coreNodes, restartedNode)
			state.Nodes = replaceNode(state.Nodes, restartedNode)
			secondValidatorRestarted = true
			restartHeights, err := waitForCoreHeightGrowth([]NodeState{restartedNode}, map[string]int64{restartedNode.Name: secondStoppedHeight}, 90*time.Second)
			if err != nil {
				printScenarioDiagnostics(state, 180)
				return fmt.Errorf("%s did not resume execution after restart: %w", restartedNode.Name, err)
			}
			secondRestartedHeight = restartHeights.ByNode[restartedNode.Name]
			fmt.Printf("scenario long_running_stability_with_temporary_validator_down: %s restarted and resumed execution: height %d -> %d\n", restartedNode.Name, secondStoppedHeight, secondRestartedHeight)
		}
	}

	if !firstValidatorStopped || !firstValidatorRestarted {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s stop/restart path did not execute (stopped=%t restarted=%t)", firstDownNode.Name, firstValidatorStopped, firstValidatorRestarted)
	}
	if !secondValidatorStopped || !secondValidatorRestarted {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s stop/restart path did not execute (stopped=%t restarted=%t)", secondDownNode.Name, secondValidatorStopped, secondValidatorRestarted)
	}
	if firstRestartedHeight <= firstStoppedHeight {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s restarted height %d did not advance beyond stopped height %d", firstDownNode.Name, firstRestartedHeight, firstStoppedHeight)
	}
	if secondRestartedHeight <= secondStoppedHeight {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("%s restarted height %d did not advance beyond stopped height %d", secondDownNode.Name, secondRestartedHeight, secondStoppedHeight)
	}

	sustainedHeights, err = waitForCoreHeightGrowth(activeCoreNodes(coreNodes), heightsByNode(activeCoreNodes(coreNodes)), 45*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	fmt.Printf("scenario long_running_stability_with_temporary_validator_down: all active validators continue execution after restart, heights %d..%d\n", sustainedHeights.Min, sustainedHeights.Max)

	for _, node := range append(coreNodes, anchorNodes...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
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
	fmt.Printf("scenario long_running_stability_with_temporary_validator_down: checking recovery latest quorum with anchor majority (%d/%d)\n", len(recoveryManifestNodes), anchorCount)
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

	fmt.Printf("PASS long_running_stability_with_temporary_validator_down: %s stopped at epoch %d height %d, network kept applying epoch transitions, %s restarted at epoch %d and resumed to height %d; then %s stopped at epoch %d height %d, recovered %s stayed in the active 3/4 validator set while epoch transitions continued, %s restarted at epoch %d and resumed to height %d, all validators sustained heights %d..%d, recovery majority=%d/%d latest=%s hash %s\n", firstDownManifestNode.Name, *downAtEpoch, firstStoppedHeight, firstDownManifestNode.Name, firstRestartEpoch, firstRestartedHeight, secondDownManifestNode.Name, secondDownAtEpoch, secondStoppedHeight, firstDownManifestNode.Name, secondDownManifestNode.Name, secondRestartEpoch, secondRestartedHeight, sustainedHeights.Min, sustainedHeights.Max, len(recoverySigners), anchorCount, expectedRange, expectedHash)
	return nil
}
