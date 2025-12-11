package anchors_pack

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type AggregatedAnchorRotationProof struct {
	EpochIndex int                   `json:"epochIndex"`
	Anchor     string                `json:"anchor"`
	VotingStat structures.VotingStat `json:"votingStat"`
	Signatures map[string]string     `json:"signatures"`
}

func BuildAnchorRotationProofPayload(anchor string, blockIndex int, blockHash string, epochIndex int) string {

	return fmt.Sprintf("ANCHOR_ROTATION_PROOF:%s:%d:%s:%d", anchor, blockIndex, blockHash, epochIndex)
}

func VerifyAggregatedAnchorRotationProof(proof *AggregatedAnchorRotationProof, epochHandler *structures.EpochDataHandler) bool {

	if proof.VotingStat.Afp.BlockId == "" {
		return false
	}
	if !slices.Contains(globals.ANCHORS_PUBKEYS, proof.Anchor) {
		return false
	}
	expectedBlockId := fmt.Sprintf("%d:%s:%d", proof.EpochIndex, proof.Anchor, proof.VotingStat.Index)
	if proof.VotingStat.Afp.BlockId != expectedBlockId {
		return false
	}
	if proof.VotingStat.Hash != proof.VotingStat.Afp.BlockHash {
		return false
	}

	blockParts := strings.Split(proof.VotingStat.Afp.BlockId, ":")

	afpIndex, err := strconv.Atoi(blockParts[2])

	if err != nil || afpIndex != proof.VotingStat.Index {
		return false
	}

	dataToVerify := BuildAnchorRotationProofPayload(proof.Anchor, proof.VotingStat.Index, proof.VotingStat.Hash, proof.EpochIndex)

	quorum := epochHandler.Quorum
	verified := 0
	seen := make(map[string]struct{})
	for voter, signature := range proof.Signatures {
		if signature == "" {
			continue
		}
		if _, dup := seen[voter]; dup {
			continue
		}
		if !slices.Contains(quorum, voter) {
			continue
		}
		if !cryptography.VerifySignature(dataToVerify, voter, signature) {
			continue
		}
		seen[voter] = struct{}{}
		verified++
	}

	return verified >= utils.GetAnchorsQuorumMajority()

}
