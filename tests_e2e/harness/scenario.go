package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func scenarioCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("missing scenario name")
	}

	switch args[0] {
	case "bootstrap_smoke":
		return bootstrapSmokeScenario(args[1:])
	case "alfp_pull_smoke":
		return alfpPullSmokeScenario(args[1:])
	case "epoch_anchor_ack_smoke":
		return epochAnchorAckSmokeScenario(args[1:])
	case "recovery_latest_quorum_smoke":
		return recoveryLatestQuorumSmokeScenario(args[1:])
	case "multi_node_quorum_smoke":
		return multiNodeQuorumSmokeScenario(args[1:])
	case "multi_node_one_anchor_down_smoke":
		return multiNodeOneAnchorDownSmokeScenario(args[1:])
	case "multi_node_one_core_down_smoke":
		return multiNodeOneCoreDownSmokeScenario(args[1:])
	default:
		return fmt.Errorf("unknown scenario %q", args[0])
	}
}

func bootstrapSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario bootstrap_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "bootstrap-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 12*time.Second, "timeout for observing core height growth")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario bootstrap_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario bootstrap_smoke: starting nodes")
	if err := startCmd([]string{
		"-manifest", manifestPath,
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-health-timeout", healthTimeout.String(),
	}); err != nil {
		if state, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(state, 80)
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

	coreNode, err := findNodeByRole(state, "core")
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}

	initialHeight, err := waitForCoreHeight(coreNode, -1, 5*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	nextHeight, err := waitForCoreHeight(coreNode, initialHeight, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 80)
		return err
	}
	if ok, err := checkHealthURL(http.Client{Timeout: 750 * time.Millisecond}, anchorNode.HealthURL); !ok {
		printScenarioDiagnostics(state, 80)
		return fmt.Errorf("anchor health check failed after startup: %v", err)
	}

	fmt.Printf("PASS bootstrap_smoke: core height advanced from %d to %d; anchor is healthy\n", initialHeight, nextHeight)
	return nil
}

func alfpPullSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario alfp_pull_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "alfp-pull-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 60*time.Second, "timeout for observing proactive ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario alfp_pull_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	anchorHTTPURL := fmt.Sprintf("http://localhost:%d", *basePort+2000)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorsHTTPURL(runDir, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario alfp_pull_smoke: blocking core ALFP POSTs via proxy %s -> %s\n", proxyURL, anchorHTTPURL)
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

	coreNode, err := findNodeByRole(state, "core")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	proactiveLogEnd, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	fmt.Println("PASS alfp_pull_smoke: anchor built ALFP from core quorum and included it in an anchor block")
	return nil
}

func epochAnchorAckSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario epoch_anchor_ack_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "epoch-anchor-ack-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 60*time.Second, "timeout for observing epoch rotation and anchor ACK")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario epoch_anchor_ack_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario epoch_anchor_ack_smoke: starting nodes")
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

	coreNode, err := findNodeByRole(state, "core")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated epoch rotation proof sent for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated anchor epoch ack proof collected and delivered for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	ack, err := waitForCoreAnchorEpochAckProof(coreNode, 1, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if ack.EpochID != 0 || ack.NextEpochID != 1 {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("unexpected anchor epoch ACK proof range: got %d->%d, want 0->1", ack.EpochID, ack.NextEpochID)
	}
	if len(ack.Proofs) == 0 {
		printScenarioDiagnostics(state, 160)
		return errors.New("anchor epoch ACK proof has no signatures")
	}
	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	fmt.Printf("PASS epoch_anchor_ack_smoke: core stored anchor epoch ACK proof for %d->%d with %d signatures\n", ack.EpochID, ack.NextEpochID, len(ack.Proofs))
	return nil
}

func recoveryLatestQuorumSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario recovery_latest_quorum_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "recovery-latest-quorum-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 19000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 60*time.Second, "timeout for observing recovery latest quorum")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario recovery_latest_quorum_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", "1",
		"-anchors", "1",
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario recovery_latest_quorum_smoke: starting nodes")
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

	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	if err := stopState(state, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	if err := enableAnchorRecoveryMode(runDir, "anchor-1"); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	anchorManifestNode, err := findManifestNodeByRole(manifest, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	fmt.Println("scenario recovery_latest_quorum_smoke: restarting anchor in RECOVERY_MODE")
	recoveryAnchorNode, err := startNode(anchorManifestNode, state.LogsDir)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	state.Nodes = []NodeState{recoveryAnchorNode}

	signed, payload, err := waitForRecoveryLatestCoreQuorum(recoveryAnchorNode, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if !cryptography.VerifySignature(string(signed.Payload), signed.PubKey, signed.Signature) {
		printScenarioDiagnostics(state, 160)
		return errors.New("recovery latest core quorum response has invalid anchor signature")
	}
	if payload.Proof == nil {
		printScenarioDiagnostics(state, 160)
		return errors.New("recovery latest core quorum payload has no proof")
	}
	if payload.Proof.NextEpochID < 1 || payload.Proof.NextEpochID != payload.Proof.EpochID+1 {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("unexpected recovery proof range: got %d->%d", payload.Proof.EpochID, payload.Proof.NextEpochID)
	}
	if payload.RecoveryViewEpoch != payload.Proof.NextEpochID {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("recovery view epoch mismatch: got %d, proof next epoch %d", payload.RecoveryViewEpoch, payload.Proof.NextEpochID)
	}
	if payload.RecoveryViewEpochDataHash == "" || payload.RecoveryViewEpochDataHash != payload.Proof.EpochDataHash {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("recovery view hash mismatch: got %q, proof hash %q", payload.RecoveryViewEpochDataHash, payload.Proof.EpochDataHash)
	}
	if payload.RecoveryViewSource != "local" && payload.RecoveryViewSource != "memory_catchup" {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("unexpected recovery view source %q", payload.RecoveryViewSource)
	}
	if len(payload.ValidatorEndpoints) == 0 {
		printScenarioDiagnostics(state, 160)
		return errors.New("recovery latest core quorum payload has no validator endpoints")
	}
	if err := assertNodeAlive(recoveryAnchorNode); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	fmt.Printf("PASS recovery_latest_quorum_smoke: anchor %s signed latest core quorum proof for %d->%d with %d validator endpoints\n", signed.PubKey, payload.Proof.EpochID, payload.Proof.NextEpochID, len(payload.ValidatorEndpoints))
	return nil
}

func multiNodeQuorumSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_quorum_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-quorum-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 23000, "base TCP port for generated configs")
	coreCount := fs.Int("core", 4, "number of core validators")
	anchorCount := fs.Int("anchors", 4, "number of anchors")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 150*time.Second, "timeout for observing quorum flow")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *coreCount < 4 {
		return errors.New("multi_node_quorum_smoke requires at least 4 core validators")
	}
	if *anchorCount < 4 {
		return errors.New("multi_node_quorum_smoke requires at least 4 anchors")
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_quorum_smoke: preparing %d core + %d anchors run %s\n", *coreCount, *anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(*coreCount),
		"-anchors", fmt.Sprint(*anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(*basePort),
		"-core-epoch-duration-ms", "12000",
		"-core-leadership-duration-ms", "2000",
		"-core-block-time-ms", "900",
		"-anchor-epoch-duration-ms", "12000",
		"-anchor-block-time-ms", "900",
		"-overwrite",
	}); err != nil {
		return err
	}

	fmt.Println("scenario multi_node_quorum_smoke: starting nodes")
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
	if len(coreNodes) != *coreCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d core nodes, got %d", *coreCount, len(coreNodes))
	}
	if len(anchorNodes) != *anchorCount {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("expected %d anchor nodes, got %d", *anchorCount, len(anchorNodes))
	}

	coreNode := coreNodes[0]
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated epoch rotation proof sent for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	for _, anchorNode := range anchorNodes {
		if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), 0, *observeTimeout); err != nil {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s did not apply core quorum transition 0->1: %w", anchorNode.Name, err)
		}
	}

	anchorMajority := quorumMajority(len(anchorNodes))
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
	for _, node := range append(coreNodes, anchorNodes...) {
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
		if len(payload.ValidatorEndpoints) < anchorMajority {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned %d validator endpoints, want at least %d", recoveryAnchor.Name, len(payload.ValidatorEndpoints), anchorMajority)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("collected recovery responses from %d unique anchors, want majority %d", len(recoverySigners), anchorMajority)
	}

	fmt.Printf("PASS multi_node_quorum_smoke: %d core + %d anchors reached 0->1, ACK had %d/%d signatures, recovery majority=%d\n", len(coreNodes), len(anchorNodes), len(ack.Proofs), len(anchorNodes), len(recoverySigners))
	return nil
}

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

func multiNodeOneCoreDownSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_one_core_down_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-one-core-down-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 31000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 210*time.Second, "timeout for observing degraded core quorum flow")
	if err := fs.Parse(args); err != nil {
		return err
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_one_core_down_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	fmt.Println("scenario multi_node_one_core_down_smoke: starting nodes")
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

	downCore := coreNodes[len(coreNodes)-1]
	activeCoreNodes := coreNodes[:len(coreNodes)-1]
	fmt.Printf("scenario multi_node_one_core_down_smoke: stopping %s before epoch rotation\n", downCore.Name)
	if err := stopState(RunState{Nodes: []NodeState{downCore}}, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	if err := waitForHealthURLUnavailable(downCore.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("%s still accepts health requests after stop: %w", downCore.Name, err)
	}

	coreNode := activeCoreNodes[0]
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Aggregated epoch rotation proof sent for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	for _, anchorNode := range anchorNodes {
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
	for _, node := range append(activeCoreNodes, anchorNodes...) {
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

	coreMajority := quorumMajority(coreCount)
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
		if len(payload.Proof.Proofs) < coreMajority {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned core rotation proof with %d signatures, want majority %d", recoveryAnchor.Name, len(payload.Proof.Proofs), coreMajority)
		}
		if len(payload.Proof.Proofs) >= coreCount {
			printScenarioDiagnostics(state, 160)
			return fmt.Errorf("%s returned core rotation proof with all %d signatures while %s was down", recoveryAnchor.Name, len(payload.Proof.Proofs), downCore.Name)
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

	fmt.Printf("PASS multi_node_one_core_down_smoke: %s stayed down, core rotation proof had %d/%d signatures, ACK had %d/%d signatures, recovery majority=%d\n", downCore.Name, coreMajority, coreCount, len(ack.Proofs), anchorCount, len(recoverySigners))
	return nil
}

func waitForCoreHeight(node NodeState, minExclusive int64, timeout time.Duration) (int64, error) {
	if node.HealthURL == "" {
		return -1, fmt.Errorf("%s has no health URL", node.Name)
	}
	lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return -1, err
		}
		height, err := fetchLastHeight(client, lastHeightURL)
		if err == nil && height > minExclusive {
			return height, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	return -1, fmt.Errorf("core height did not advance beyond %d within %s: %v", minExclusive, timeout, lastErr)
}

func fetchLastHeight(client http.Client, lastHeightURL string) (int64, error) {
	var payload struct {
		LastHeight int64 `json:"lastHeight"`
	}
	if err := fetchJSON(client, lastHeightURL, &payload); err != nil {
		return -1, err
	}
	return payload.LastHeight, nil
}

func waitForCoreEpochAtLeast(node NodeState, minEpoch int, timeout time.Duration) error {
	if node.HealthURL == "" {
		return fmt.Errorf("%s has no health URL", node.Name)
	}
	liveStatsURL := node.HealthURL
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return err
		}
		epochID, err := fetchLiveStatsEpoch(client, liveStatsURL)
		if err == nil && epochID >= minEpoch {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("core epoch did not reach %d within %s: %v", minEpoch, timeout, lastErr)
}

func fetchLiveStatsEpoch(client http.Client, liveStatsURL string) (int, error) {
	var payload map[string]any
	if err := fetchJSON(client, liveStatsURL, &payload); err != nil {
		return -1, err
	}
	epoch, ok := payload["epoch"].(map[string]any)
	if !ok {
		return -1, errors.New("live_stats response has no epoch object")
	}
	for _, key := range []string{"id", "Id"} {
		if raw, ok := epoch[key].(float64); ok {
			return int(raw), nil
		}
	}
	return -1, errors.New("live_stats epoch has no id")
}

type anchorEpochAckProofResponse struct {
	EpochID       int               `json:"epochId"`
	NextEpochID   int               `json:"nextEpochId"`
	EpochDataHash string            `json:"epochDataHash"`
	Proofs        map[string]string `json:"proofs"`
}

func waitForCoreAnchorEpochAckProof(node NodeState, lookupEpochID int, timeout time.Duration) (anchorEpochAckProofResponse, error) {
	if node.HealthURL == "" {
		return anchorEpochAckProofResponse{}, fmt.Errorf("%s has no health URL", node.Name)
	}
	endpoint := strings.TrimSuffix(node.HealthURL, "/live_stats") + fmt.Sprintf("/aggregated_anchor_epoch_ack_proof/%d", lookupEpochID)
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return anchorEpochAckProofResponse{}, err
		}
		var payload anchorEpochAckProofResponse
		if err := fetchJSONStatusOK(client, endpoint, &payload); err == nil {
			return payload, nil
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}

	return anchorEpochAckProofResponse{}, fmt.Errorf("core did not expose anchor epoch ACK proof for lookup epoch %d within %s: %v", lookupEpochID, timeout, lastErr)
}

func fetchJSONStatusOK(client http.Client, url string, target any) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

type recoverySignedResponse struct {
	PubKey    string          `json:"pubKey"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

type recoveryCoreQuorumPayload struct {
	Proof                     *recoveryAggregatedEpochRotationProof `json:"proof"`
	ValidatorEndpoints        map[string]recoveryValidatorEndpoints `json:"validatorEndpoints"`
	RecoveryViewEpoch         int                                   `json:"recoveryViewEpoch"`
	RecoveryViewEpochDataHash string                                `json:"recoveryViewEpochDataHash"`
	RecoveryViewSource        string                                `json:"recoveryViewSource"`
	RecoveryViewFromEpoch     int                                   `json:"recoveryViewFromEpoch"`
	RecoveryViewVerifiedAtMs  int64                                 `json:"recoveryViewVerifiedAtMs"`
}

type recoveryValidatorEndpoints struct {
	ValidatorURL    string `json:"validatorUrl"`
	WssValidatorURL string `json:"wssValidatorUrl"`
}

type recoveryAggregatedEpochRotationProof struct {
	EpochID       int               `json:"epochId"`
	NextEpochID   int               `json:"nextEpochId"`
	EpochDataHash string            `json:"epochDataHash"`
	Proofs        map[string]string `json:"proofs"`
}

func waitForRecoveryLatestCoreQuorum(node NodeState, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	if node.HealthURL == "" {
		return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s has no health URL", node.Name)
	}
	endpoint := strings.TrimSuffix(node.HealthURL, "/core/quorum_state") + "/recovery/latest_core_quorum"
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		if err := assertNodeAlive(node); err != nil {
			return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, err
		}
		var signed recoverySignedResponse
		if err := fetchJSONStatusOK(client, endpoint, &signed); err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var payload recoveryCoreQuorumPayload
		if err := json.Unmarshal(signed.Payload, &payload); err != nil {
			return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, err
		}
		return signed, payload, nil
	}

	return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("anchor did not expose recovery latest core quorum within %s: %v", timeout, lastErr)
}

func waitForHealthURLUnavailable(healthURL string, timeout time.Duration) error {
	if healthURL == "" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get(healthURL)
		if err != nil {
			return nil
		}
		lastErr = fmt.Errorf("GET %s returned %d", healthURL, resp.StatusCode)
		_ = resp.Body.Close()
		time.Sleep(200 * time.Millisecond)
	}

	return lastErr
}

func waitForLogPatternFrom(path string, pattern *regexp.Regexp, from int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) >= from {
			if match := pattern.FindIndex(raw[from:]); match != nil {
				return from + match[1], nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, fmt.Errorf("log %s did not match %q within %s", path, pattern.String(), timeout)
}

func startAlfpBlockingProxy(targetRawURL string) (string, func(), error) {
	targetURL, err := url.Parse(targetRawURL)
	if err != nil {
		return "", nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/accept_aggregated_leader_finalization_proof" {
				if shouldBlockAlfpPost(r) {
					http.Error(w, "ALFP POST intentionally blocked by alfp_pull_smoke", http.StatusServiceUnavailable)
					return
				}
			}
			proxy.ServeHTTP(w, r)
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()

	closeFn := func() {
		_ = server.Close()
	}
	return "http://" + listener.Addr().String(), closeFn, nil
}

func shouldBlockAlfpPost(r *http.Request) bool {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return true
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	var payload struct {
		LeaderFinalizations []struct {
			EpochIndex int `json:"epochIndex"`
		} `json:"leaderFinalizations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return true
	}
	for _, proof := range payload.LeaderFinalizations {
		if proof.EpochIndex > 0 {
			return true
		}
	}
	return false
}

func rewriteCoreAnchorsHTTPURL(runDir string, proxyURL string) error {
	path := filepath.Join(runDir, "network", "core-1", "anchors.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var anchors []map[string]any
	if err := json.Unmarshal(raw, &anchors); err != nil {
		return err
	}
	if len(anchors) == 0 {
		return errors.New("core anchors.json has no anchors")
	}
	anchors[0]["anchorURL"] = proxyURL
	return writeJSON(path, anchors)
}

func enableAnchorRecoveryMode(runDir string, anchorName string) error {
	path := filepath.Join(runDir, "network", anchorName, "configs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config["RECOVERY_MODE"] = true
	return writeJSON(path, config)
}

func findManifestNodeByRole(manifest Manifest, role string) (ManifestNode, error) {
	for _, node := range manifest.Nodes {
		if node.Role == role {
			return node, nil
		}
	}
	return ManifestNode{}, fmt.Errorf("manifest node with role %q not found", role)
}

func findManifestNodesByRole(manifest Manifest, role string) []ManifestNode {
	nodes := make([]ManifestNode, 0)
	for _, node := range manifest.Nodes {
		if node.Role == role {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func findNodeByRole(state RunState, role string) (NodeState, error) {
	for _, node := range state.Nodes {
		if node.Role == role {
			return node, nil
		}
	}
	return NodeState{}, fmt.Errorf("node with role %q not found", role)
}

func findNodesByRole(state RunState, role string) []NodeState {
	nodes := make([]NodeState, 0)
	for _, node := range state.Nodes {
		if node.Role == role {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func quorumMajority(quorumSize int) int {
	majority := (2 * quorumSize / 3) + 1
	if majority > quorumSize {
		return quorumSize
	}
	return majority
}

func assertNodeAlive(node NodeState) error {
	if !processAlive(node.PID) {
		return fmt.Errorf("%s is not running (pid=%d)", node.Name, node.PID)
	}
	return nil
}

func printScenarioDiagnostics(state RunState, lines int) {
	fmt.Fprintln(os.Stderr, "scenario diagnostics:")
	for _, node := range state.Nodes {
		fmt.Fprintf(os.Stderr, "--- %s stdout ---\n", node.Name)
		_ = printLastLinesTo(os.Stderr, node.StdoutLog, lines)
		fmt.Fprintf(os.Stderr, "--- %s stderr ---\n", node.Name)
		_ = printLastLinesTo(os.Stderr, node.StderrLog, lines)
	}
}
