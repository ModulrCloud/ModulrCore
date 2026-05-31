package main

import (
	"fmt"
	"os"
	"regexp"
	"time"
)

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
