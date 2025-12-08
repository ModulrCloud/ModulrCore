package threads

import (
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type SequenceAlignmentAnchorData struct {
	AggregatedAnchorRotationProof anchors_pack.AggregatedAnchorRotationProof `json:"aarp"`
	FoundInBlock                  int                                        `json:"foundInBlock"`
}

type SequenceAlignmentDataResponse struct {
	FoundInAnchorIndex int                                     `json:"foundInAnchorIndex"`
	Anchors            map[int]SequenceAlignmentAnchorData     `json:"anchors"`
	Afp                *structures.AggregatedFinalizationProof `json:"afp,omitempty"`
}

func SequenceAlignmentThread() {

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		anchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption
		epochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		latestAnchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if latestAnchorIndex != anchorIndex {
			time.Sleep(time.Second)
			continue
		}

		if anchorIndex < 0 || anchorIndex >= len(globals.ANCHORS) {
			time.Sleep(time.Second)
			continue
		}

		anchor := globals.ANCHORS[anchorIndex]
		blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, 0)

		response := getAnchorBlockAndAfpFromAnchorsPoD(blockID)
		if response == nil || response.Block == nil || response.Afp == nil {
			time.Sleep(time.Second)
			continue
		}

		if response.Afp.BlockId != blockID || response.Block.Creator != anchor.Pubkey || !response.Block.VerifySignature() {
			time.Sleep(time.Second)
			continue
		}

		if !utils.VerifyAggregatedFinalizationProof(response.Afp, &epochHandler) {
			time.Sleep(time.Second)
			continue
		}

		for _, proof := range response.Block.ExtraData.AggregatedLeaderFinalizationProofs {

			firstBlockHash := structures.NewLeaderVotingStatTemplate().Hash
			if firstBlock := block_pack.GetBlock(epochHandler.Id, proof.Leader, 0, &epochHandler); firstBlock != nil {
				firstBlockHash = firstBlock.GetHash()
			}

			if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, &epochHandler, firstBlockHash) {
				continue
			}

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

			if _, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.InfoAboutLastBlocksInEpoch[proof.Leader]; !exists {
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.InfoAboutLastBlocksInEpoch[proof.Leader] = structures.ExecutionStatsPerLeaderSequence{
					Index:          proof.VotingStat.Index,
					Hash:           proof.VotingStat.Hash,
					FirstBlockHash: "",
				}
			}

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
		}

		time.Sleep(time.Second)
	}

}
