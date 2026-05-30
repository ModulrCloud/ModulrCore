package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"lukechampine.com/blake3"
)

func scenarioCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("missing scenario name")
	}

	switch args[0] {
	case "bootstrap_smoke":
		return bootstrapSmokeScenario(args[1:])
	case "debug_api_smoke":
		return debugAPISmokeScenario(args[1:])
	case "alfp_pull_smoke":
		return alfpPullSmokeScenario(args[1:])
	case "early_epoch_announcement_alfp_smoke":
		return earlyEpochAnnouncementAlfpSmokeScenario(args[1:])
	case "epoch_anchor_ack_smoke":
		return epochAnchorAckSmokeScenario(args[1:])
	case "recovery_latest_quorum_smoke":
		return recoveryLatestQuorumSmokeScenario(args[1:])
	case "multi_node_quorum_smoke":
		return multiNodeQuorumSmokeScenario(args[1:])
	case "multi_node_one_anchor_down_smoke":
		return multiNodeOneAnchorDownSmokeScenario(args[1:])
	case "anchor_rotation_aarp_smoke":
		return anchorRotationAarpSmokeScenario(args[1:])
	case "anchor_rotation_aarp_no_initial_block_smoke":
		return anchorRotationAarpNoInitialBlockSmokeScenario(args[1:])
	case "multi_node_one_core_down_smoke":
		return multiNodeOneCoreDownSmokeScenario(args[1:])
	case "multi_node_alfp_pull_after_push_failure":
		return multiNodeAlfpPullAfterPushFailureScenario(args[1:])
	case "multi_node_recovery_majority_latest_quorum":
		return multiNodeRecoveryMajorityLatestQuorumScenario(args[1:])
	case "multi_node_lagging_anchor_catchup":
		return multiNodeLaggingAnchorCatchupScenario(args[1:])
	case "multi_node_network_partition_no_false_majority":
		return multiNodeNetworkPartitionNoFalseMajorityScenario(args[1:])
	case "recovery_script_style":
		return recoveryScriptStyleScenario(args[1:])
	case "recovery_full_cycle_smoke":
		return recoveryFullCycleSmokeScenario(args[1:])
	case "long_running_stability":
		return longRunningStabilityScenario(args[1:])
	case "long_running_stability_with_temporary_validator_down":
		return longRunningStabilityWithTemporaryValidatorDownScenario(args[1:])
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

func debugAPISmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario debug_api_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "debug-api-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
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
	fmt.Printf("scenario debug_api_smoke: preparing run %s\n", *runID)
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

	fmt.Println("scenario debug_api_smoke: starting nodes")
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

	initialHeight, err := waitForCoreHeight(coreNode, -1, 5*time.Second)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	nextHeight, err := waitForCoreHeight(coreNode, initialHeight, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	baseURL := strings.TrimSuffix(coreNode.HealthURL, "/live_stats")
	client := http.Client{Timeout: 2 * time.Second}

	var pipeline map[string]any
	if err := fetchJSONStatusOK(client, baseURL+"/debug/pipeline_state", &pipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug pipeline_state failed: %w", err)
	}
	if err := assertDebugPipelineShape(pipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	executionNext, err := extractDebugExecutionNext(pipeline)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	var heightProbe map[string]any
	if err := fetchJSONStatusOK(client, fmt.Sprintf("%s/debug/height_probe/%d", baseURL, executionNext), &heightProbe); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe failed: %w", err)
	}
	if err := assertNestedMap(heightProbe, "core"); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe malformed: %w", err)
	}
	if err := assertNumberKey(heightProbe, "ahpNext"); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug height_probe malformed: %w", err)
	}

	epochID, leaderIndex, err := extractDebugEpochAndLeader(pipeline)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}
	var leaderPipeline map[string]any
	if err := fetchJSONStatusOK(client, fmt.Sprintf("%s/debug/leader_pipeline/%d/%d", baseURL, epochID, leaderIndex), &leaderPipeline); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug leader_pipeline failed: %w", err)
	}
	for _, key := range []string{"nodePublicKey", "epochId", "leaderIndex", "leader", "localBlocks", "alfp", "lastMileTracker", "lastMileRelation", "ahpTracker", "ahpRelation"} {
		if _, ok := leaderPipeline[key]; !ok {
			printScenarioDiagnostics(state, 120)
			return fmt.Errorf("debug leader_pipeline missing %q", key)
		}
	}

	var outbox map[string]any
	if err := fetchJSONStatusOK(client, baseURL+"/debug/pod_outbox_state", &outbox); err != nil {
		printScenarioDiagnostics(state, 120)
		return fmt.Errorf("debug pod_outbox_state failed: %w", err)
	}
	for _, key := range []string{"pendingCount", "countsByType", "sampleIds"} {
		if _, ok := outbox[key]; !ok {
			printScenarioDiagnostics(state, 120)
			return fmt.Errorf("debug pod_outbox_state missing %q", key)
		}
	}

	fmt.Printf("PASS debug_api_smoke: height advanced from %d to %d; debug API shapes are valid\n", initialHeight, nextHeight)
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
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL, false)
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

func earlyEpochAnnouncementAlfpSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario early_epoch_announcement_alfp_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "early-epoch-announcement-alfp-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 0, "base TCP port for generated configs; 0 auto-selects a free range")
	healthTimeout := fs.Duration("health-timeout", 30*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 75*time.Second, "timeout for observing early announcement and ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	coreCount, anchorCount := 1, 1
	selectedBasePort := *basePort
	if selectedBasePort == 0 {
		var err error
		selectedBasePort, err = findAvailableGeneratedBasePort(35000, 4000, coreCount, anchorCount)
		if err != nil {
			return err
		}
		fmt.Printf("scenario early_epoch_announcement_alfp_smoke: auto-selected base-port %d\n", selectedBasePort)
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario early_epoch_announcement_alfp_smoke: preparing run %s\n", *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
		"-run-root", *runRoot,
		"-run-id", *runID,
		"-core-repo", *coreRepo,
		"-anchors-repo", *anchorsRepo,
		"-base-port", fmt.Sprint(selectedBasePort),
		"-core-epoch-duration-ms", "8000",
		"-core-leadership-duration-ms", "1000",
		"-core-block-time-ms", "700",
		"-anchor-epoch-duration-ms", "8000",
		"-anchor-block-time-ms", "700",
		"-overwrite",
	}); err != nil {
		return err
	}

	anchorHTTPURL := fmt.Sprintf("http://localhost:%d", selectedBasePort+2000)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(anchorHTTPURL, false)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorsHTTPURL(runDir, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario early_epoch_announcement_alfp_smoke: blocking direct core ALFP POSTs via proxy %s -> %s\n", proxyURL, anchorHTTPURL)
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

	coreNode, err := findNodeByRole(state, "core")
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	anchorNode, err := findNodeByRole(state, "anchor")
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if _, err := waitForLogPatternFrom(coreNode.StdoutLog, regexp.MustCompile(`Epoch announcement proof collected for epoch 0->1`), 0, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}

	earlyStart, earlyEnd, err := waitForLogMatchRangeFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied early epoch announcement proof 0 -> 1`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if found, err := logPatternExistsBefore(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), earlyStart); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	} else if found {
		printScenarioDiagnostics(state, 200)
		return errors.New("anchor applied AERP 0->1 before early epoch announcement proof")
	}

	proactiveLogEnd, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool \(epoch=1`), earlyEnd, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}
	if _, err := waitForLogPatternFrom(anchorNode.StdoutLog, regexp.MustCompile(`Core quorum catch-up: applied epoch rotation proof 0 -> 1`), earlyEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 200)
		return err
	}

	if err := assertNodeAlive(coreNode); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if err := assertNodeAlive(anchorNode); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}

	fmt.Println("PASS early_epoch_announcement_alfp_smoke: anchor applied early core epoch announcement before AERP, proactively collected epoch 1 ALFP, and later applied AERP 0->1")
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

func anchorRotationAarpNoInitialBlockSmokeScenario(args []string) error {
	fs := flag.NewFlagSet("scenario anchor_rotation_aarp_no_initial_block_smoke", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "anchor-rotation-aarp-no-initial-block-smoke-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
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
		fmt.Printf("scenario anchor_rotation_aarp_no_initial_block_smoke: auto-selected base-port %d\n", selectedBasePort)
	}
	if err := ensureGeneratedPortsAvailable(selectedBasePort, coreCount, anchorCount); err != nil {
		return err
	}

	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario anchor_rotation_aarp_no_initial_block_smoke: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	downAnchor, err := manifestNodeByName(manifest, "anchor-1")
	if err != nil {
		return err
	}

	fmt.Println("scenario anchor_rotation_aarp_no_initial_block_smoke: starting nodes without anchor-1")
	state, err := startManifestSubset(manifestPath, *runRoot, *runID, map[string]bool{"anchor-1": true}, *healthTimeout)
	if err != nil {
		if loadedState, loadErr := loadState(runDir); loadErr == nil {
			printScenarioDiagnostics(loadedState, 160)
		}
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
	if len(anchorNodes) != anchorCount-1 {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("expected %d active anchor nodes, got %d", anchorCount-1, len(anchorNodes))
	}
	if err := waitForHealthURLUnavailable(downAnchor.HealthURL, 5*time.Second); err != nil {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("%s unexpectedly accepts health requests: %w", downAnchor.Name, err)
	}

	coreNode := coreNodes[0]
	activeAnchors := anchorNodes

	aarpCollectedPattern := regexp.MustCompile(`Anchor rotation: collected [0-9]+ signatures for .* in epoch 0`)
	aarpIncludedPattern := regexp.MustCompile(`New block generated 0:.*\| AARPs=[1-9][0-9]*`)
	coreAcceptedPattern := regexp.MustCompile(`Anchor rotation monitor: accepted AARP chain for epoch 0 anchorIndex=0 -> foundInAnchorIndex=1 lastBlockIndex=-1`)
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
			return fmt.Errorf("%s did not apply core quorum transition 0->1 after zero-block AARP rotation: %w", anchorNode.Name, err)
		}
	}

	baselineHeights := heightsByNode(coreNodes)
	if _, err := waitForCoreHeightGrowth(coreNodes, baselineHeights, 45*time.Second); err != nil {
		printScenarioDiagnostics(state, 220)
		return fmt.Errorf("core block execution did not continue after zero-block AARP rotation: %w", err)
	}

	for _, node := range append(coreNodes, activeAnchors...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 160)
			return err
		}
	}

	fmt.Printf("PASS anchor_rotation_aarp_no_initial_block_smoke: %s was never started, active anchors included zero-block AARP, core accepted lastBlockIndex=-1, core transition 0->1 completed, and block execution continued\n", downAnchor.Name)
	return nil
}

func manifestNodeByName(manifest Manifest, name string) (ManifestNode, error) {
	for _, node := range manifest.Nodes {
		if node.Name == name {
			return node, nil
		}
	}
	return ManifestNode{}, fmt.Errorf("manifest node %q not found", name)
}

func startManifestSubset(manifestPath, runRoot, runID string, exclude map[string]bool, healthTimeout time.Duration) (RunState, error) {
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return RunState{}, err
	}
	if len(manifest.Nodes) == 0 {
		return RunState{}, errors.New("manifest has no nodes")
	}

	runDir := filepath.Join(runRoot, runID)
	logsDir := filepath.Join(runDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return RunState{}, err
	}

	state := RunState{
		RunID:       runID,
		Manifest:    absOrOriginal(manifestPath),
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		RunDir:      absOrOriginal(runDir),
		LogsDir:     absOrOriginal(logsDir),
		HarnessNote: "manifest-driven process harness; some nodes intentionally excluded by scenario",
	}

	for _, node := range manifest.Nodes {
		if exclude[node.Name] {
			continue
		}
		nodeState, err := startNode(node, logsDir)
		if err != nil {
			_ = stopState(state, 2*time.Second)
			return RunState{}, fmt.Errorf("start %s: %w", node.Name, err)
		}
		state.Nodes = append(state.Nodes, nodeState)
	}

	if len(state.Nodes) == 0 {
		return RunState{}, errors.New("no nodes selected for start")
	}
	if err := writeState(runDir, state); err != nil {
		_ = stopState(state, 2*time.Second)
		return RunState{}, err
	}
	if err := writeLatestPointer(runRoot, runDir); err != nil {
		return RunState{}, err
	}
	if err := waitForHealthChecks(state.Nodes, healthTimeout); err != nil {
		_ = stopState(state, 2*time.Second)
		return RunState{}, err
	}

	fmt.Printf("started run %s\nstate: %s\nlogs: %s\n", state.RunID, filepath.Join(runDir, "state.json"), logsDir)
	return state, nil
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

func multiNodeAlfpPullAfterPushFailureScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_alfp_pull_after_push_failure", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-alfp-pull-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 35000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 180*time.Second, "timeout for observing proactive ALFP collection")
	if err := fs.Parse(args); err != nil {
		return err
	}

	const coreCount = 4
	const anchorCount = 4
	runDir := filepath.Join(*runRoot, *runID)
	manifestPath := filepath.Join(runDir, "manifest.json")
	fmt.Printf("scenario multi_node_alfp_pull_after_push_failure: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
	if err := prepareCmd([]string{
		"-core", fmt.Sprint(coreCount),
		"-anchors", fmt.Sprint(anchorCount),
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

	targetAnchorName := "anchor-4"
	targetAnchorHTTPURL := fmt.Sprintf("http://127.0.0.1:%d", *basePort+2000+3)
	proxyURL, closeProxy, err := startAlfpBlockingProxy(targetAnchorHTTPURL, true)
	if err != nil {
		return err
	}
	defer closeProxy()

	if err := rewriteCoreAnchorHTTPURLForAll(runDir, targetAnchorHTTPURL, proxyURL); err != nil {
		return err
	}

	fmt.Printf("scenario multi_node_alfp_pull_after_push_failure: blocking ALFP POSTs to %s via proxy %s -> %s\n", targetAnchorName, proxyURL, targetAnchorHTTPURL)
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
	targetAnchor, err := findNodeByName(state, targetAnchorName)
	if err != nil {
		printScenarioDiagnostics(state, 120)
		return err
	}

	coreNode := coreNodes[0]
	if err := waitForCoreEpochAtLeast(coreNode, 1, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	proactiveLogEnd, err := waitForLogPatternFrom(targetAnchor.StdoutLog, regexp.MustCompile(`ALFP collection: built ALFP locally and deposited to mempool`), 0, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}
	if _, err := waitForLogPatternFrom(targetAnchor.StdoutLog, regexp.MustCompile(`ALFPs=[1-9][0-9]*`), proactiveLogEnd, *observeTimeout); err != nil {
		printScenarioDiagnostics(state, 180)
		return err
	}

	ack, err := waitForCoreAnchorEpochAckProof(coreNode, 1, *observeTimeout)
	if err != nil {
		printScenarioDiagnostics(state, 160)
		return err
	}
	if len(ack.Proofs) < quorumMajority(anchorCount) {
		printScenarioDiagnostics(state, 160)
		return fmt.Errorf("anchor epoch ACK proof has %d signatures, want majority %d", len(ack.Proofs), quorumMajority(anchorCount))
	}
	for _, node := range append(coreNodes, anchorNodes...) {
		if err := assertNodeAlive(node); err != nil {
			printScenarioDiagnostics(state, 120)
			return err
		}
	}

	fmt.Printf("PASS multi_node_alfp_pull_after_push_failure: %s built ALFP from core quorum after blocked core push; ACK had %d/%d signatures\n", targetAnchorName, len(ack.Proofs), anchorCount)
	return nil
}

func multiNodeRecoveryMajorityLatestQuorumScenario(args []string) error {
	fs := flag.NewFlagSet("scenario multi_node_recovery_majority_latest_quorum", flag.ExitOnError)
	runRoot := fs.String("run-root", filepath.Join("tests_e2e", "runs", "scenarios"), "directory for scenario run state")
	runID := fs.String("run-id", "multi-node-recovery-majority-"+time.Now().UTC().Format("20060102T150405Z"), "run identifier")
	coreRepo := fs.String("core-repo", ".", "path to modulr-core repository")
	anchorsRepo := fs.String("anchors-repo", "../modulr-anchors-core", "path to modulr-anchors-core repository")
	basePort := fs.Int("base-port", 39000, "base TCP port for generated configs")
	healthTimeout := fs.Duration("health-timeout", 90*time.Second, "timeout for startup health checks")
	observeTimeout := fs.Duration("observe-timeout", 210*time.Second, "timeout for observing recovery majority convergence")
	targetEpoch := fs.Int("target-epoch", 2, "core epoch to reach before testing recovery majority")
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
	fmt.Printf("scenario multi_node_recovery_majority_latest_quorum: preparing %d core + %d anchors run %s\n", coreCount, anchorCount, *runID)
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

	fmt.Println("scenario multi_node_recovery_majority_latest_quorum: starting nodes")
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
	var expectedEpoch int
	var expectedHash string
	var expectedRange string
	for _, recoveryAnchor := range recoveryAnchors {
		signed, payload, err := waitForRecoveryLatestCoreQuorum(recoveryAnchor, *observeTimeout)
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
		rangeLabel := fmt.Sprintf("%d->%d", payload.Proof.EpochID, payload.Proof.NextEpochID)
		if expectedHash == "" {
			expectedEpoch = payload.Proof.NextEpochID
			expectedHash = payload.Proof.EpochDataHash
			expectedRange = rangeLabel
		} else if payload.Proof.NextEpochID != expectedEpoch || payload.Proof.EpochDataHash != expectedHash {
			printScenarioDiagnostics(state, 180)
			return fmt.Errorf("%s disagreed on latest quorum: got %s hash %s, want %s hash %s", recoveryAnchor.Name, rangeLabel, payload.Proof.EpochDataHash, expectedRange, expectedHash)
		}
		recoverySigners[signed.PubKey] = struct{}{}
	}
	if len(recoverySigners) < anchorMajority {
		printScenarioDiagnostics(state, 180)
		return fmt.Errorf("collected recovery responses from %d unique anchors, want majority %d", len(recoverySigners), anchorMajority)
	}

	fmt.Printf("PASS multi_node_recovery_majority_latest_quorum: recovery majority=%d converged on latest core quorum %s hash %s\n", len(recoverySigners), expectedRange, expectedHash)
	return nil
}

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

func findAvailableGeneratedBasePort(startBasePort, stride int, coreCount, anchorCount int) (int, error) {
	if stride < 3004 {
		return 0, errors.New("port stride must be at least 3004")
	}
	for basePort := startBasePort; basePort <= 65535; basePort += stride {
		if err := ensureGeneratedPortsAvailable(basePort, coreCount, anchorCount); err == nil {
			return basePort, nil
		}
	}
	return 0, fmt.Errorf("could not find a free generated port range from base %d with stride %d", startBasePort, stride)
}

func ensureGeneratedPortsAvailable(basePort, coreCount, anchorCount int) error {
	ports := generatedNodePorts(basePort, coreCount, anchorCount)
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("generated port %d from base-port %d is outside valid TCP range", port, basePort)
		}
		if err := checkPortAvailable(port); err != nil {
			return fmt.Errorf("generated port %d from base-port %d is unavailable: %w", port, basePort, err)
		}
	}
	return nil
}

func generatedNodePorts(basePort, coreCount, anchorCount int) []int {
	ports := make([]int, 0, coreCount*2+anchorCount*2)
	for idx := 0; idx < coreCount; idx++ {
		ports = append(ports, basePort+idx, basePort+1000+idx)
	}
	for idx := 0; idx < anchorCount; idx++ {
		ports = append(ports, basePort+2000+idx, basePort+3000+idx)
	}
	return ports
}

func checkPortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	_ = listener.Close()

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

func waitForAnyCoreHeight(nodes []NodeState, minExclusive int64, timeout time.Duration) (NodeState, int64, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.HealthURL == "" {
				continue
			}
			if err := assertNodeAlive(node); err != nil {
				lastErr = err
				continue
			}
			lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
			height, err := fetchLastHeight(client, lastHeightURL)
			if err == nil && height > minExclusive {
				return node, height, nil
			}
			if err != nil {
				lastErr = err
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return NodeState{}, -1, fmt.Errorf("no core height advanced beyond %d within %s: %v", minExclusive, timeout, lastErr)
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

func fetchNodeLastHeight(node NodeState) (int64, error) {
	if node.HealthURL == "" {
		return -1, fmt.Errorf("%s has no health URL", node.Name)
	}
	client := http.Client{Timeout: 750 * time.Millisecond}
	lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
	return fetchLastHeight(client, lastHeightURL)
}

func waitForCoreHeightGrowth(nodes []NodeState, previous map[string]int64, timeout time.Duration) (coreHeightSnapshot, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		snapshot := coreHeightSnapshot{
			ByNode: make(map[string]int64, len(nodes)),
			Min:    math.MaxInt64,
			Max:    math.MinInt64,
		}
		allAdvanced := true
		for _, node := range nodes {
			baseline, ok := previous[node.Name]
			if !ok {
				return coreHeightSnapshot{}, fmt.Errorf("missing previous height for %s", node.Name)
			}
			if err := assertNodeAlive(node); err != nil {
				return coreHeightSnapshot{}, err
			}
			lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
			height, err := fetchLastHeight(client, lastHeightURL)
			if err != nil {
				lastErr = err
				allAdvanced = false
				break
			}
			if height <= baseline {
				lastErr = fmt.Errorf("%s executed height %d did not advance beyond %d", node.Name, height, baseline)
				allAdvanced = false
			}
			snapshot.ByNode[node.Name] = height
			if height < snapshot.Min {
				snapshot.Min = height
			}
			if height > snapshot.Max {
				snapshot.Max = height
			}
		}
		if allAdvanced && len(snapshot.ByNode) == len(nodes) {
			return snapshot, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return coreHeightSnapshot{}, fmt.Errorf("core executed heights did not advance on every node within %s: %v", timeout, lastErr)
}

func heightsByNode(nodes []NodeState) map[string]int64 {
	heights := make(map[string]int64, len(nodes))
	for _, node := range nodes {
		height, err := fetchNodeLastHeight(node)
		if err != nil {
			heights[node.Name] = -1
			continue
		}
		heights[node.Name] = height
	}
	return heights
}

type coreHeightSnapshot struct {
	ByNode map[string]int64
	Min    int64
	Max    int64
}

func waitForCoreHeightSnapshot(nodes []NodeState, minHeight int64, timeout time.Duration) (coreHeightSnapshot, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		snapshot := coreHeightSnapshot{
			ByNode: make(map[string]int64, len(nodes)),
			Min:    math.MaxInt64,
			Max:    -1,
		}
		allReady := true
		for _, node := range nodes {
			if err := assertNodeAlive(node); err != nil {
				return coreHeightSnapshot{}, err
			}
			lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
			height, err := fetchLastHeight(client, lastHeightURL)
			if err != nil {
				lastErr = err
				allReady = false
				break
			}
			if height < minHeight {
				lastErr = fmt.Errorf("%s height %d is below %d", node.Name, height, minHeight)
				allReady = false
				break
			}
			snapshot.ByNode[node.Name] = height
			if height < snapshot.Min {
				snapshot.Min = height
			}
			if height > snapshot.Max {
				snapshot.Max = height
			}
		}
		if allReady && len(snapshot.ByNode) == len(nodes) {
			return snapshot, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return coreHeightSnapshot{}, fmt.Errorf("core heights were not readable within %s: %v", timeout, lastErr)
}

func waitForAllCoreHeightsEqual(nodes []NodeState, minHeight int64, timeout time.Duration) (int64, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		var expected *int64
		allEqual := true
		for _, node := range nodes {
			if err := assertNodeAlive(node); err != nil {
				return -1, err
			}
			lastHeightURL := strings.TrimSuffix(node.HealthURL, "/live_stats") + "/last_height"
			height, err := fetchLastHeight(client, lastHeightURL)
			if err != nil {
				lastErr = err
				allEqual = false
				break
			}
			if height < minHeight {
				lastErr = fmt.Errorf("%s height %d is below %d", node.Name, height, minHeight)
				allEqual = false
				break
			}
			if expected == nil {
				heightCopy := height
				expected = &heightCopy
				continue
			}
			if height != *expected {
				lastErr = fmt.Errorf("core heights differ: expected %d got %d from %s", *expected, height, node.Name)
				allEqual = false
				break
			}
		}
		if allEqual && expected != nil {
			return *expected, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return -1, fmt.Errorf("core heights did not converge within %s: %v", timeout, lastErr)
}

type epochRotationProofResponse struct {
	EpochID     int               `json:"epochId"`
	NextEpochID int               `json:"nextEpochId"`
	Proofs      map[string]string `json:"proofs"`
}

func waitForAnyCoreEpochRotationProofSigner(nodes []NodeState, epochID int, signerPubKey string, timeout time.Duration) (epochRotationProofResponse, bool, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.HealthURL == "" {
				continue
			}
			if err := assertNodeAlive(node); err != nil {
				lastErr = err
				continue
			}
			endpoint := strings.TrimSuffix(node.HealthURL, "/live_stats") + fmt.Sprintf("/aggregated_epoch_rotation_proof/%d", epochID)
			var payload epochRotationProofResponse
			if err := fetchJSONStatusOK(client, endpoint, &payload); err != nil {
				lastErr = err
				continue
			}
			_, signed := payload.Proofs[signerPubKey]
			return payload, signed, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return epochRotationProofResponse{}, false, fmt.Errorf("no core exposed epoch rotation proof %d signed by %s within %s: %v", epochID, signerPubKey, timeout, lastErr)
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

func waitForAnyCoreAnchorEpochAckProof(nodes []NodeState, lookupEpochID int, timeout time.Duration) (anchorEpochAckProofResponse, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 750 * time.Millisecond}
	var lastErr error

	for time.Now().Before(deadline) {
		for _, node := range nodes {
			if node.HealthURL == "" {
				continue
			}
			if err := assertNodeAlive(node); err != nil {
				lastErr = err
				continue
			}
			endpoint := strings.TrimSuffix(node.HealthURL, "/live_stats") + fmt.Sprintf("/aggregated_anchor_epoch_ack_proof/%d", lookupEpochID)
			var payload anchorEpochAckProofResponse
			if err := fetchJSONStatusOK(client, endpoint, &payload); err == nil {
				return payload, nil
			} else {
				lastErr = err
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return anchorEpochAckProofResponse{}, fmt.Errorf("no core exposed anchor epoch ACK proof for lookup epoch %d within %s: %v", lookupEpochID, timeout, lastErr)
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

func assertDebugPipelineShape(payload map[string]any) error {
	for _, key := range []string{"node", "approvement", "generation", "finalizer", "alfp", "lastMile", "execution", "podOutbox"} {
		if err := assertNestedMap(payload, key); err != nil {
			return fmt.Errorf("debug pipeline_state malformed: %w", err)
		}
	}
	if err := assertNestedMap(payload["execution"].(map[string]any), "nextHeightProbe"); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	if err := assertNestedMap(payload["lastMile"].(map[string]any), "tracker"); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	lastMile := payload["lastMile"].(map[string]any)
	if err := assertNestedMap(lastMile, "ahpCollectorTracker"); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	if err := assertNumberKey(lastMile, "ahpCollectorLag"); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	if err := assertAHPCollectorNotAhead(lastMile); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	if err := assertNestedMap(payload["podOutbox"].(map[string]any), "countsByType"); err != nil {
		return fmt.Errorf("debug pipeline_state malformed: %w", err)
	}
	return nil
}

func assertNestedMap(payload map[string]any, key string) error {
	value, ok := payload[key]
	if !ok {
		return fmt.Errorf("missing %q", key)
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("%q is not an object", key)
	}
	return nil
}

func assertNumberKey(payload map[string]any, key string) error {
	if _, ok := payload[key].(float64); !ok {
		return fmt.Errorf("%q is missing or not numeric", key)
	}
	return nil
}

func assertAHPCollectorNotAhead(lastMile map[string]any) error {
	tracker, ok := lastMile["tracker"].(map[string]any)
	if !ok {
		return fmt.Errorf("lastMile.tracker is not an object")
	}
	ahpTracker, ok := lastMile["ahpCollectorTracker"].(map[string]any)
	if !ok {
		return fmt.Errorf("lastMile.ahpCollectorTracker is not an object")
	}
	sequencerNext, ok := tracker["nextHeight"].(float64)
	if !ok {
		return fmt.Errorf("lastMile.tracker.nextHeight is missing or not numeric")
	}
	ahpNext, ok := ahpTracker["nextHeight"].(float64)
	if !ok {
		return fmt.Errorf("lastMile.ahpCollectorTracker.nextHeight is missing or not numeric")
	}
	if ahpNext > sequencerNext {
		return fmt.Errorf("lastMile.ahpCollectorTracker.nextHeight %.0f is ahead of tracker.nextHeight %.0f", ahpNext, sequencerNext)
	}
	return nil
}

func extractDebugExecutionNext(payload map[string]any) (int64, error) {
	execution, ok := payload["execution"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("debug pipeline_state execution is not an object")
	}
	raw, ok := execution["nextHeight"].(float64)
	if !ok {
		return 0, fmt.Errorf("debug pipeline_state execution.nextHeight is missing or not numeric")
	}
	return int64(raw), nil
}

func extractDebugEpochAndLeader(payload map[string]any) (int, int, error) {
	approvement, ok := payload["approvement"].(map[string]any)
	if !ok {
		return 0, 0, fmt.Errorf("debug pipeline_state approvement is not an object")
	}
	epochRaw, ok := approvement["epochId"].(float64)
	if !ok {
		return 0, 0, fmt.Errorf("debug pipeline_state approvement.epochId is missing or not numeric")
	}
	leaderRaw, ok := approvement["wallClockLeaderIndex"].(float64)
	if !ok {
		return 0, 0, fmt.Errorf("debug pipeline_state approvement.wallClockLeaderIndex is missing or not numeric")
	}
	leaders, ok := approvement["leadersSequence"].([]any)
	if !ok || len(leaders) == 0 {
		return 0, 0, fmt.Errorf("debug pipeline_state approvement.leadersSequence is missing or empty")
	}
	leaderIndex := int(leaderRaw)
	if leaderIndex >= len(leaders) {
		leaderIndex = len(leaders) - 1
	}
	if leaderIndex < 0 {
		leaderIndex = 0
	}
	return int(epochRaw), leaderIndex, nil
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
	return waitForRecoveryCoreQuorumEndpoint(node, endpoint, timeout)
}

func waitForRecoveryLatestCoreQuorumAtLeast(node NodeState, minEpochID int, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		signed, payload, err := waitForRecoveryLatestCoreQuorum(node, 6*time.Second)
		if err == nil && payload.Proof != nil && payload.Proof.NextEpochID >= minEpochID {
			return signed, payload, nil
		}
		if err != nil {
			lastErr = err
		} else if payload.Proof == nil {
			lastErr = errors.New("latest recovery payload had no proof")
		} else {
			lastErr = fmt.Errorf("latest recovery epoch %d is below %d", payload.Proof.NextEpochID, minEpochID)
		}
		time.Sleep(500 * time.Millisecond)
	}

	return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s did not expose recovery latest core quorum >= %d within %s: %v", node.Name, minEpochID, timeout, lastErr)
}

func waitForRecoveryCoreQuorum(node NodeState, epochID int, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	if node.HealthURL == "" {
		return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("%s has no health URL", node.Name)
	}
	endpoint := strings.TrimSuffix(node.HealthURL, "/core/quorum_state") + fmt.Sprintf("/recovery/core_quorum/%d", epochID)
	return waitForRecoveryCoreQuorumEndpoint(node, endpoint, timeout)
}

func waitForRecoveryCoreQuorumEndpoint(node NodeState, endpoint string, timeout time.Duration) (recoverySignedResponse, recoveryCoreQuorumPayload, error) {
	deadline := time.Now().Add(timeout)
	client := http.Client{Timeout: 5 * time.Second}
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

	return recoverySignedResponse{}, recoveryCoreQuorumPayload{}, fmt.Errorf("anchor did not expose recovery core quorum at %s within %s: %v", endpoint, timeout, lastErr)
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

func waitForAnyLogPattern(nodes []NodeState, pattern *regexp.Regexp, timeout time.Duration) (NodeState, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, node := range nodes {
			raw, err := os.ReadFile(node.StdoutLog)
			if err == nil && pattern.FindIndex(raw) != nil {
				return node, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return NodeState{}, fmt.Errorf("no node log matched %q within %s", pattern.String(), timeout)
}

func waitForLogMatchRangeFrom(path string, pattern *regexp.Regexp, from int, timeout time.Duration) (int, int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) >= from {
			if match := pattern.FindIndex(raw[from:]); match != nil {
				return from + match[0], from + match[1], nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return 0, 0, fmt.Errorf("log %s did not match %q within %s", path, pattern.String(), timeout)
}

func logPatternExistsBefore(path string, pattern *regexp.Regexp, before int) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if before < 0 {
		before = 0
	}
	if before > len(raw) {
		before = len(raw)
	}
	return pattern.FindIndex(raw[:before]) != nil, nil
}

func startAlfpBlockingProxy(targetRawURL string, blockGenesisEpoch bool) (string, func(), error) {
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
				if shouldBlockAlfpPost(r, blockGenesisEpoch) {
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

func shouldBlockAlfpPost(r *http.Request, blockGenesisEpoch bool) bool {
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
		if blockGenesisEpoch && proof.EpochIndex >= 0 {
			return true
		}
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

func rewriteCoreAnchorHTTPURLForAll(runDir string, fromURL string, toURL string) error {
	for coreIndex := 1; ; coreIndex++ {
		path := filepath.Join(runDir, "network", fmt.Sprintf("core-%d", coreIndex), "anchors.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && coreIndex > 1 {
				return nil
			}
			return err
		}
		var anchors []map[string]any
		if err := json.Unmarshal(raw, &anchors); err != nil {
			return err
		}
		updated := false
		for _, anchor := range anchors {
			if anchor["anchorURL"] == fromURL {
				anchor["anchorURL"] = toURL
				updated = true
			}
		}
		if !updated {
			return fmt.Errorf("no anchorURL %q found in %s", fromURL, path)
		}
		if err := writeJSON(path, anchors); err != nil {
			return err
		}
	}
}

func enableAnchorRecoveryMode(runDir string, anchorName string) error {
	return setAnchorRecoveryMode(runDir, anchorName, true)
}

func setAnchorRecoveryMode(runDir string, anchorName string, enabled bool) error {
	path := filepath.Join(runDir, "network", anchorName, "configs.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	config["RECOVERY_MODE"] = enabled
	return writeJSON(path, config)
}

func buildRecoveryGenesisFromCoreNode(coreNode NodeState, runID string) (structures.Genesis, cryptography.Ed25519Box, error) {
	raw, err := os.ReadFile(filepath.Join(coreNode.ChaindataPath, "genesis.json"))
	if err != nil {
		return structures.Genesis{}, cryptography.Ed25519Box{}, err
	}
	var genesis structures.Genesis
	if err := json.Unmarshal(raw, &genesis); err != nil {
		return structures.Genesis{}, cryptography.Ed25519Box{}, err
	}
	if len(genesis.Validators) == 0 {
		return structures.Genesis{}, cryptography.Ed25519Box{}, errors.New("recovery genesis has no validators")
	}
	genesis.NetworkId = randomHex(32)
	genesis.FirstEpochStartTimestamp = uint64(time.Now().Add(2 * time.Second).UnixMilli())
	if genesis.State == nil {
		genesis.State = make(map[string]structures.Account)
	}
	teamKey := cryptography.GenerateKeyPair("", "", nil)
	genesis.State[teamKey.Pub] = structures.Account{Balance: 1_000_000_000, Nonce: 0}

	return genesis, teamKey, nil
}

func buildSignedRecoveryData(lastEpochIndex int, lastAbsoluteHeight int64, genesis structures.Genesis, teamKey cryptography.Ed25519Box) (structures.RecoveryData, error) {
	payload, err := buildRecoveryPayloadForHarness(lastEpochIndex, lastAbsoluteHeight, genesis)
	if err != nil {
		return structures.RecoveryData{}, err
	}
	return structures.RecoveryData{
		LastEpochIndex:     lastEpochIndex,
		LastAbsoluteHeight: lastAbsoluteHeight,
		Genesis:            genesis,
		TeamSig:            cryptography.GenerateSignature(teamKey.Prv, payload),
	}, nil
}

func buildRecoveryPayloadForHarness(lastEpochIndex int, lastAbsoluteHeight int64, genesis structures.Genesis) (string, error) {
	raw, err := json.Marshal(genesis)
	if err != nil {
		return "", fmt.Errorf("marshal recovery genesis: %w", err)
	}
	hashBytes := blake3.Sum256(raw)
	genesisHash := hex.EncodeToString(hashBytes[:])
	return fmt.Sprintf("RECOVERY_RESTART:%d:%d:%s", lastEpochIndex, lastAbsoluteHeight, genesisHash), nil
}

func writeCoreRecoveryPlan(coreNode NodeState, recoveryData structures.RecoveryData) error {
	stateDB, err := leveldb.OpenFile(filepath.Join(coreNode.ChaindataPath, "STATE"), nil)
	if err != nil {
		return err
	}
	defer stateDB.Close()

	recoveryDataBytes, err := json.Marshal(recoveryData)
	if err != nil {
		return err
	}
	height := fmt.Sprintf("%d", recoveryData.LastAbsoluteHeight)
	batch := new(leveldb.Batch)
	batch.Put([]byte(constants.DBKeyPrefixRecoveryData+height), recoveryDataBytes)
	batch.Put([]byte(constants.DBKeyRecoveryActive), []byte(height))
	return stateDB.Write(batch, nil)
}

func resetAnchorRuntimeState(anchor ManifestNode) error {
	return os.RemoveAll(filepath.Join(anchor.ChaindataPath, "DATABASES"))
}

func updateAnchorGenesisForRecovery(path string, recoveryGenesis structures.Genesis) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var genesis map[string]any
	if err := json.Unmarshal(raw, &genesis); err != nil {
		return err
	}
	genesis["NETWORK_ID"] = recoveryGenesis.NetworkId
	genesis["FIRST_EPOCH_START_TIMESTAMP"] = recoveryGenesis.FirstEpochStartTimestamp
	return writeJSON(path, genesis)
}

func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
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

func findManifestNodeByName(manifest Manifest, name string) (ManifestNode, error) {
	for _, node := range manifest.Nodes {
		if node.Name == name {
			return node, nil
		}
	}
	return ManifestNode{}, fmt.Errorf("manifest node %q not found", name)
}

func findNodeByRole(state RunState, role string) (NodeState, error) {
	for _, node := range state.Nodes {
		if node.Role == role {
			return node, nil
		}
	}
	return NodeState{}, fmt.Errorf("node with role %q not found", role)
}

func findNodeByName(state RunState, name string) (NodeState, error) {
	for _, node := range state.Nodes {
		if node.Name == name {
			return node, nil
		}
	}
	return NodeState{}, fmt.Errorf("node %q not found", name)
}

func findNodeByNameInList(nodes []NodeState, name string) (NodeState, error) {
	for _, node := range nodes {
		if node.Name == name {
			return node, nil
		}
	}
	return NodeState{}, fmt.Errorf("node %q not found", name)
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

func nodesExcept(nodes []NodeState, name string) []NodeState {
	filtered := make([]NodeState, 0, len(nodes))
	for _, node := range nodes {
		if node.Name != name {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func nodeNames(nodes []NodeState) []string {
	names := make([]string, 0, len(nodes))
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	return names
}

func activeCoreNodes(nodes []NodeState) []NodeState {
	active := make([]NodeState, 0, len(nodes))
	for _, node := range nodes {
		if node.Role == "core" && processAlive(node.PID) {
			active = append(active, node)
		}
	}
	return active
}

func replaceNode(nodes []NodeState, replacement NodeState) []NodeState {
	replaced := make([]NodeState, len(nodes))
	copy(replaced, nodes)
	for idx, node := range replaced {
		if node.Name == replacement.Name {
			replaced[idx] = replacement
			return replaced
		}
	}
	return append(replaced, replacement)
}

func readNodePublicKey(node NodeState) (string, error) {
	raw, err := os.ReadFile(filepath.Join(node.ChaindataPath, "configs.json"))
	if err != nil {
		return "", err
	}
	var payload struct {
		PublicKey string `json:"PUBLIC_KEY"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if payload.PublicKey == "" {
		return "", fmt.Errorf("%s configs.json has empty PUBLIC_KEY", node.Name)
	}
	return payload.PublicKey, nil
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
