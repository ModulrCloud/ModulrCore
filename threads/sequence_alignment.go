package threads

import (
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
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

		epochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler

		anchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption

		catchUpTargets := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByAnchors

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		anchor := globals.ANCHORS[anchorIndex]

		target, hasInfoAboutLastBlockByAnchor := catchUpTargets[anchorIndex]

		if !hasInfoAboutLastBlockByAnchor {

			currentExecIndex = 0
			blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, anchorIndex)

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

				if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, &epochHandler) {
					continue
				}

				handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

				if _, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader]; !exists {

					handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader] = structures.ExecutionStats{
						Index: proof.VotingStat.Index,
						Hash:  proof.VotingStat.Hash,
					}

				}

				handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			}

			time.Sleep(time.Second)

			continue

		}

		blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, currentExecIndex)

		response := getAnchorBlockAndAfpFromAnchorsPoD(blockID)

		if response == nil || response.Block == nil {
			time.Sleep(time.Second)
			continue
		}

		block := response.Block

		if block.Creator != anchor.Pubkey || block.Index != currentExecIndex || !block.VerifySignature() {
			time.Sleep(time.Second)
			continue
		}

		if currentExecIndex < target.Index {
			if response.Afp != nil {
				if response.Afp.BlockId != blockID || !utils.VerifyAggregatedFinalizationProof(response.Afp, &epochHandler) {
					time.Sleep(time.Second)
					continue
				}
			}

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentLeaderToExecBlocksFrom = currentExecIndex + 1
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			time.Sleep(time.Second)
			continue
		}

		if currentExecIndex == target.Index {
			actualHash := block.GetHash()

			if target.Hash != "" && actualHash != target.Hash {
				time.Sleep(time.Second)
				continue
			}

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[anchor.Pubkey] = structures.ExecutionStats{
				Index:          target.Index,
				Hash:           actualHash,
				FirstBlockHash: "",
			}

			nextAnchor := anchorIndex + 1
			if nextAnchor < len(globals.ANCHORS) {
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption = nextAnchor
			} else {
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption = nextAnchor
			}
			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentLeaderToExecBlocksFrom = 0
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
		}

		time.Sleep(time.Second)
	}

}
