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

	t.Logf("generated anchors: %v", anchors)
	t.Logf("generated leaders: %v", leaders)

	blocksByAnchor := make(map[string][]Block, len(anchors))
	for _, anchor := range anchors {
		count := rng.Intn(11) // 0..10 inclusive
		t.Logf("anchor %s will generate %d blocks", anchor, count)
		blocksByAnchor[anchor] = generateBlocksForAnchor(rng, count, anchors, leaders)
	}

	if len(blocksByAnchor) != len(anchors) {
		t.Fatalf("expected %d anchor entries, got %d", len(anchors), len(blocksByAnchor))
	}

	for anchor, blocks := range blocksByAnchor {
		if len(blocks) > 10 {
			t.Fatalf("anchor %s generated too many blocks: %d", anchor, len(blocks))
		}

		var prevHash string
		for i, blk := range blocks {
			expectedIndex := uint(i + 1)
			t.Logf("anchor %s inspecting block %d: hash=%s prev_hash=%s", anchor, expectedIndex, blk.Hash, blk.PrevHash)
			if blk.Index != expectedIndex {
				t.Fatalf("anchor %s block %d has unexpected index %d", anchor, i, blk.Index)
			}

			if blk.PrevHash != prevHash {
				t.Fatalf("anchor %s block %d has prev hash %q, expected %q", anchor, i, blk.PrevHash, prevHash)
			}

			if len(blk.AnchorsStopProofs) != len(anchors) {
				t.Fatalf("anchor %s block %d missing anchor proofs: expected %d, got %d", anchor, i, len(anchors), len(blk.AnchorsStopProofs))
			}
			t.Logf("anchor %s block %d anchor proofs: %d entries", anchor, expectedIndex, len(blk.AnchorsStopProofs))

			if len(blk.LeaderStopProofs) != len(leaders) {
				t.Fatalf("anchor %s block %d missing leader proofs: expected %d, got %d", anchor, i, len(leaders), len(blk.LeaderStopProofs))
			}
			t.Logf("anchor %s block %d leader proofs: %d entries", anchor, expectedIndex, len(blk.LeaderStopProofs))

			for _, proof := range blk.AnchorsStopProofs {
				if proof.Index != blk.Index {
					t.Fatalf("anchor %s block %d has mismatched anchor proof index %d", anchor, i, proof.Index)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty anchor proof hash", anchor, i)
				}
				if len(proof.Proofs) == 0 {
					t.Fatalf("anchor %s block %d expected anchor proof map to be populated", anchor, i)
				}
			}

			for _, proof := range blk.LeaderStopProofs {
				if proof.Index != blk.Index {
					t.Fatalf("anchor %s block %d has mismatched leader proof index %d", anchor, i, proof.Index)
				}
				if proof.Hash == "" {
					t.Fatalf("anchor %s block %d has empty leader proof hash", anchor, i)
				}
				if len(proof.Proofs) == 0 {
					t.Fatalf("anchor %s block %d expected leader proof map to be populated", anchor, i)
				}
			}

			prevHash = blk.Hash
		}
	}
}

func generateBlocksForAnchor(rng *rand.Rand, count int, anchors, leaders []string) []Block {
	blocks := make([]Block, count)
	var prevHash string
	for i := 0; i < count; i++ {
		idx := uint(i + 1)
		anchorProofs := make(map[string]AnchorStopProof, len(anchors))
		leaderProofs := make(map[string]LeaderStopProof, len(leaders))

		for _, anchor := range anchors {
			anchorProofs[anchor] = AnchorStopProof{
				Index:  idx,
				Hash:   randomHash(rng, fmt.Sprintf("anchor-%s", anchor), i),
				Proofs: randomProofs(rng, "anchor-proof", 2),
			}
		}

		for _, leader := range leaders {
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
