package threads

import (
	"strconv"

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

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

		epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler

		anchorsAlignmentData := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByAnchors

		currentAnchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption

		currentAnchorBlockPointerObserved := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorBlockIndexObserved

		infoAboutAnchorLastBlock, infoAboutAnchorLastBlockExists := anchorsAlignmentData[currentAnchorIndex] // {index,hash}

		if infoAboutAnchorLastBlockExists && infoAboutAnchorLastBlock.Index == currentAnchorBlockPointerObserved {

			// Move to next anchor

			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption++

			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorBlockIndexObserved = -1

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

			continue

		}

		anchorData := globals.ANCHORS[currentAnchorIndex]

		for {

			// Try to get the next block + proof and do it until block will be unavailable or we finished with current block creator

			blockId := strconv.Itoa(epochHandlerRef.Id) + ":" + anchorData.Pubkey + ":" + strconv.Itoa(currentAnchorBlockPointerObserved+1)

			response := getAnchorBlockAndAfpFromAnchorsPoD(blockId)

			// If no data - break
			if response == nil || response.Block == nil {
				break
			}

			if infoAboutAnchorLastBlockExists && infoAboutAnchorLastBlock.Index == currentAnchorBlockPointerObserved && response.Block.GetHash() == infoAboutAnchorLastBlock.Hash {

				// Exec block without AFP

				for _, proof := range response.Block.ExtraData.AggregatedLeaderFinalizationProofs {

					if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, epochHandlerRef) {
						continue
					}

					if _, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader]; !exists {

						handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader] = structures.ExecutionStats{
							Index: proof.VotingStat.Index,
							Hash:  proof.VotingStat.Hash,
						}

					}

				}

			} else if response.Afp != nil && utils.VerifyAggregatedFinalizationProofForAnchorBlock(response.Afp) {

				// Exec block with AFP. Go through ALFP, verify and fill the SequenceAlignmentData.LastBlocksByLeaders

				for _, proof := range response.Block.ExtraData.AggregatedLeaderFinalizationProofs {

					if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, epochHandlerRef) {
						continue
					}

					if _, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader]; !exists {

						handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[proof.Leader] = structures.ExecutionStats{
							Index: proof.VotingStat.Index,
							Hash:  proof.VotingStat.Hash,
						}

					}

				}

			} else {

				break

			}

			// Finally, increase the index to move to the next block
			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorBlockIndexObserved++

		}

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	}

}
