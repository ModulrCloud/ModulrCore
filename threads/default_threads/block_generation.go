package default_threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
	"github.com/syndtr/goleveldb/leveldb"
)

type DoubleMap = map[string]map[string][]byte

type RotationProofCollector struct {
	wsConnMap map[string]*websocket.Conn
	quorum    []string
	majority  int
	timeout   time.Duration
}

var ALRP_METADATA = make(map[string]*structures.AlrpSkeleton) // previousLeaderPubkey => AlrpData

var WEBSOCKET_CONNECTIONS_FOR_ALRP = make(map[string]*websocket.Conn) // quorumMember => websocket connection handler

func BlocksGenerationThread() {

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

		blockTime := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.BlockTime

		generateBlock()

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		time.Sleep(time.Duration(blockTime) * time.Millisecond)

	}

}

func getTransactionsFromMempool() []structures.Transaction {

	globals.MEMPOOL.Mutex.Lock()
	defer globals.MEMPOOL.Mutex.Unlock()

	limit := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.TxLimitPerBlock

	mempoolSize := len(globals.MEMPOOL.Slice)

	if limit > mempoolSize {
		limit = mempoolSize
	}

	transactions := make([]structures.Transaction, limit)

	copy(transactions, globals.MEMPOOL.Slice[:limit])

	globals.MEMPOOL.Slice = globals.MEMPOOL.Slice[limit:]

	return transactions
}

func getBatchOfApprovedDelayedTxsByQuorum(indexOfLeader int) structures.DelayedTransactionsBatch {

	epochHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	prevEpochIndex := epochHandlerRef.Id - 2
	majority := utils.GetQuorumMajority(epochHandlerRef)

	batch := structures.DelayedTransactionsBatch{
		EpochIndex:          prevEpochIndex,
		DelayedTransactions: []map[string]string{},
		Proofs:              map[string]string{},
	}

	if indexOfLeader != 0 {
		return batch
	}

	delayedTxKey := fmt.Sprintf("DELAYED_TRANSACTIONS:%d", prevEpochIndex)
	rawDelayedTxs, err := databases.STATE.Get([]byte(delayedTxKey), nil)
	if err != nil {
		return batch
	}

	var delayedTransactions []map[string]string
	if err := json.Unmarshal(rawDelayedTxs, &delayedTransactions); err != nil {
		return batch
	}

	if len(delayedTransactions) == 0 {
		return batch
	}

	delayedTxHash := utils.Blake3(string(rawDelayedTxs))

	proofs := map[string]string{
		globals.CONFIGURATION.PublicKey: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, delayedTxHash),
	}

	quorumMembers := utils.GetQuorumUrlsAndPubkeys(epochHandlerRef)
	reqBody, err := json.Marshal(map[string]int{"epochIndex": prevEpochIndex})
	if err != nil {
		return batch
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type signatureResult struct {
		pubKey    string
		signature string
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	signaturesChan := make(chan signatureResult, len(quorumMembers))
	var wg sync.WaitGroup

	for _, member := range quorumMembers {
		if member.PubKey == globals.CONFIGURATION.PublicKey {
			continue
		}

		wg.Add(1)
		go func(member structures.QuorumMemberData) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, member.Url+"/delayed_transactions_signature", bytes.NewBuffer(reqBody))
			if err != nil {
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			if err != nil {
				return
			}

			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}

			var signResponse struct {
				Signature string `json:"signature"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&signResponse); err != nil {
				return
			}

			if signResponse.Signature == "" {
				return
			}

			select {
			case signaturesChan <- signatureResult{pubKey: member.PubKey, signature: signResponse.Signature}:
			case <-ctx.Done():
			}
		}(member)
	}

	go func() {
		wg.Wait()
		close(signaturesChan)
	}()

	for signResult := range signaturesChan {
		if _, alreadyAdded := proofs[signResult.pubKey]; alreadyAdded {
			continue
		}

		proofs[signResult.pubKey] = signResult.signature
		if len(proofs) >= majority {
			cancel()
		}
	}

	if len(proofs) < majority {
		return batch
	}

	batch.DelayedTransactions = delayedTransactions
	batch.Proofs = proofs

	return batch

}

func generateBlock() {

	epochHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	if !utils.EpochStillFresh(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

		return

	}

	epochFullID := epochHandlerRef.Hash + "#" + strconv.Itoa(epochHandlerRef.Id)

	epochIndex := epochHandlerRef.Id

	currentLeaderPubKey := epochHandlerRef.LeadersSequence[epochHandlerRef.CurrentLeaderIndex]

	PROOFS_GRABBER_MUTEX.RLock()

	// Safe "if" branch to prevent unnecessary blocks generation

	shouldGenerateBlocks := currentLeaderPubKey == globals.CONFIGURATION.PublicKey && handlers.GENERATION_THREAD_METADATA.NextIndex <= PROOFS_GRABBER.AcceptedIndex+1

	shouldRotateEpochOnGenerationThread := handlers.GENERATION_THREAD_METADATA.EpochFullId != epochFullID

	if shouldGenerateBlocks || shouldRotateEpochOnGenerationThread {

		PROOFS_GRABBER_MUTEX.RUnlock()

		// Check if <epochFullID> is the same in APPROVEMENT_THREAD and in GENERATION_THREAD

		if shouldRotateEpochOnGenerationThread {

			// If new epoch - add the aggregated proof of previous epoch finalization

			if epochIndex != 0 {

				aefpForPreviousEpoch := getAggregatedEpochFinalizationProof(epochHandlerRef)

				if aefpForPreviousEpoch != nil {

					handlers.GENERATION_THREAD_METADATA.AefpForPreviousEpoch = aefpForPreviousEpoch

				} else {

					return

				}

			}

			// Update the index & hash of epoch (by assigning new epoch full ID)

			handlers.GENERATION_THREAD_METADATA.EpochFullId = epochFullID

			// Nullish the index & hash in generation thread for new epoch

			handlers.GENERATION_THREAD_METADATA.PrevHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

			handlers.GENERATION_THREAD_METADATA.NextIndex = 0

			// Nullify values in ALRP map

			ALRP_METADATA = make(map[string]*structures.AlrpSkeleton)

			// Open websocket connections with the quorum of new epoch

			utils.OpenWebsocketConnectionsWithQuorum(epochHandlerRef.Quorum, WEBSOCKET_CONNECTIONS_FOR_ALRP)

		}

		// Safe "if" branch to prevent unnecessary blocks generation
		if !shouldGenerateBlocks {
			return
		}

		extraData := block_pack.ExtraDataToBlock{}

		if handlers.GENERATION_THREAD_METADATA.NextIndex == 0 {

			if epochIndex > 0 {

				if handlers.GENERATION_THREAD_METADATA.AefpForPreviousEpoch != nil {

					extraData.AefpForPreviousEpoch = handlers.GENERATION_THREAD_METADATA.AefpForPreviousEpoch

				} else {

					return

				}

			}

			majority := utils.GetQuorumMajority(epochHandlerRef)

			// Build the template to insert to the extraData of block. Structure is {pool0:ALRP,...,poolN:ALRP}

			myIndexInLeadersSequence := slices.Index(epochHandlerRef.LeadersSequence, globals.CONFIGURATION.PublicKey)

			if myIndexInLeadersSequence > 0 {

				// Get all previous leaders - from zero to <my_position>

				pubKeysOfAllThePreviousLeader := slices.Clone(epochHandlerRef.LeadersSequence[:myIndexInLeadersSequence])

				slices.Reverse(pubKeysOfAllThePreviousLeader)

				previousToMeLeaderPubKey := epochHandlerRef.LeadersSequence[myIndexInLeadersSequence-1]

				extraData.DelayedTransactionsBatch = getBatchOfApprovedDelayedTxsByQuorum(epochHandlerRef.CurrentLeaderIndex)

				//_____________________ Fill the extraData.aggregatedLeadersRotationProofs _____________________

				alrpsForPreviousLeaders := make(map[string]*structures.AggregatedLeaderRotationProof)

				/*

				   Here we need to fill the object with aggregated leader rotation proofs (ALRPs) for all the previous leaders till the leader which was rotated on not-zero height

				   If we can't find all the required ALRPs - skip this iteration to try again later

				*/

				// Add the ALRP for the previous leaders in leaders sequence

				pubkeysOfLeadersToGetAlrps := []string{}

				for _, leaderPubKey := range pubKeysOfAllThePreviousLeader {

					votingFinalizationStatsPerLeader := &structures.LeaderVotingStat{
						Index: -1,
					}

					keyBytes := []byte(strconv.Itoa(epochIndex) + ":" + leaderPubKey)

					if finStatsRaw, err := databases.FINALIZATION_VOTING_STATS.Get(keyBytes, nil); err == nil {

						if jsonErrParse := json.Unmarshal(finStatsRaw, votingFinalizationStatsPerLeader); jsonErrParse == nil {

							proofThatAtLeastFirstBlockWasCreated := votingFinalizationStatsPerLeader.Index >= 0

							// We 100% need ALRP for previous leader
							// But no need in leaders who created at least one block in epoch and it's not our previous leader

							if leaderPubKey != previousToMeLeaderPubKey && proofThatAtLeastFirstBlockWasCreated {

								break

							}

						}

					}

					pubkeysOfLeadersToGetAlrps = append(pubkeysOfLeadersToGetAlrps, leaderPubKey)

				}

				breakedCycle := false

				for _, leaderID := range pubkeysOfLeadersToGetAlrps {

					if possibleAlrp := getAggregatedLeaderRotationProof(majority, epochIndex, leaderID); possibleAlrp != nil {

						alrpsForPreviousLeaders[leaderID] = possibleAlrp

					} else {

						breakedCycle = true // this is a signal that we need to initiate ALRP finding process at least one more time

						break
					}

				}

				if breakedCycle {

					// Now when we have a list of previous leader to get ALRP for them - run it

					collector := RotationProofCollector{
						wsConnMap: WEBSOCKET_CONNECTIONS_FOR_ALRP,
						quorum:    epochHandlerRef.Quorum,
						majority:  majority,
						timeout:   2 * time.Second,
					}

					resultsOfAlrpRequests := collector.alrpForLeadersCollector(context.Background(), pubkeysOfLeadersToGetAlrps, epochHandlerRef)

					// Parse results here and modify the content inside ALRP_METADATA

					for leaderID, validatorsResponses := range resultsOfAlrpRequests {

						if alrpMetadataForPrevLeader, ok := ALRP_METADATA[leaderID]; ok {

							for validatorID, validatorResponse := range validatorsResponses {

								var response structures.ResponseStatus

								if errParse := json.Unmarshal(validatorResponse, &response); errParse == nil {

									if response.Status == "OK" {

										var lrpOk websocket_pack.WsLeaderRotationProofResponseOk

										if errParse := json.Unmarshal(validatorResponse, &lrpOk); errParse == nil {

											dataThatShouldBeSigned := "LEADER_ROTATION_PROOF:" + leaderID

											dataThatShouldBeSigned += ":" + alrpMetadataForPrevLeader.AfpForFirstBlock.BlockHash

											dataThatShouldBeSigned += ":" + strconv.Itoa(alrpMetadataForPrevLeader.SkipData.Index)

											dataThatShouldBeSigned += ":" + alrpMetadataForPrevLeader.SkipData.Hash

											dataThatShouldBeSigned += ":" + epochFullID

											if validatorID == lrpOk.Voter && leaderID == lrpOk.ForLeaderPubkey && cryptography.VerifySignature(dataThatShouldBeSigned, validatorID, lrpOk.Sig) {

												alrpMetadataForPrevLeader.Proofs[validatorID] = lrpOk.Sig

											}

										}

										if len(alrpMetadataForPrevLeader.Proofs) >= majority {

											break

										}

									} else if response.Status == "UPGRADE" {

										var lrpUpgrade websocket_pack.WsLeaderRotationProofResponseUpgrade

										if errParse := json.Unmarshal(validatorResponse, &lrpUpgrade); errParse == nil {

											ourLocalHeightIsLower := alrpMetadataForPrevLeader.SkipData.Index < lrpUpgrade.SkipData.Index

											if ourLocalHeightIsLower {

												blockIdInSkipDataAfp := strconv.Itoa(epochIndex) + ":" + lrpUpgrade.ForLeaderPubkey + ":" + strconv.Itoa(lrpUpgrade.SkipData.Index)

												proposedSkipDataIsValid := lrpUpgrade.SkipData.Hash == lrpUpgrade.SkipData.Afp.BlockHash && blockIdInSkipDataAfp == lrpUpgrade.SkipData.Afp.BlockId && utils.VerifyAggregatedFinalizationProof(&lrpUpgrade.SkipData.Afp, epochHandlerRef)

												firstBlockID := strconv.Itoa(epochIndex) + ":" + lrpUpgrade.ForLeaderPubkey + ":0"

												proposedFirstBlockIsValid := firstBlockID == lrpUpgrade.AfpForFirstBlock.BlockId && utils.VerifyAggregatedFinalizationProof(&lrpUpgrade.AfpForFirstBlock, epochHandlerRef)

												if proposedFirstBlockIsValid && proposedSkipDataIsValid {

													alrpMetadataForPrevLeader.AfpForFirstBlock = lrpUpgrade.AfpForFirstBlock

													alrpMetadataForPrevLeader.SkipData = lrpUpgrade.SkipData

													alrpMetadataForPrevLeader.Proofs = make(map[string]string)

												}

											}

										}

									}

								}

							}

						}

					}

					return

				} else {

					extraData.AggregatedLeadersRotationProofs = alrpsForPreviousLeaders

				}

			}

		}

		extraData.Rest = globals.CONFIGURATION.ExtraDataToBlock

		blockDbAtomicBatch := new(leveldb.Batch)

		blockCandidate := block_pack.NewBlock(getTransactionsFromMempool(), extraData, epochFullID)

		blockHash := blockCandidate.GetHash()

		blockCandidate.SignBlock()

		// BlockID has the following format => epochID(epochIndex):Ed25519_Pubkey:IndexOfBlockInCurrentEpoch

		blockID := strconv.Itoa(epochIndex) + ":" + globals.CONFIGURATION.PublicKey + ":" + strconv.Itoa(blockCandidate.Index)

		utils.LogWithTime("New block generated "+blockID+" (hash: "+blockHash[:8]+"...)", utils.CYAN_COLOR)

		if blockBytes, serializeErr := json.Marshal(blockCandidate); serializeErr == nil {

			handlers.GENERATION_THREAD_METADATA.PrevHash = blockHash

			handlers.GENERATION_THREAD_METADATA.NextIndex++

			if gtBytes, serializeErr2 := json.Marshal(handlers.GENERATION_THREAD_METADATA); serializeErr2 == nil {

				// Store block locally

				blockDbAtomicBatch.Put([]byte(blockID), blockBytes)

				// Update the GENERATION_THREAD after all

				blockDbAtomicBatch.Put([]byte("GT"), gtBytes)

				if err := databases.BLOCKS.Write(blockDbAtomicBatch, nil); err != nil {

					panic("Can't store GT and block candidate")

				}

			}

		}

	} else {

		PROOFS_GRABBER_MUTEX.RUnlock()

	}

}
