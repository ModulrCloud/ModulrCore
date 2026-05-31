package main

import (
	"fmt"
	"os"
)

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
