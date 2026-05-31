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

func multiNodeNetworkPartitionNoFalseMajorityScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_network_partition_no_false_majority", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-partition-no-majority-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 43000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 210*time.Second, "timeout for observing partition recovery responses")
	targetEpoch := fs.Int("target-epoch", 2, "core epoch to reach before testing partitioned recovery")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetEpoch < 1 {
		return errors.New("-target-epoch must be at least 1")
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_network_partition_no_false_majority: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	fmt.Println("scenario multi_node_network_partition_no_false_majority: starting nodes")
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

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorMajority := quorumMajority(anchorCount)
	partitionSize := anchorMajority - 1
	if partitionSize <= 0 {
		return fmt.Errorf("invalid partition size %d for anchor majority %d", partitionSize, anchorMajority)
	}
	anchorManifestNodes := findManifestNodesByRole(manifest, "anchor")
	if len(anchorManifestNodes) < anchorCount {
		return fmt.Errorf("manifest has %d anchors, want %d", len(anchorManifestNodes), anchorCount)
	}

	partitionAnchors := make([]NodeState, 0, partitionSize)
	for idx := 0; idx < partitionSize; idx++ {
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
		partitionAnchors = append(partitionAnchors, recoveryNode)
	}
	state.Nodes = partitionAnchors
	if err := waitForHealthChecks(partitionAnchors, *healthTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	recoverySigners := make(map[string]struct{}, partitionSize)
	var expectedEpoch int
	var expectedHash string
	for _, recoveryAnchor := range partitionAnchors {
		signed, payload, err := waitForRecoveryCoreQuorum(recoveryAnchor, *targetEpoch, *observeTimeout)
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
		if len(payload.Proof.Proofs) < quorumMajority(coreCount) {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s recovery proof has %d core signatures, want majority %d", recoveryAnchor.Name, len(payload.Proof.Proofs), quorumMajority(coreCount))
		}
		if expectedHash == "" {
			expectedEpoch = payload.Proof.NextEpochID
			expectedHash = payload.Proof.EpochDataHash
		} else if payload.Proof.NextEpochID != expectedEpoch || payload.Proof.EpochDataHash != expectedHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s disagreed inside minority partition: got epoch %d hash %s, want epoch %d hash %s", recoveryAnchor.Name, payload.Proof.NextEpochID, payload.Proof.EpochDataHash, expectedEpoch, expectedHash)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) >= anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("partition produced false recovery majority: got %d unique anchors, majority is %d", len(recoverySigners), anchorMajority)
	}

	fmt.Printf("PASS multi_node_network_partition_no_false_majority: partition returned %d/%d signed core quorum responses, below recovery majority %d\n", len(recoverySigners), anchorCount, anchorMajority)
	return nil
}
