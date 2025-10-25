package threads

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/system_contracts"
	"github.com/ModulrCloud/ModulrCore/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

type FirstBlockDataWithAefp struct {
	FirstBlockCreator, FirstBlockHash string

	Aefp *structures.AggregatedEpochFinalizationProof
}

const LATEST_BATCH_KEY = "LATEST_BATCH_INDEX"

var aefpHTTP = &http.Client{Timeout: 2 * time.Second}

var AEFP_AND_FIRST_BLOCK_DATA FirstBlockDataWithAefp

func fetchAefp(ctx context.Context, url string, quorum []string, majority int, epochFullID string, resultCh chan<- *structures.AggregatedEpochFinalizationProof) {

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)

	if err != nil {
		return
	}

	resp, err := aefpHTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return
	}

	var aefp structures.AggregatedEpochFinalizationProof
	if err := json.NewDecoder(resp.Body).Decode(&aefp); err != nil {
		return
	}
	if utils.VerifyAggregatedEpochFinalizationProof(&aefp, quorum, majority, epochFullID) {
		select {
		case resultCh <- &aefp:
		case <-ctx.Done():
		}
	}
}

// Reads latest batch index from LevelDB.
// Supports legacy decimal-string format and migrates it to 8-byte BigEndian.
func readLatestBatchIndex() int64 {

	raw, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte(LATEST_BATCH_KEY), nil)

	if err != nil || len(raw) == 0 {
		return 0
	}

	if len(raw) == 8 {
		return int64(binary.BigEndian.Uint64(raw))
	}

	// Legacy format: decimal string. Try to parse and migrate.
	if v, perr := strconv.ParseInt(string(raw), 10, 64); perr == nil && v >= 0 {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], uint64(v))
		_ = globals.APPROVEMENT_THREAD_METADATA.Put([]byte(LATEST_BATCH_KEY), buf[:], nil)
		return v
	}

	return 0

}

// Writes latest batch index to LevelDB as 8-byte BigEndian.
func writeLatestBatchIndexBatch(batch *leveldb.Batch, v int64) {

	var buf [8]byte

	binary.BigEndian.PutUint64(buf[:], uint64(v))

	batch.Put([]byte(LATEST_BATCH_KEY), buf[:])

}

func ExecuteDelayedTransaction(delayedTransaction map[string]string, contextTag string) {

	if delayedTxType, ok := delayedTransaction["type"]; ok {

		// Now find the handler

		if funcHandler, ok := system_contracts.DELAYED_TRANSACTIONS_MAP[delayedTxType]; ok {

			funcHandler(delayedTransaction, contextTag)

		}

	}

}

func EpochRotationThread() {

	for {

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RLock()

		if !utils.EpochStillFresh(&globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler) {

			epochHandlerRef := &globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler

			epochFullID := epochHandlerRef.Hash + "#" + strconv.Itoa(epochHandlerRef.Id)

			if !utils.SignalAboutEpochRotationExists(epochHandlerRef.Id) {

				// If epoch is not fresh - send the signal to persistent db that we finish it - not to create AFPs, ALRPs anymore
				keyValue := []byte("EPOCH_FINISH:" + strconv.Itoa(epochHandlerRef.Id))

				globals.FINALIZATION_VOTING_STATS.Put(keyValue, []byte("TRUE"), nil)

			}

			if utils.SignalAboutEpochRotationExists(epochHandlerRef.Id) {

				majority := utils.GetQuorumMajority(epochHandlerRef)

				quorumMembers := utils.GetQuorumUrlsAndPubkeys(epochHandlerRef)

				haveEverything := AEFP_AND_FIRST_BLOCK_DATA.Aefp != nil && AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash != ""

				if !haveEverything {

					// 1. Find AEFPs

					if AEFP_AND_FIRST_BLOCK_DATA.Aefp == nil {

						// Try to find locally first

						keyValue := []byte("AEFP:" + strconv.Itoa(epochHandlerRef.Id))

						aefpRaw, err := globals.EPOCH_DATA.Get(keyValue, nil)

						var aefp structures.AggregatedEpochFinalizationProof

						errParse := json.Unmarshal(aefpRaw, &aefp)

						if err == nil && errParse == nil {

							AEFP_AND_FIRST_BLOCK_DATA.Aefp = &aefp

						} else {

							// Ask quorum for AEFP

							resultCh := make(chan *structures.AggregatedEpochFinalizationProof, 1)
							ctx, cancel := context.WithCancel(context.Background())

							for _, quorumMember := range quorumMembers {
								go fetchAefp(ctx, quorumMember.Url, epochHandlerRef.Quorum, majority, epochFullID, resultCh)
							}

							select {

							case value := <-resultCh:
								AEFP_AND_FIRST_BLOCK_DATA.Aefp = value
								cancel()
							case <-time.After(2 * time.Second):
								cancel()
							}
						}
					}

					// 2. Find first block in this epoch
					if AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash == "" {

						firstBlockData := GetFirstBlockDataFromDB(epochHandlerRef.Id)

						if firstBlockData != nil {

							AEFP_AND_FIRST_BLOCK_DATA.FirstBlockCreator = firstBlockData.FirstBlockCreator

							AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash = firstBlockData.FirstBlockHash

						}

					}

				}

				if AEFP_AND_FIRST_BLOCK_DATA.Aefp != nil && AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash != "" {

					// 1. Fetch first block

					firstBlock := block_pack.GetBlock(epochHandlerRef.Id, AEFP_AND_FIRST_BLOCK_DATA.FirstBlockCreator, 0, epochHandlerRef)

					// 2. Compare hashes

					if firstBlock != nil && firstBlock.GetHash() == AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash {

						// 3. Verify that quorum agreed batch of delayed transactions

						latestBatchIndex := readLatestBatchIndex()

						var delayedTransactionsToExecute []map[string]string

						jsonedDelayedTxs, _ := json.Marshal(firstBlock.ExtraData.DelayedTransactionsBatch.DelayedTransactions)

						dataThatShouldBeSigned := "SIG_DELAYED_OPERATIONS:" + strconv.Itoa(epochHandlerRef.Id) + ":" + string(jsonedDelayedTxs)

						okSignatures := 0

						unique := make(map[string]bool)

						quorumMap := make(map[string]bool)

						for _, pk := range epochHandlerRef.Quorum {
							quorumMap[strings.ToLower(pk)] = true
						}

						for signerPubKey, signa := range firstBlock.ExtraData.DelayedTransactionsBatch.Proofs {

							isOK := cryptography.VerifySignature(dataThatShouldBeSigned, signerPubKey, signa)

							loweredPubKey := strings.ToLower(signerPubKey)

							quorumMember := quorumMap[loweredPubKey]

							if isOK && quorumMember && !unique[loweredPubKey] {

								unique[loweredPubKey] = true

								okSignatures++

							}

						}

						// 5. Finally - check if this batch has bigger index than already executed
						// 6. Only in case it's indeed new batch - execute it

						globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

						// Before acquiring .Lock() for modification, disable route reads.
						// This prevents HTTP/WebSocket handlers from calling RLock() during update,
						// avoiding a flood scenario where excessive reads delay the writer.
						// Existing readers will finish normally; new ones are rejected via this flag.

						globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(false)

						globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.Lock()

						if okSignatures >= majority && int64(epochHandlerRef.Id) > latestBatchIndex {

							latestBatchIndex = int64(epochHandlerRef.Id)

							delayedTransactionsToExecute = firstBlock.ExtraData.DelayedTransactionsBatch.DelayedTransactions

						}

						keyBytes := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochHandlerRef.Id))

						valBytes, _ := json.Marshal(epochHandlerRef)

						globals.EPOCH_DATA.Put(keyBytes, valBytes, nil)

						var daoVotingContractCalls, allTheRestContractCalls []map[string]string

						atomicBatch := new(leveldb.Batch)

						for _, delayedTransaction := range delayedTransactionsToExecute {

							if delayedTxType, ok := delayedTransaction["type"]; ok {

								if delayedTxType == "votingAccept" {

									daoVotingContractCalls = append(daoVotingContractCalls, delayedTransaction)

								} else {

									allTheRestContractCalls = append(allTheRestContractCalls, delayedTransaction)

								}

							}

						}

						delayedTransactionsOrderByPriority := append(daoVotingContractCalls, allTheRestContractCalls...)

						// Execute delayed transactions (in context of approvement thread)

						for _, delayedTransaction := range delayedTransactionsOrderByPriority {

							ExecuteDelayedTransaction(delayedTransaction, "APPROVEMENT_THREAD")

						}

						for key, value := range globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache {

							valBytes, _ := json.Marshal(value)

							atomicBatch.Put([]byte(key), valBytes)

						}

						utils.LogWithTime("Delayed txs were executed for epoch on AT: "+epochFullID, utils.GREEN_COLOR)

						//_______________________ Update the values for new epoch _______________________

						// Now, after the execution we can change the epoch id and get the new hash + prepare new temporary object

						nextEpochId := epochHandlerRef.Id + 1

						nextEpochHash := utils.Blake3(AEFP_AND_FIRST_BLOCK_DATA.FirstBlockHash)

						nextEpochQuorumSize := globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.QuorumSize

						nextEpochHandler := structures.EpochDataHandler{
							Id:                 nextEpochId,
							Hash:               nextEpochHash,
							ValidatorsRegistry: epochHandlerRef.ValidatorsRegistry,
							Quorum:             utils.GetCurrentEpochQuorum(epochHandlerRef, nextEpochQuorumSize, nextEpochHash),
							LeadersSequence:    []string{},
							StartTimestamp:     epochHandlerRef.StartTimestamp + uint64(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.EpochDuration),
							CurrentLeaderIndex: 0,
						}

						utils.SetLeadersSequence(&nextEpochHandler, nextEpochHash)

						writeLatestBatchIndexBatch(atomicBatch, latestBatchIndex)

						globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler = nextEpochHandler

						jsonedHandler, _ := json.Marshal(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler)

						atomicBatch.Put([]byte("AT"), jsonedHandler)

						// Clean cache

						clear(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.ValidatorsStoragesCache)

						// Clean in-memory helpful object

						AEFP_AND_FIRST_BLOCK_DATA = FirstBlockDataWithAefp{}

						if batchCommitErr := globals.APPROVEMENT_THREAD_METADATA.Write(atomicBatch, nil); batchCommitErr != nil {

							panic("Error with writing batch to approvement thread db. Try to launch again")

						}

						utils.LogWithTime("Epoch on approvement thread was updated => "+nextEpochHash+"#"+strconv.Itoa(nextEpochId), utils.GREEN_COLOR)

						globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.Unlock()

						// Re-enable route reads after modification is complete.
						// New HTTP/WebSocket handlers can now call RLock() as usual

						globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

						//_______________________Check the version required for the next epoch________________________

						if utils.IsMyCoreVersionOld(&globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler) {

							utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

							utils.GracefulShutdown()

						}

					} else {

						globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

					}

				} else {

					globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

				}

			} else {

				globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

			}

		} else {

			globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

		}

		time.Sleep(200 * time.Millisecond)

	}

}
