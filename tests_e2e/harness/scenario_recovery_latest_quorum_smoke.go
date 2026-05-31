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
