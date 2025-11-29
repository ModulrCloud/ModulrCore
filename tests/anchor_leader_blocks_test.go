package tests

import (
	"fmt"
	"math/rand"
	"reflect"
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
	anchorPositions := make(map[string]int, len(anchors))
	for idx, name := range anchors {
		anchorPositions[name] = idx
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	selectedAnchorIndex := rng.Intn(len(anchors))
	selectedAnchor := anchors[selectedAnchorIndex]
	t.Logf("selected anchor: %s (index %d)", selectedAnchor, selectedAnchorIndex)

	blockCounts := make(map[string]int, len(anchors))
	for idx, anchor := range anchors {
		if idx == selectedAnchorIndex {
			blockCounts[anchor] = rng.Intn(10) + 1 // ensure at least one block to host required proofs
		} else {
			blockCounts[anchor] = rng.Intn(11)
		}
	}

	blocksByAnchor := make(map[string][]Block, len(anchors))
	for idx, anchor := range anchors {
		count := blockCounts[anchor]

		if idx == selectedAnchorIndex {
			blocksByAnchor[anchor] = generateTargetAnchorBlocks(rng, count, idx, anchors, leaders, blockCounts)
		} else {
			blocksByAnchor[anchor] = generateBlocksForAnchor(rng, count, idx, anchors, leaders, blockCounts)
		}
	}

	if len(blocksByAnchor) != len(anchors) {
		t.Fatalf("expected %d anchor entries, got %d", len(anchors), len(blocksByAnchor))
	}

	leaderFinal, anchorFinal := aggregateStopProofs(selectedAnchorIndex, anchors, anchorPositions, blocksByAnchor)

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

			for name, proof := range blk.AnchorsStopProofs {
				if anchorPositions[name] >= idx {
					t.Fatalf("anchor %s block %d references future anchor proof %s", anchor, i, name)
				}
				anchorBlockCount := blockCounts[name]
				if anchorBlockCount == 0 {
					if proof.Index != 0 {
						t.Fatalf("anchor %s block %d has anchor proof index %d exceeding available blocks for %s", anchor, i, proof.Index, name)
					}
				} else if proof.Index == 0 || int(proof.Index) > anchorBlockCount {
					t.Fatalf("anchor %s block %d has anchor proof index %d outside range 1-%d for %s", anchor, i, proof.Index, anchorBlockCount, name)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty anchor proof hash", anchor, i)
				}
			}

			for _, proof := range blk.LeaderStopProofs {
				if proof.Index == 0 {
					t.Fatalf("anchor %s block %d has zero leader proof index", anchor, i)
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

	validateLeaderAggregation(t, leaders, leaderFinal)
	validateAnchorAggregation(t, anchors[:selectedAnchorIndex], anchorFinal)

	visualizeAnchors(t, anchors, blocksByAnchor)
	visualizeAggregatedProofs(t, leaderFinal, anchorFinal)
}

func TestAggregateFromDifferentAnchorViews(t *testing.T) {
	anchors := generateSequential("anchor", 5)
	leaders := generateSequential("leader", 6)
	anchorPositions := make(map[string]int, len(anchors))
	for idx, name := range anchors {
		anchorPositions[name] = idx
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	firstTarget := rng.Intn(len(anchors))
	secondTarget := rng.Intn(len(anchors) - 1)
	if secondTarget >= firstTarget {
		secondTarget++
	}

	if secondTarget < firstTarget {
		firstTarget, secondTarget = secondTarget, firstTarget
	}

	t.Logf("selected anchors: %s (index %d), %s (index %d)", anchors[firstTarget], firstTarget, anchors[secondTarget], secondTarget)

	blockCounts := make(map[string]int, len(anchors))
	for idx, anchor := range anchors {
		if idx == firstTarget || idx == secondTarget {
			blockCounts[anchor] = rng.Intn(10) + 1
		} else {
			blockCounts[anchor] = rng.Intn(11)
		}
	}

	blocksByAnchor := make(map[string][]Block, len(anchors))
	for idx, anchor := range anchors {
		count := blockCounts[anchor]
		switch idx {
		case firstTarget, secondTarget:
			blocksByAnchor[anchor] = generateTargetAnchorBlocks(rng, count, idx, anchors, leaders, blockCounts)
		default:
			blocksByAnchor[anchor] = generateBlocksForAnchor(rng, count, idx, anchors, leaders, blockCounts)
		}
	}

	if len(blocksByAnchor) != len(anchors) {
		t.Fatalf("expected %d anchor entries, got %d", len(anchors), len(blocksByAnchor))
	}

	firstLeaderFinal, firstAnchorFinal := aggregateStopProofs(firstTarget, anchors, anchorPositions, blocksByAnchor)
	secondLeaderFinal, secondAnchorFinal := aggregateStopProofs(secondTarget, anchors, anchorPositions, blocksByAnchor)

	for idx, anchor := range anchors {
		blocks := blocksByAnchor[anchor]
		var prevHash string
		for i, blk := range blocks {
			expectedIndex := uint(i + 1)
			if blk.Index != expectedIndex {
				t.Fatalf("anchor %s block %d has unexpected index %d", anchor, i, blk.Index)
			}

			if blk.PrevHash != prevHash {
				t.Fatalf("anchor %s block %d has prev hash %q, expected %q", anchor, i, blk.PrevHash, prevHash)
			}

			for name, proof := range blk.AnchorsStopProofs {
				if anchorPositions[name] >= idx {
					t.Fatalf("anchor %s block %d references future anchor proof %s", anchor, i, name)
				}
				anchorBlockCount := blockCounts[name]
				if anchorBlockCount == 0 {
					if proof.Index != 0 {
						t.Fatalf("anchor %s block %d has anchor proof index %d exceeding available blocks for %s", anchor, i, proof.Index, name)
					}
				} else if proof.Index == 0 || int(proof.Index) > anchorBlockCount {
					t.Fatalf("anchor %s block %d has anchor proof index %d outside range 1-%d for %s", anchor, i, proof.Index, anchorBlockCount, name)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty anchor proof hash", anchor, i)
				}
			}

			for _, proof := range blk.LeaderStopProofs {
				if proof.Index == 0 {
					t.Fatalf("anchor %s block %d has zero leader proof index", anchor, i)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty leader proof hash", anchor, i)
				}
			}

			prevHash = blk.Hash
		}

		if idx == firstTarget || idx == secondTarget {
			validateTargetAnchorCoverage(t, anchor, blocks, anchors[:idx], leaders)
		}
	}

	validateLeaderAggregation(t, leaders, firstLeaderFinal)
	validateLeaderAggregation(t, leaders, secondLeaderFinal)
	validateAnchorAggregation(t, anchors[:firstTarget], firstAnchorFinal)
	validateAnchorAggregation(t, anchors[:secondTarget], secondAnchorFinal)

	if !leaderProofMapsEqual(firstLeaderFinal, secondLeaderFinal) {
		t.Fatalf("aggregated leader mappings differ between anchors %s and %s", anchors[firstTarget], anchors[secondTarget])
	}

	visualizeAnchors(t, anchors, blocksByAnchor)
	visualizeAggregatedProofs(t, firstLeaderFinal, firstAnchorFinal)
	visualizeAggregatedProofs(t, secondLeaderFinal, secondAnchorFinal)
}

func generateBlocksForAnchor(rng *rand.Rand, count, anchorIndex int, anchors, leaders []string, blockCounts map[string]int) []Block {
	blocks := make([]Block, count)
	var prevHash string
	for i := 0; i < count; i++ {
		idx := uint(i + 1)
		blocks[i] = Block{
			Index:             idx,
			Hash:              randomHash(rng, "block", i),
			PrevHash:          prevHash,
			AnchorsStopProofs: randomAnchorProofSubset(rng, anchors[:anchorIndex], blockCounts),
			LeaderStopProofs:  randomLeaderProofSubset(rng, leaders),
		}

		prevHash = blocks[i].Hash
	}

	return blocks
}

func generateTargetAnchorBlocks(rng *rand.Rand, count, targetIndex int, anchors, leaders []string, blockCounts map[string]int) []Block {
	blocks := make([]Block, count)
	var prevHash string

	anchorAssignments := distributeProofs(rng, count, anchors[:targetIndex])
	leaderAssignments := distributeProofs(rng, count, leaders)

	for i := 0; i < count; i++ {
		idx := uint(i + 1)

		anchorProofs := randomAnchorProofSubset(rng, anchors[:targetIndex], blockCounts)
		leaderProofs := randomLeaderProofSubset(rng, leaders)

		for _, anchor := range anchorAssignments[i] {
			anchorProofs[anchor] = AnchorStopProof{
				Index:  randomAnchorProofIndex(rng, blockCounts[anchor]),
				Hash:   randomHash(rng, fmt.Sprintf("anchor-%s", anchor), i),
				Proofs: randomProofs(rng, "anchor-proof", 2),
			}
		}

		for _, leader := range leaderAssignments[i] {
			leaderProofs[leader] = LeaderStopProof{
				Index:  randomProofIndex(rng),
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

func aggregateStopProofs(selectedAnchorIndex int, anchors []string, anchorPositions map[string]int, blocksByAnchor map[string][]Block) (map[string]LeaderStopProof, map[string]AnchorStopProof) {
	leaderFinal := make(map[string]LeaderStopProof)
	anchorFinal := make(map[string]AnchorStopProof)

	for ai := selectedAnchorIndex; ai >= 0; ai-- {
		anchor := anchors[ai]
		blocks := blocksByAnchor[anchor]
		for bi := len(blocks) - 1; bi >= 0; bi-- {
			blk := blocks[bi]
			for name, proof := range blk.LeaderStopProofs {
				leaderFinal[name] = proof
			}

			for name, proof := range blk.AnchorsStopProofs {
				if anchorPositions[name] >= ai {
					continue
				}
				anchorFinal[name] = proof
			}
		}
	}

	return leaderFinal, anchorFinal
}

func validateLeaderAggregation(t *testing.T, leaders []string, leaderFinal map[string]LeaderStopProof) {
	for _, leader := range leaders {
		proof, ok := leaderFinal[leader]
		if !ok {
			t.Fatalf("leader %s missing from aggregated mapping", leader)
		}
		if proof.Index == 0 {
			t.Fatalf("leader %s has zero index in aggregated mapping", leader)
		}
		if proof.Hash == "" {
			t.Fatalf("leader %s has empty hash in aggregated mapping", leader)
		}
	}
}

func validateAnchorAggregation(t *testing.T, priorAnchors []string, anchorFinal map[string]AnchorStopProof) {
	for _, anchor := range priorAnchors {
		proof, ok := anchorFinal[anchor]
		if !ok {
			t.Fatalf("anchor %s missing from aggregated mapping", anchor)
		}
		if proof.Hash == "" {
			t.Fatalf("anchor %s has empty hash in aggregated mapping", anchor)
		}
	}
}

func randomAnchorProofSubset(rng *rand.Rand, anchors []string, blockCounts map[string]int) map[string]AnchorStopProof {
	proofs := make(map[string]AnchorStopProof)
	for _, anchor := range anchors {
		if rng.Intn(2) == 0 { // roughly half will include the proof
			proofs[anchor] = AnchorStopProof{
				Index:  randomAnchorProofIndex(rng, blockCounts[anchor]),
				Hash:   randomHash(rng, fmt.Sprintf("anchor-%s", anchor), rng.Int()),
				Proofs: randomProofs(rng, "anchor-proof", 1),
			}
		}
	}
	return proofs
}

func randomLeaderProofSubset(rng *rand.Rand, leaders []string) map[string]LeaderStopProof {
	proofs := make(map[string]LeaderStopProof)
	for _, leader := range leaders {
		if rng.Intn(2) == 0 {
			proofs[leader] = LeaderStopProof{
				Index:  randomProofIndex(rng),
				Hash:   randomHash(rng, fmt.Sprintf("leader-%s", leader), rng.Int()),
				Proofs: randomProofs(rng, "leader-proof", 1),
			}
		}
	}
	return proofs
}

func visualizeAnchors(t *testing.T, anchors []string, blocksByAnchor map[string][]Block) {
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
	for name, proof := range m {
		keys = append(keys, fmt.Sprintf("%s(%d)", name, proof.Index))
	}
	return keys
}

func visualizeAggregatedProofs(t *testing.T, leaders map[string]LeaderStopProof, anchors map[string]AnchorStopProof) {
	t.Log("aggregated leader proofs (earliest wins):")
	for name, proof := range leaders {
		keys := make([]string, 0, len(proof.Proofs))
		for k := range proof.Proofs {
			keys = append(keys, k)
		}
		t.Logf("  %s -> index=%d hash=%s proofs=%v", name, proof.Index, proof.Hash, keys)
	}

	t.Log("aggregated anchor proofs (earliest wins):")
	for name, proof := range anchors {
		keys := make([]string, 0, len(proof.Proofs))
		for k := range proof.Proofs {
			keys = append(keys, k)
		}
		t.Logf("  %s -> index=%d hash=%s proofs=%v", name, proof.Index, proof.Hash, keys)
	}
}

func randomProofIndex(rng *rand.Rand) uint {
	return uint(rng.Intn(20) + 1)
}

func randomAnchorProofIndex(rng *rand.Rand, maxCount int) uint {
	if maxCount <= 0 {
		return 0
	}
	return uint(rng.Intn(maxCount) + 1)
}

func leaderProofMapsEqual(a, b map[string]LeaderStopProof) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		other, ok := b[k]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(v, other) {
			return false
		}
	}

	return true
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
