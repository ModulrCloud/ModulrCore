package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usageAndExit()
	}

	var err error
	switch os.Args[1] {
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return
	case "prepare":
		err = prepareCmd(os.Args[2:])
	case "start":
		err = startCmd(os.Args[2:])
	case "status":
		err = statusCmd(os.Args[2:])
	case "logs":
		err = logsCmd(os.Args[2:])
	case "scenario":
		err = scenarioCmd(os.Args[2:])
	case "stop":
		err = stopCmd(os.Args[2:])
	default:
		usageAndExit()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usageAndExit() {
	printUsage(os.Stderr)
	os.Exit(2)
}

func printUsage(out io.Writer) {
	fmt.Fprintf(out, `usage:
  go run ./tests_e2e/harness prepare [-core 1] [-anchors 1] [-overwrite]
  go run ./tests_e2e/harness scenario bootstrap_smoke
  go run ./tests_e2e/harness scenario debug_api_smoke
  go run ./tests_e2e/harness scenario alfp_pull_smoke
  go run ./tests_e2e/harness scenario early_epoch_announcement_alfp_smoke
  go run ./tests_e2e/harness scenario epoch_anchor_ack_smoke
  go run ./tests_e2e/harness scenario recovery_latest_quorum_smoke
  go run ./tests_e2e/harness scenario multi_node_quorum_smoke
  go run ./tests_e2e/harness scenario multi_node_one_anchor_down_smoke
  go run ./tests_e2e/harness scenario anchor_rotation_aarp_smoke
  go run ./tests_e2e/harness scenario multi_node_one_core_down_smoke
  go run ./tests_e2e/harness scenario multi_node_alfp_pull_after_push_failure
  go run ./tests_e2e/harness scenario multi_node_recovery_majority_latest_quorum
  go run ./tests_e2e/harness scenario multi_node_lagging_anchor_catchup
  go run ./tests_e2e/harness scenario multi_node_network_partition_no_false_majority
  go run ./tests_e2e/harness scenario recovery_script_style
  go run ./tests_e2e/harness scenario recovery_full_cycle_smoke
  go run ./tests_e2e/harness scenario long_running_stability
  go run ./tests_e2e/harness scenario long_running_21_validator_liveness
  go run ./tests_e2e/harness scenario long_running_stability_with_temporary_validator_down
  go run ./tests_e2e/harness start  -manifest tests_e2e/manifests/example.json
  go run ./tests_e2e/harness status [-run-dir tests_e2e/runs/latest]
  go run ./tests_e2e/harness logs   -node core-1 [-stream stdout] [-lines 120]
  go run ./tests_e2e/harness stop   [-run-dir tests_e2e/runs/latest]
  go run ./tests_e2e/harness help

manifest node command examples:
  ["go", "run", "."]
  ["/absolute/path/to/modulr"]

`)
}
