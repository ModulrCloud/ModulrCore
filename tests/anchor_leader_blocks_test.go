package tests

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

type AnchorStopProof struct {
	Index  uint
	Hash   string
	Proofs map[string]string
}

type LeaderStopProof struct {
	Index  uint
	Hash   string
	Proofs map[string]string
}

type Block struct {
	Index             uint
	Hash              string
	PrevHash          string
	AnchorsStopProofs map[string]AnchorStopProof
	LeaderStopProofs  map[string]LeaderStopProof
}

func TestGenerateAnchorBlocks(t *testing.T) {
	anchors := generateSequential("anchor", 5)
	leaders := generateSequential("leader", 6)

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	selectedAnchorIndex := rng.Intn(len(anchors))
	selectedAnchor := anchors[selectedAnchorIndex]
	t.Logf("selected anchor: %s (index %d)", selectedAnchor, selectedAnchorIndex)

	blocksByAnchor := make(map[string][]Block, len(anchors))
	for idx, anchor := range anchors {
		var count int
		if idx == selectedAnchorIndex {
			count = rng.Intn(10) + 1 // ensure at least one block to host required proofs
		} else {
			count = rng.Intn(11)
		}

		if idx == selectedAnchorIndex {
			blocksByAnchor[anchor] = generateTargetAnchorBlocks(rng, count, idx, anchors, leaders)
		} else {
			blocksByAnchor[anchor] = generateBlocksForAnchor(rng, count, anchors, leaders)
		}
	}

	if len(blocksByAnchor) != len(anchors) {
		t.Fatalf("expected %d anchor entries, got %d", len(anchors), len(blocksByAnchor))
	}

	for idx, anchor := range anchors {
		blocks := blocksByAnchor[anchor]
		if len(blocks) > 10 {
			t.Fatalf("anchor %s generated too many blocks: %d", anchor, len(blocks))
		}

		var prevHash string
		for i, blk := range blocks {
			expectedIndex := uint(i + 1)
			if blk.Index != expectedIndex {
				t.Fatalf("anchor %s block %d has unexpected index %d", anchor, i, blk.Index)
			}

			if blk.PrevHash != prevHash {
				t.Fatalf("anchor %s block %d has prev hash %q, expected %q", anchor, i, blk.PrevHash, prevHash)
			}

			for _, proof := range blk.AnchorsStopProofs {
				if proof.Index != blk.Index {
					t.Fatalf("anchor %s block %d has mismatched anchor proof index %d", anchor, i, proof.Index)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty anchor proof hash", anchor, i)
				}
			}

			for _, proof := range blk.LeaderStopProofs {
				if proof.Index != blk.Index {
					t.Fatalf("anchor %s block %d has mismatched leader proof index %d", anchor, i, proof.Index)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty leader proof hash", anchor, i)
				}
			}

			prevHash = blk.Hash
		}

		if idx == selectedAnchorIndex {
			validateTargetAnchorCoverage(t, anchor, blocks, anchors[:idx], leaders)
		}
	}

	visualizeAnchors(t, anchors, leaders, blocksByAnchor)
}

func generateBlocksForAnchor(rng *rand.Rand, count int, anchors, leaders []string) []Block {
	blocks := make([]Block, count)
	var prevHash string
	for i := 0; i < count; i++ {
		idx := uint(i + 1)
		blocks[i] = Block{
			Index:             idx,
			Hash:              randomHash(rng, "block", i),
			PrevHash:          prevHash,
			AnchorsStopProofs: randomAnchorProofSubset(rng, idx, anchors),
			LeaderStopProofs:  randomLeaderProofSubset(rng, idx, leaders),
		}

		prevHash = blocks[i].Hash
	}

	return blocks
}

func generateTargetAnchorBlocks(rng *rand.Rand, count, targetIndex int, anchors, leaders []string) []Block {
	blocks := make([]Block, count)
	var prevHash string

	anchorAssignments := distributeProofs(rng, count, anchors[:targetIndex])
	leaderAssignments := distributeProofs(rng, count, leaders)

	for i := 0; i < count; i++ {
		idx := uint(i + 1)

		anchorProofs := randomAnchorProofSubset(rng, idx, anchors)
		leaderProofs := randomLeaderProofSubset(rng, idx, leaders)

		for _, anchor := range anchorAssignments[i] {
			anchorProofs[anchor] = AnchorStopProof{
				Index:  idx,
				Hash:   randomHash(rng, fmt.Sprintf("anchor-%s", anchor), i),
				Proofs: randomProofs(rng, "anchor-proof", 2),
			}
		}

		for _, leader := range leaderAssignments[i] {
			leaderProofs[leader] = LeaderStopProof{
				Index:  idx,
				Hash:   randomHash(rng, fmt.Sprintf("leader-%s", leader), i),
				Proofs: randomProofs(rng, "leader-proof", 3),
			}
		}

		blocks[i] = Block{
			Index:             idx,
			Hash:              randomHash(rng, "block", i),
			PrevHash:          prevHash,
			AnchorsStopProofs: anchorProofs,
			LeaderStopProofs:  leaderProofs,
		}

		prevHash = blocks[i].Hash
	}

	return blocks
}

func distributeProofs(rng *rand.Rand, blockCount int, names []string) map[int][]string {
	assignments := make(map[int][]string, blockCount)
	if blockCount == 0 {
		return assignments
	}

	for _, name := range names {
		blockIndex := rng.Intn(blockCount)
		assignments[blockIndex] = append(assignments[blockIndex], name)
	}

	return assignments
}

func validateTargetAnchorCoverage(t *testing.T, anchor string, blocks []Block, previousAnchors, leaders []string) {
	seenAnchors := make(map[string]struct{}, len(previousAnchors))
	seenLeaders := make(map[string]struct{}, len(leaders))

	for _, blk := range blocks {
		for name, proof := range blk.AnchorsStopProofs {
			if contains(previousAnchors, name) && len(proof.Proofs) > 0 {
				seenAnchors[name] = struct{}{}
			}
		}
		for name, proof := range blk.LeaderStopProofs {
			if len(proof.Proofs) > 0 {
				seenLeaders[name] = struct{}{}
			}
		}
	}

	for _, prev := range previousAnchors {
		if _, ok := seenAnchors[prev]; !ok {
			t.Fatalf("anchor %s is missing coverage for previous anchor %s", anchor, prev)
		}
	}

	for _, leader := range leaders {
		if _, ok := seenLeaders[leader]; !ok {
			t.Fatalf("anchor %s is missing coverage for leader %s", anchor, leader)
		}
	}
}

func randomAnchorProofSubset(rng *rand.Rand, idx uint, anchors []string) map[string]AnchorStopProof {
	proofs := make(map[string]AnchorStopProof)
	for _, anchor := range anchors {
		if rng.Intn(2) == 0 { // roughly half will include the proof
			proofs[anchor] = AnchorStopProof{
				Index:  idx,
				Hash:   randomHash(rng, fmt.Sprintf("anchor-%s", anchor), int(idx)),
				Proofs: randomProofs(rng, "anchor-proof", 1),
			}
		}
	}
	return proofs
}

func randomLeaderProofSubset(rng *rand.Rand, idx uint, leaders []string) map[string]LeaderStopProof {
	proofs := make(map[string]LeaderStopProof)
	for _, leader := range leaders {
		if rng.Intn(2) == 0 {
			proofs[leader] = LeaderStopProof{
				Index:  idx,
				Hash:   randomHash(rng, fmt.Sprintf("leader-%s", leader), int(idx)),
				Proofs: randomProofs(rng, "leader-proof", 1),
			}
		}
	}
	return proofs
}

func visualizeAnchors(t *testing.T, anchors, leaders []string, blocksByAnchor map[string][]Block) {
	for _, anchor := range anchors {
		blocks := blocksByAnchor[anchor]
		t.Logf("anchor %s: %d blocks", anchor, len(blocks))
		for _, blk := range blocks {
			anchorProofKeys := keysFromAnchorProofs(blk.AnchorsStopProofs)
			leaderProofKeys := keysFromLeaderProofs(blk.LeaderStopProofs)
			t.Logf("  block #%d hash=%s prev=%s", blk.Index, blk.Hash, blk.PrevHash)
			t.Logf("    anchor proofs -> %v", anchorProofKeys)
			t.Logf("    leader proofs -> %v", leaderProofKeys)
		}
		if len(blocks) == 0 {
			t.Log("  (no blocks)")
		}
		t.Log("------------------------------")
	}
}

func keysFromAnchorProofs(m map[string]AnchorStopProof) []string {
	keys := make([]string, 0, len(m))
	for name, proof := range m {
		keys = append(keys, fmt.Sprintf("%s(%d)", name, proof.Index))
	}
	return keys
}

func keysFromLeaderProofs(m map[string]LeaderStopProof) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func generateSequential(prefix string, count int) []string {
	values := make([]string, count)
	for i := 0; i < count; i++ {
		values[i] = fmt.Sprintf("%s%d", prefix, i+1)
	}
	return values
}

func randomHash(rng *rand.Rand, prefix string, idx int) string {
	return fmt.Sprintf("%s-%d-%d", prefix, idx, rng.Int63())
}

func randomProofs(rng *rand.Rand, prefix string, minEntries int) map[string]string {
	entries := rng.Intn(3) + minEntries // ensures at least minEntries entries
	proofs := make(map[string]string, entries)
	for i := 0; i < entries; i++ {
		key := fmt.Sprintf("%s-%d", prefix, i)
		proofs[key] = randomHash(rng, prefix, i)
	}
	return proofs
}
