package threads

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/system_contracts"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

type FirstBlockData struct {
	FirstBlockCreator, FirstBlockHash string
}

var FIRST_BLOCK_DATA FirstBlockData

// Reads latest batch index from LevelDB.
// Supports legacy decimal-string format and migrates it to 8-byte BigEndian.
func readLatestBatchIndex() int64 {

	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("LATEST_BATCH_INDEX"), nil)

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
		_ = databases.APPROVEMENT_THREAD_METADATA.Put([]byte("LATEST_BATCH_INDEX"), buf[:], nil)
		return v
	}

	return 0

}

// Writes latest batch index to LevelDB as 8-byte BigEndian.
func writeLatestBatchIndexBatch(batch *leveldb.Batch, v int64) {

	var buf [8]byte

	binary.BigEndian.PutUint64(buf[:], uint64(v))

	batch.Put([]byte("LATEST_BATCH_INDEX"), buf[:])

}

func getFirstBlockDataFromDB(epochIndex int) *FirstBlockData {
	// TBD
	// FIRST_BLOCK_DATA: key
	return nil
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

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

		if !utils.EpochStillFresh(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

			epochHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

			epochFullID := epochHandlerRef.Hash + "#" + strconv.Itoa(epochHandlerRef.Id)

			if !utils.SignalAboutEpochRotationExists(epochHandlerRef.Id) {

				// If epoch is not fresh - send the signal to persistent db that we finish it - not to create AFPs, ALFPs anymore
				keyValue := []byte("EPOCH_FINISH:" + strconv.Itoa(epochHandlerRef.Id))

				databases.FINALIZATION_VOTING_STATS.Put(keyValue, []byte("TRUE"), nil)

			}

			if utils.SignalAboutEpochRotationExists(epochHandlerRef.Id) {

				majority := utils.GetQuorumMajority(epochHandlerRef)

				haveFirstBlockData := FIRST_BLOCK_DATA.FirstBlockHash != ""

				if !haveFirstBlockData {

					// Find first block in this epoch
					if FIRST_BLOCK_DATA.FirstBlockHash == "" {

						firstBlockData := getFirstBlockDataFromDB(epochHandlerRef.Id)

						if firstBlockData != nil {

							FIRST_BLOCK_DATA.FirstBlockCreator = firstBlockData.FirstBlockCreator

							FIRST_BLOCK_DATA.FirstBlockHash = firstBlockData.FirstBlockHash

						}

					}

				}

				if FIRST_BLOCK_DATA.FirstBlockHash != "" {

					// 1. Fetch first block

					firstBlock := block_pack.GetBlock(epochHandlerRef.Id, FIRST_BLOCK_DATA.FirstBlockCreator, 0, epochHandlerRef)

					// 2. Compare hashes

					if firstBlock != nil && firstBlock.GetHash() == FIRST_BLOCK_DATA.FirstBlockHash {

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

						handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

						// Before acquiring .Lock() for modification, disable route reads.
						// This prevents HTTP/WebSocket handlers from calling RLock() during update,
						// avoiding a flood scenario where excessive reads delay the writer.
						// Existing readers will finish normally; new ones are rejected via this flag.

						globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(false)

						handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()

						if okSignatures >= majority && int64(epochHandlerRef.Id) > latestBatchIndex {

							latestBatchIndex = int64(epochHandlerRef.Id)

							delayedTransactionsToExecute = firstBlock.ExtraData.DelayedTransactionsBatch.DelayedTransactions

						}

						keyBytes := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochHandlerRef.Id))

						valBytes, marshalErr := json.Marshal(epochHandlerRef)

						if marshalErr != nil {

							handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

							globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

							panic(fmt.Sprintf("failed to marshal epoch handler: %v", marshalErr))

						}

						if err := databases.EPOCH_DATA.Put(keyBytes, valBytes, nil); err != nil {

							handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

							globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

							panic(fmt.Sprintf("failed to store epoch handler: %v", err))

						}

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

						for key, value := range handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache {

							valBytes, _ := json.Marshal(value)

							atomicBatch.Put([]byte(key), valBytes)

						}

						utils.LogWithTime("Delayed txs were executed for epoch on AT: "+epochFullID, utils.GREEN_COLOR)

						//_______________________ Update the values for new epoch _______________________

						// Now, after the execution we can change the epoch id and get the new hash + prepare new temporary object

						nextEpochId := epochHandlerRef.Id + 1

						nextEpochHash := utils.Blake3(FIRST_BLOCK_DATA.FirstBlockHash)

						nextEpochQuorumSize := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize

						nextEpochHandler := structures.EpochDataHandler{
							Id:                 nextEpochId,
							Hash:               nextEpochHash,
							ValidatorsRegistry: epochHandlerRef.ValidatorsRegistry,
							Quorum:             utils.GetCurrentEpochQuorum(epochHandlerRef, nextEpochQuorumSize, nextEpochHash),
							LeadersSequence:    []string{},
							StartTimestamp:     epochHandlerRef.StartTimestamp + uint64(handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.EpochDuration),
							CurrentLeaderIndex: 0,
						}

						utils.SetLeadersSequence(&nextEpochHandler, nextEpochHash)

						nextEpochDataHandler := structures.NextEpochDataHandler{
							NextEpochHash:               nextEpochHash,
							NextEpochValidatorsRegistry: nextEpochHandler.ValidatorsRegistry,
							NextEpochQuorum:             nextEpochHandler.Quorum,
							NextEpochLeadersSequence:    nextEpochHandler.LeadersSequence,
							DelayedTransactions:         delayedTransactionsOrderByPriority,
						}

						jsonedNextEpochDataHandler, _ := json.Marshal(nextEpochDataHandler)

						atomicBatch.Put([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochId)), jsonedNextEpochDataHandler)

						writeLatestBatchIndexBatch(atomicBatch, latestBatchIndex)

						// Finally - assign new handler

						handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = nextEpochHandler

						// And commit all the changes on AT as a single atomic batch

						jsonedHandler, _ := json.Marshal(handlers.APPROVEMENT_THREAD_METADATA.Handler)

						atomicBatch.Put([]byte("AT"), jsonedHandler)

						// Clean cache

						clear(handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache)

						// Clean in-memory helpful object

						FIRST_BLOCK_DATA = FirstBlockData{}

						if batchCommitErr := databases.APPROVEMENT_THREAD_METADATA.Write(atomicBatch, nil); batchCommitErr != nil {

							panic("Error with writing batch to approvement thread db. Try to launch again")

						}

						utils.LogWithTime("Epoch on approvement thread was updated => "+nextEpochHash+"#"+strconv.Itoa(nextEpochId), utils.GREEN_COLOR)

						handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

						// Re-enable route reads after modification is complete.
						// New HTTP/WebSocket handlers can now call RLock() as usual

						globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

						//_______________________Check the version required for the next epoch________________________

						if utils.IsMyCoreVersionOld(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

							utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

							utils.GracefulShutdown()

						}

					} else {

						handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

					}

				} else {

					handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

				}

			} else {

				handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

			}

		} else {

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		}

		time.Sleep(200 * time.Millisecond)

	}

}
