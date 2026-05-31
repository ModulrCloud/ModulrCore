package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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
