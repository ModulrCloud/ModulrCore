package websocket_pack

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/lxzan/gws"
)

// Only one block creator can request proof for block at a choosen period of time T
var BLOCK_CREATOR_REQUEST_MUTEX = sync.Mutex{}

func GetFinalizationProof(parsedRequest WsFinalizationProofRequest, connection *gws.Conn) {

	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	epochHandler := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	epochIndex := epochHandler.Id

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	itsLeader := epochHandler.LeadersSequence[epochHandler.CurrentLeaderIndex] == parsedRequest.Block.Creator

	if itsLeader {

		localVotingDataForLeader := structures.NewLeaderVotingStatTemplate()

		localVotingDataRaw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), nil)

		if err == nil {

			json.Unmarshal(localVotingDataRaw, &localVotingDataForLeader)

		}

		proposedBlockHash := parsedRequest.Block.GetHash()

		itsSameChainSegment := localVotingDataForLeader.Index < int(parsedRequest.Block.Index) || localVotingDataForLeader.Index == int(parsedRequest.Block.Index) && proposedBlockHash == localVotingDataForLeader.Hash && parsedRequest.Block.Epoch == epochFullID

		if itsSameChainSegment {

			proposedBlockId := strconv.Itoa(epochIndex) + ":" + parsedRequest.Block.Creator + ":" + strconv.Itoa(int(parsedRequest.Block.Index))

			previousBlockIndex := int(parsedRequest.Block.Index - 1)

			var futureVotingDataToStore structures.LeaderVotingStat

			positionOfBlockCreatorInLeadersSequence := slices.Index(epochHandler.LeadersSequence, parsedRequest.Block.Creator)

			if parsedRequest.Block.VerifySignature() && !utils.SignalAboutEpochRotationExists(epochIndex) {

				BLOCK_CREATOR_REQUEST_MUTEX.Lock()

				defer BLOCK_CREATOR_REQUEST_MUTEX.Unlock()

				if localVotingDataForLeader.Index == int(parsedRequest.Block.Index) {

					futureVotingDataToStore = localVotingDataForLeader

				} else {

					futureVotingDataToStore = structures.LeaderVotingStat{

						Index: previousBlockIndex,

						Hash: parsedRequest.PreviousBlockAfp.BlockHash,

						Afp: parsedRequest.PreviousBlockAfp,
					}

				}

				if parsedRequest.Block.Index > 0 {

					// This branch related to case when block index is > 0 (so it's not the first block by leader)

					previousBlockId := strconv.Itoa(epochIndex) + ":" + parsedRequest.Block.Creator + ":" + strconv.Itoa(previousBlockIndex)

					// Check if AFP inside related to previous block AFP

					if previousBlockId == parsedRequest.PreviousBlockAfp.BlockId && utils.VerifyAggregatedFinalizationProof(&parsedRequest.PreviousBlockAfp, epochHandler) {

						// In case it's request for the third block, we'll receive AFP for the second block which includes .prevBlockHash field
						// This will be the assumption of hash of the first block in epoch

						if parsedRequest.Block.Index == 2 {

							keyBytes := []byte("FIRST_BLOCK_ASSUMPTION:" + strconv.Itoa(epochIndex))

							_, err := databases.EPOCH_DATA.Get(keyBytes, nil)

							// We need to store first block assumption only in case we don't have it yet

							if err != nil {

								assumption := structures.FirstBlockAssumption{

									IndexOfFirstBlockCreator: positionOfBlockCreatorInLeadersSequence,

									AfpForSecondBlock: parsedRequest.PreviousBlockAfp,
								}

								valBytes, _ := json.Marshal(assumption)

								databases.EPOCH_DATA.Put(keyBytes, valBytes, nil)

							}

						}

					} else {

						return

					}

				}

				// Store the block and return finalization proof

				blockBytes, err := json.Marshal(parsedRequest.Block)

				if err == nil {

					// 1. Store the block

					err = databases.BLOCKS.Put([]byte(proposedBlockId), blockBytes, nil)

					if err == nil {

						afpBytes, err := json.Marshal(parsedRequest.PreviousBlockAfp)

						if err == nil {

							// 2. Store the AFP for previous block

							errStore := databases.EPOCH_DATA.Put([]byte("AFP:"+parsedRequest.PreviousBlockAfp.BlockId), afpBytes, nil)

							votingStatBytes, errParse := json.Marshal(futureVotingDataToStore)

							if errStore == nil && errParse == nil {

								// 3. Store the voting stats

								err := databases.FINALIZATION_VOTING_STATS.Put([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), votingStatBytes, nil)

								if err == nil {

									// Only after we stored the these 3 components = generate signature (finalization proof)

									dataToSign := ""
									prevBlockHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

									if parsedRequest.Block.Index > 0 {

										prevBlockHash = parsedRequest.PreviousBlockAfp.BlockHash

									}

									dataToSign += strings.Join([]string{prevBlockHash, proposedBlockId, proposedBlockHash, epochFullID}, ":")

									response := WsFinalizationProofResponse{
										Voter:             globals.CONFIGURATION.PublicKey,
										FinalizationProof: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
										VotedForHash:      proposedBlockHash,
									}

									jsonResponse, err := json.Marshal(response)

									if err == nil {

										connection.WriteMessage(gws.OpcodeText, jsonResponse)

									}

								}

							}

						}

					}

				}

			}

		}

	}

}

func GetLeaderRotationProof(parsedRequest WsLeaderRotationProofRequest, connection *gws.Conn) {

	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	epochHandler := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	epochIndex := epochHandler.Id

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	leaderToRotate := epochHandler.LeadersSequence[parsedRequest.IndexOfLeaderToRotate]

	if epochHandler.CurrentLeaderIndex > parsedRequest.IndexOfLeaderToRotate {

		localVotingData := structures.NewLeaderVotingStatTemplate()

		localVotingDataRaw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(strconv.Itoa(epochIndex)+":"+leaderToRotate), nil)

		if err == nil {

			json.Unmarshal(localVotingDataRaw, &localVotingData)

		}

		propSkipData := parsedRequest.SkipData

		if localVotingData.Index > propSkipData.Index {

			// Try to return with AFP for the first block

			firstBlockID := strconv.Itoa(epochHandler.Id) + ":" + leaderToRotate + ":0"

			afpForFirstBlockBytes, err := databases.EPOCH_DATA.Get([]byte("AFP:"+firstBlockID), nil)

			if err == nil {

				var afpForFirstBlock structures.AggregatedFinalizationProof

				err := json.Unmarshal(afpForFirstBlockBytes, &afpForFirstBlock)

				if err == nil {

					responseData := WsLeaderRotationProofResponseUpgrade{
						Voter:            globals.CONFIGURATION.PublicKey,
						ForLeaderPubkey:  leaderToRotate,
						Status:           "UPGRADE",
						AfpForFirstBlock: afpForFirstBlock,
						SkipData:         localVotingData,
					}

					jsonResponse, err := json.Marshal(responseData)

					if err == nil {

						connection.WriteMessage(gws.OpcodeText, jsonResponse)

					}

				}

			}

		} else {

			//________________________________________________ Verify the proposed AFP ________________________________________________

			afpIsOk := false

			parts := strings.Split(propSkipData.Afp.BlockId, ":")

			if len(parts) != 3 {
				return
			}

			indexOfBlockInAfp, err := strconv.Atoi(parts[2])

			if err != nil {
				return
			}

			if propSkipData.Index > -1 {

				if propSkipData.Hash == propSkipData.Afp.BlockHash && propSkipData.Index == indexOfBlockInAfp {

					afpIsOk = utils.VerifyAggregatedFinalizationProof(&propSkipData.Afp, epochHandler)

				}

			} else {

				afpIsOk = true
			}

			if afpIsOk {

				dataToSignForLeaderRotation, firstBlockAfpIsOk := "", false

				if parsedRequest.SkipData.Index == -1 {

					dataToSignForLeaderRotation = "LEADER_ROTATION_PROOF:" + leaderToRotate
					dataToSignForLeaderRotation += ":0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:-1"
					dataToSignForLeaderRotation += ":0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:" + epochFullID

					firstBlockAfpIsOk = true

				} else if parsedRequest.SkipData.Index >= 0 {

					blockIdOfFirstBlock := strconv.Itoa(epochIndex) + ":" + leaderToRotate + ":0"

					if parsedRequest.AfpForFirstBlock.BlockId == blockIdOfFirstBlock && utils.VerifyAggregatedFinalizationProof(&parsedRequest.AfpForFirstBlock, epochHandler) {

						firstBlockHash := parsedRequest.AfpForFirstBlock.BlockHash

						dataToSignForLeaderRotation = "LEADER_ROTATION_PROOF:" + leaderToRotate +
							":" + firstBlockHash +
							":" + strconv.Itoa(propSkipData.Index) +
							":" + propSkipData.Hash +
							":" + epochFullID

						firstBlockAfpIsOk = true

					}

				}

				// If proof is ok - generate LRP(leader rotation proof)

				if firstBlockAfpIsOk {

					leaderRotationProofMessage := WsLeaderRotationProofResponseOk{

						Voter: globals.CONFIGURATION.PublicKey,

						ForLeaderPubkey: leaderToRotate,

						Status: "OK",

						Sig: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSignForLeaderRotation),
					}

					jsonResponse, err := json.Marshal(leaderRotationProofMessage)

					if err == nil {

						connection.WriteMessage(gws.OpcodeText, jsonResponse)

					}

				}

			}

		}

	}

}

func GetBlockWithProof(parsedRequest WsBlockWithAfpRequest, connection *gws.Conn) {

	if blockBytes, err := databases.BLOCKS.Get([]byte(parsedRequest.BlockId), nil); err == nil {

		var block block_pack.Block

		if err := json.Unmarshal(blockBytes, &block); err == nil {

			resp := WsBlockWithAfpResponse{&block, nil}

			// Now try to get AFP for block

			parts := strings.Split(parsedRequest.BlockId, ":")

			if len(parts) > 0 {

				last := parts[len(parts)-1]

				if idx, err := strconv.ParseUint(last, 10, 64); err == nil {

					parts[len(parts)-1] = strconv.FormatUint(idx+1, 10)

					nextBlockId := strings.Join(parts, ":")

					// Remark: To make sure block with index X is 100% approved we need to get the AFP for next block

					if afpBytes, err := databases.EPOCH_DATA.Get([]byte("AFP:"+nextBlockId), nil); err == nil {

						var afp structures.AggregatedFinalizationProof

						if err := json.Unmarshal(afpBytes, &afp); err == nil {

							resp.Afp = &afp

						}

					}

				}

			}

			jsonResponse, err := json.Marshal(resp)

			if err == nil {

				connection.WriteMessage(gws.OpcodeText, jsonResponse)

			}

		}

	}

}
