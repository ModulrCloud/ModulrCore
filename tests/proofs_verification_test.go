package tests

import (
	"strconv"
	"strings"
	"testing"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func TestAggregatedProofVerificationMatrix(t *testing.T) {
	quorum := []cryptography.Ed25519Box{
		cryptography.GenerateKeyPair("", "", nil),
		cryptography.GenerateKeyPair("", "", nil),
		cryptography.GenerateKeyPair("", "", nil),
		cryptography.GenerateKeyPair("", "", nil),
	}
	nonQuorum := cryptography.GenerateKeyPair("", "", nil)
	epochHandler := proofMatrixEpochHandler(quorum)

	t.Run("block finalization proof requires current quorum majority over exact payload", func(t *testing.T) {
		proof := buildCoreFinalizationProofForTest(quorum[:3], epochHandler, "prev-hash", "1:"+quorum[0].Pub+":4", "block-hash")
		if !utils.VerifyAggregatedFinalizationProof(&proof, epochHandler) {
			t.Fatalf("expected valid AFP to verify")
		}

		tampered := proof
		tampered.BlockHash = "other-hash"
		if utils.VerifyAggregatedFinalizationProof(&tampered, epochHandler) {
			t.Fatalf("expected tampered AFP block hash to fail")
		}

		insufficient := buildCoreFinalizationProofForTest(quorum[:2], epochHandler, proof.PrevBlockHash, proof.BlockId, proof.BlockHash)
		insufficient.Proofs[nonQuorum.Pub] = cryptography.GenerateSignature(nonQuorum.Prv, coreFinalizationPayload(epochHandler, proof.PrevBlockHash, proof.BlockId, proof.BlockHash))
		if utils.VerifyAggregatedFinalizationProof(&insufficient, epochHandler) {
			t.Fatalf("expected non-quorum signature not to satisfy majority")
		}
	})

	t.Run("leader finalization proof validates embedded AFP and signed voting stat", func(t *testing.T) {
		leader := quorum[0].Pub
		afp := buildCoreFinalizationProofForTest(quorum[:3], epochHandler, "prev-hash", "1:"+leader+":4", "leader-block-hash")
		proof := buildCoreLeaderFinalizationProofForTest(quorum[:3], epochHandler, leader, structures.VotingStat{
			Index: 4,
			Hash:  afp.BlockHash,
			Afp:   afp,
		})
		if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, epochHandler) {
			t.Fatalf("expected valid ALFP to verify")
		}

		tampered := proof
		tampered.VotingStat.Hash = "other-hash"
		if utils.VerifyAggregatedLeaderFinalizationProof(&tampered, epochHandler) {
			t.Fatalf("expected ALFP with tampered voting hash to fail")
		}

		wrongEpoch := proof
		wrongEpoch.EpochIndex = epochHandler.Id + 1
		if utils.VerifyAggregatedLeaderFinalizationProof(&wrongEpoch, epochHandler) {
			t.Fatalf("expected ALFP for different epoch to fail")
		}
	})

	t.Run("height proof requires quorum signatures over exact height tuple", func(t *testing.T) {
		proof := buildCoreHeightProofForTest(quorum[:3], 42, "1:"+quorum[0].Pub+":4", "height-hash", epochHandler.Id, 4)
		if !utils.VerifyAggregatedHeightProof(&proof, epochHandler) {
			t.Fatalf("expected valid height proof to verify")
		}

		tampered := proof
		tampered.AbsoluteHeight++
		if utils.VerifyAggregatedHeightProof(&tampered, epochHandler) {
			t.Fatalf("expected tampered height proof to fail")
		}
	})

	t.Run("epoch rotation proof validates epoch data hash and quorum signatures", func(t *testing.T) {
		proof := buildCoreEpochRotationProofForTest(t, quorum[:3], epochHandler)
		if !utils.VerifyAggregatedEpochRotationProof(&proof, epochHandler) {
			t.Fatalf("expected valid AERP to verify")
		}

		tamperedHash := proof
		tamperedHash.EpochDataHash = "tampered-hash"
		if utils.VerifyAggregatedEpochRotationProof(&tamperedHash, epochHandler) {
			t.Fatalf("expected AERP with tampered epoch data hash to fail")
		}

		tamperedNext := proof
		tamperedNext.NextEpochId = proof.EpochId + 2
		if utils.VerifyAggregatedEpochRotationProof(&tamperedNext, epochHandler) {
			t.Fatalf("expected non-sequential AERP to fail")
		}
	})

	t.Run("anchor epoch ack proof requires anchors majority", func(t *testing.T) {
		previousAnchors := globals.ANCHORS_PUBKEYS
		previousAnchorStorages := globals.ANCHORS
		globals.ANCHORS_PUBKEYS = []string{quorum[0].Pub, quorum[1].Pub, quorum[2].Pub, quorum[3].Pub}
		globals.ANCHORS = []structures.Anchor{
			{Pubkey: quorum[0].Pub},
			{Pubkey: quorum[1].Pub},
			{Pubkey: quorum[2].Pub},
			{Pubkey: quorum[3].Pub},
		}
		t.Cleanup(func() {
			globals.ANCHORS_PUBKEYS = previousAnchors
			globals.ANCHORS = previousAnchorStorages
		})

		proof := buildCoreAnchorEpochAckProofForTest(quorum[:3], 1, 2, "next-epoch-data-hash")
		if !utils.VerifyAggregatedAnchorEpochAckProof(&proof) {
			t.Fatalf("expected valid anchor epoch ack proof to verify")
		}

		insufficient := buildCoreAnchorEpochAckProofForTest(quorum[:2], 1, 2, "next-epoch-data-hash")
		insufficient.Proofs[nonQuorum.Pub] = cryptography.GenerateSignature(nonQuorum.Prv, coreAnchorEpochAckPayload(1, 2, "next-epoch-data-hash"))
		if utils.VerifyAggregatedAnchorEpochAckProof(&insufficient) {
			t.Fatalf("expected non-anchor signature not to satisfy anchors majority")
		}
	})
}

func proofMatrixEpochHandler(quorum []cryptography.Ed25519Box) *structures.EpochDataHandler {
	pubkeys := make([]string, 0, len(quorum))
	for _, member := range quorum {
		pubkeys = append(pubkeys, member.Pub)
	}

	return &structures.EpochDataHandler{
		Id:                 1,
		Hash:               "epoch-hash",
		ValidatorsRegistry: pubkeys,
		Quorum:             pubkeys,
		LeadersSequence:    pubkeys,
	}
}

func buildCoreFinalizationProofForTest(
	signers []cryptography.Ed25519Box,
	epochHandler *structures.EpochDataHandler,
	prevHash string,
	blockID string,
	blockHash string,
) structures.AggregatedFinalizationProof {
	payload := coreFinalizationPayload(epochHandler, prevHash, blockID, blockHash)
	proofs := make(map[string]string, len(signers))
	for _, signer := range signers {
		proofs[signer.Pub] = cryptography.GenerateSignature(signer.Prv, payload)
	}

	return structures.AggregatedFinalizationProof{
		PrevBlockHash: prevHash,
		BlockId:       blockID,
		BlockHash:     blockHash,
		Proofs:        proofs,
	}
}

func coreFinalizationPayload(epochHandler *structures.EpochDataHandler, prevHash string, blockID string, blockHash string) string {
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)
	return strings.Join([]string{prevHash, blockID, blockHash, epochFullID}, ":")
}

func buildCoreLeaderFinalizationProofForTest(
	signers []cryptography.Ed25519Box,
	epochHandler *structures.EpochDataHandler,
	leader string,
	votingStat structures.VotingStat,
) structures.AggregatedLeaderFinalizationProof {
	payload := strings.Join([]string{
		constants.SigningPrefixLeaderFinalization,
		leader,
		strconv.Itoa(votingStat.Index),
		votingStat.Hash,
		epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id),
	}, ":")
	signatures := make(map[string]string, len(signers))
	for _, signer := range signers {
		signatures[signer.Pub] = cryptography.GenerateSignature(signer.Prv, payload)
	}

	return structures.AggregatedLeaderFinalizationProof{
		EpochIndex: epochHandler.Id,
		Leader:     leader,
		VotingStat: votingStat,
		Signatures: signatures,
	}
}

func buildCoreHeightProofForTest(
	signers []cryptography.Ed25519Box,
	absoluteHeight int,
	blockID string,
	blockHash string,
	epochID int,
	heightInEpoch int,
) structures.AggregatedHeightProof {
	payload := strings.Join([]string{
		constants.SigningPrefixHeightProof,
		strconv.Itoa(absoluteHeight),
		blockID,
		blockHash,
		strconv.Itoa(epochID),
		strconv.Itoa(heightInEpoch),
	}, ":")
	proofs := make(map[string]string, len(signers))
	for _, signer := range signers {
		proofs[signer.Pub] = cryptography.GenerateSignature(signer.Prv, payload)
	}

	return structures.AggregatedHeightProof{
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockID,
		BlockHash:      blockHash,
		EpochId:        epochID,
		HeightInEpoch:  heightInEpoch,
		Proofs:         proofs,
	}
}

func buildCoreEpochRotationProofForTest(
	t *testing.T,
	signers []cryptography.Ed25519Box,
	epochHandler *structures.EpochDataHandler,
) structures.AggregatedEpochRotationProof {
	t.Helper()

	nextEpochData := structures.NextEpochDataHandler{
		NextEpochHash:               "next-epoch-hash",
		NextEpochValidatorsRegistry: epochHandler.ValidatorsRegistry,
		NextEpochQuorum:             epochHandler.Quorum,
		NextEpochLeadersSequence:    epochHandler.LeadersSequence,
		NextEpochStartTimestamp:     123456789,
	}
	epochDataHash := utils.ComputeEpochDataHash(&nextEpochData)
	payload := utils.BuildEpochRotationProofSigningPayload(
		epochHandler.Id,
		epochHandler.Id+1,
		epochDataHash,
		42,
		"1:"+epochHandler.Quorum[0]+":4",
		"finished-hash",
	)
	proofs := make(map[string]string, len(signers))
	for _, signer := range signers {
		proofs[signer.Pub] = cryptography.GenerateSignature(signer.Prv, payload)
	}

	return structures.AggregatedEpochRotationProof{
		EpochId:           epochHandler.Id,
		NextEpochId:       epochHandler.Id + 1,
		EpochData:         nextEpochData,
		EpochDataHash:     epochDataHash,
		FinishedOnHeight:  42,
		FinishedOnBlockId: "1:" + epochHandler.Quorum[0] + ":4",
		FinishedOnHash:    "finished-hash",
		Proofs:            proofs,
	}
}

func buildCoreAnchorEpochAckProofForTest(
	signers []cryptography.Ed25519Box,
	epochID int,
	nextEpochID int,
	epochDataHash string,
) structures.AggregatedAnchorEpochAckProof {
	payload := coreAnchorEpochAckPayload(epochID, nextEpochID, epochDataHash)
	proofs := make(map[string]string, len(signers))
	for _, signer := range signers {
		proofs[signer.Pub] = cryptography.GenerateSignature(signer.Prv, payload)
	}

	return structures.AggregatedAnchorEpochAckProof{
		EpochId:       epochID,
		NextEpochId:   nextEpochID,
		EpochDataHash: epochDataHash,
		Proofs:        proofs,
	}
}

func coreAnchorEpochAckPayload(epochID int, nextEpochID int, epochDataHash string) string {
	return strings.Join([]string{
		constants.SigningPrefixAnchorEpochAckProof,
		strconv.Itoa(epochID),
		strconv.Itoa(nextEpochID),
		epochDataHash,
	}, ":")
}
