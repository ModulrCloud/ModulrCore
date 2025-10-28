package threads

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
)

type AlignmentDataResponse struct {
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

		resp, err := client.Get(randomTarget.Url + "/sequence_alignment")

		// Network error or timeout
		if err != nil {

			globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

			time.Sleep(time.Second)

			continue

		}

		// Non-200 response, close immediately
		if resp.StatusCode != http.StatusOK {

			resp.Body.Close()

			globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

			time.Sleep(time.Second)

			continue

		}

		var targetResponse AlignmentDataResponse

		// Decode JSON response
		dec := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)) // 10 MiB limit

		if err := dec.Decode(&targetResponse); err != nil {

			resp.Body.Close()

			globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

			time.Sleep(time.Second)

			continue

		}

		// Body successfully decoded, safe to close now
		resp.Body.Close()

		proposedLeaderIndexIsValid := localVersionOfCurrentLeader < targetResponse.ProposedIndexOfLeader && targetResponse.ProposedIndexOfLeader < len(epochHandlerRef.LeadersSequence)

		if proposedLeaderIndexIsValid && targetResponse.FirstBlockByCurrentLeader.Index == 0 {

			// Verify the AFP for second block(with index 1 in epoch) to make sure that block 0(first block in epoch) was 100% accepted

			afp := &targetResponse.AfpForSecondBlockByCurrentLeader
			firstBlock := &targetResponse.FirstBlockByCurrentLeader
			proposedIndex := targetResponse.ProposedIndexOfLeader

			sameHashAndValidAfp := afp.PrevBlockHash == firstBlock.GetHash() && utils.VerifyAggregatedFinalizationProof(afp, epochHandlerRef)

			if sameHashAndValidAfp {

				// Verify all the ALRPs in block header
				if epochHandlerRef.LeadersSequence[proposedIndex] == firstBlock.Creator {

					isOk, infoAboutFinalBlocks := firstBlock.VerifyAlrpChainExtended(epochHandlerRef, proposedIndex, true)

					shouldChange := true

					if isOk {

						collectionOfAlrpsFromAllThePreviousLeaders := []map[string]structures.ExecutionStatsPerLeaderSequence{infoAboutFinalBlocks} // each element here is object like {pool:{index,hash,firstBlockHash}}

						currentAlrpSet := map[string]structures.ExecutionStatsPerLeaderSequence{}

						for leaderPubkey, execStats := range infoAboutFinalBlocks {

							currentAlrpSet[leaderPubkey] = structures.ExecutionStatsPerLeaderSequence{
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

									leaderOnThisPosition := epochHandlerRef.LeadersSequence[position]

									alrpForThisLeaderFromCurrentSet, dataExists := currentAlrpSet[leaderOnThisPosition]

									if dataExists && alrpForThisLeaderFromCurrentSet.Index != -1 {

										// Ask the first block and extract next set of ALRPs
										firstBlockInThisEpochByLeader := block_pack.GetBlock(epochHandlerRef.Id, leaderOnThisPosition, 0, epochHandlerRef)

										if firstBlockInThisEpochByLeader != nil && firstBlockInThisEpochByLeader.GetHash() == alrpForThisLeaderFromCurrentSet.FirstBlockHash {

											alrpChainValidationOk, dataAboutLastBlocks := false, make(map[string]structures.ExecutionStatsPerLeaderSequence)

											if position == 0 {
												alrpChainValidationOk = true
											} else {
												alrpChainValidationOk, dataAboutLastBlocks = firstBlockInThisEpochByLeader.VerifyAlrpChainExtended(
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

									for _, leaderExecStats := range collectionOfAlrpsFromAllThePreviousLeaders {

										// collectionOfAlrpsFromAllThePreviousLeaders[i] = {pool0:{index,hash},poolN:{index,hash}}

										for leaderPubKey, leaderExecData := range leaderExecStats {

											_, dataAlreadyExists := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.InfoAboutLastBlocksInEpoch[leaderPubKey]

											if !dataAlreadyExists {

												globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.InfoAboutLastBlocksInEpoch[leaderPubKey] = leaderExecData

												utils.LogWithTime2("Resolved last block index for "+utils.CYAN_COLOR+leaderPubKey, utils.DEEP_GRAY)

											}

										}

									}

									// Finally, set the <currentLeader> to the new pointer

									globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData.CurrentLeaderAssumption = targetResponse.ProposedIndexOfLeader

									leaderPubkey := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.LeadersSequence[targetResponse.ProposedIndexOfLeader]

									utils.LogWithTime2("New leader on exec thread detected "+utils.CYAN_COLOR+leaderPubkey, utils.GREEN_COLOR)

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

		globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

		// Add some delay before next alignment request
		time.Sleep(time.Second)
	}
}
