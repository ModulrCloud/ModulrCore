package threads

import (
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type TargetResponse struct {
	ProposedIndexOfLeader            int                                    `json:"proposedIndexOfLeader"`
	FirstBlockByCurrentLeader        block_pack.Block                       `json:"firstBlockByCurrentLeader"`
	AfpForSecondBlockByCurrentLeader structures.AggregatedFinalizationProof `json:"afpForSecondBlockByCurrentLeader"`
}

func SequenceAlignmentThread() {

	// In this function we should time by time ask for ALRPs from quorum to understand of how to continue block sequence

	for {

		globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RLock()

		epochHandlerRef := &globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler

		localVersionOfCurrentLeader := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.CurrentLeaderAssumption

		quorumMembers := utils.GetQuorumUrlsAndPubkeys(&globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler)

		randomTarget := utils.GetRandomFromSlice(quorumMembers)

		// Now send request to random quorum member

		client := &http.Client{
			Timeout: 5 * time.Second,
		}

		resp, err := client.Get(randomTarget.Url)

		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			time.Sleep(time.Second)
			continue
		}

		var targetResponse TargetResponse

		if err := json.NewDecoder(resp.Body).Decode(&targetResponse); err == nil {

			if localVersionOfCurrentLeader <= targetResponse.ProposedIndexOfLeader && targetResponse.FirstBlockByCurrentLeader.Index == 0 {

				// Verify the AFP for second block(with index 1 in epoch) to make sure that block 0(first block in epoch) was 100% accepted

				afp := &targetResponse.AfpForSecondBlockByCurrentLeader
				firstBlock := &targetResponse.FirstBlockByCurrentLeader
				proposedIndex := targetResponse.ProposedIndexOfLeader

				sameHash := afp.PrevBlockHash == firstBlock.GetHash()
				validProof := utils.VerifyAggregatedFinalizationProof(afp, epochHandlerRef)

				if sameHash && validProof {

					// Verify all the ALRPs in block header

					if epochHandlerRef.LeadersSequence[proposedIndex] == firstBlock.Creator {

						isOk, infoAboutFinalBlocks := firstBlock.ExtendedCheckAlrpChainValidity(epochHandlerRef, proposedIndex, true)

						shouldChange := true

						if isOk {

							collectionOfAlrpsFromAllThePreviousLeaders := []map[string]structures.ExecutionStatsPerPool{infoAboutFinalBlocks} // each element here is object like {pool:{index,hash,firstBlockHash}}

							currentAlrpSet := map[string]structures.ExecutionStatsPerPool{}

							for poolKey, execStats := range infoAboutFinalBlocks {

								currentAlrpSet[poolKey] = structures.ExecutionStatsPerPool{
									Index:          execStats.Index,
									Hash:           execStats.Hash,
									FirstBlockHash: execStats.FirstBlockHash,
								}

							}

							position := targetResponse.ProposedIndexOfLeader - 1

							/*

							   ________________ What to do next? ________________

							   Now we know that proposed leader has created some first block(firstBlockByCurrentLeader)

							   and we verified the AFP so it's clear proof that block is 100% accepted and the data inside is valid and will be a part of epoch data



							   Now, start the cycle in reverse order on range

							   [proposedIndexOfLeader-1 ; localVersionOfCurrentLeader]

							*/

							if position >= localVersionOfCurrentLeader {

								for {

									for ; position >= localVersionOfCurrentLeader; position-- {

										poolOnThisPosition := epochHandlerRef.LeadersSequence[position]

										alrpForThisPoolFromCurrentSet := currentAlrpSet[poolOnThisPosition]

										if alrpForThisPoolFromCurrentSet.Index != -1 {

											// Ask the first block and extract next set of ALRPs

											firstBlockInThisEpochByPool := block_pack.GetBlock(epochHandlerRef.Id, poolOnThisPosition, 0, epochHandlerRef)

											if firstBlockInThisEpochByPool != nil && firstBlockInThisEpochByPool.GetHash() == alrpForThisPoolFromCurrentSet.FirstBlockHash {

												alrpChainValidationOk, dataAboutLastBlocks := false, make(map[string]structures.ExecutionStatsPerPool)

												if position == 0 {

													alrpChainValidationOk = true

												} else {

													alrpChainValidationOk, dataAboutLastBlocks = firstBlockInThisEpochByPool.ExtendedCheckAlrpChainValidity(
														epochHandlerRef, position, true,
													)

												}

												if alrpChainValidationOk {

													collectionOfAlrpsFromAllThePreviousLeaders = append(collectionOfAlrpsFromAllThePreviousLeaders, dataAboutLastBlocks)

													currentAlrpSet = dataAboutLastBlocks

													position--

													break

												} else {

													shouldChange = false

													break
												}

											} else {

												shouldChange = false

												break

											}

										}

									}

									if !shouldChange || position <= localVersionOfCurrentLeader {

										break
									}

								}

								// Now, <collectionOfAlrpsFromAllThePreviousLeaders> is array of objects like {pool:{index,hash,firstBlockHash}}
								// We need to reverse it and fill the temp data for VT

								if shouldChange {

									// Release read mutex and immediately acquire mutex to write operation

									storedEpochIndex := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.Id

									globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

									globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.Lock()

									if globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.Id == storedEpochIndex {

										slices.Reverse(collectionOfAlrpsFromAllThePreviousLeaders)

										for _, poolsExecStats := range collectionOfAlrpsFromAllThePreviousLeaders {

											// collectionOfAlrpsFromAllThePreviousLeaders[i] = {pool0:{index,hash},poolN:{index,hash}}

											for poolPubKey, poolExecData := range poolsExecStats {

												_, dataAlreadyExists := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.InfoAboutLastBlocksInEpoch[poolPubKey]

												if !dataAlreadyExists {

													globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.InfoAboutLastBlocksInEpoch[poolPubKey] = poolExecData

												}

											}

										}

										// Finally, set the <currentLeader> to the new pointer

										globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.CurrentLeaderAssumption = targetResponse.ProposedIndexOfLeader

										globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.Unlock()

										time.Sleep(time.Second)

										continue

									} else {

										globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.Unlock()

										time.Sleep(time.Second)

										continue

									}

								}

							}

						}

					}

				}

			}

		}

		globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

		// Add some delay

		time.Sleep(time.Second)

	}

}
