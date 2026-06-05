package main

import (
	"errors"
	"fmt"
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
	case "recovery_scheduled_transition_smoke":
		return recoveryScheduledTransitionSmokeScenario(args[1:])
	case "long_running_stability":
		return longRunningStabilityScenario(args[1:])
	case "long_running_21_validator_liveness":
		return longRunning21ValidatorLivenessScenario(args[1:])
	case "long_running_stability_with_temporary_validator_down":
		return longRunningStabilityWithTemporaryValidatorDownScenario(args[1:])
	default:
		return fmt.Errorf("unknown scenario %q", args[0])
	}
}
