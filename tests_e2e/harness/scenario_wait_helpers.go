package main

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type coreHeightSnapshot struct {
	ByNode map[string]int64
	Min    int64
	Max    int64
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
