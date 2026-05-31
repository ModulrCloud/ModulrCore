package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

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
