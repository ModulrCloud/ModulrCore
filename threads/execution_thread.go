package threads

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/block"
	"github.com/ModulrCloud/ModulrCore/common_functions"
	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
	"github.com/ModulrCloud/ModulrCore/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
)

var FEES_COLLECTOR uint = 0

func getBlockAndProofFromPoD(blockID string) *websocket_pack.WsBlockWithAfpResponse {

	request := websocket_pack.WsBlockWithAfpRequest{BlockId: blockID}

	var response websocket_pack.WsBlockWithAfpResponse

	if requestBytes, err := json.Marshal(request); err == nil {

		if responseBytes, err := utils.SendWebsocketMessageToPoD(requestBytes); err == nil {

			if err := json.Unmarshal(responseBytes, &response); err == nil {

				return &response

			}

		}

	}

	return &response

}

func GetAccountFromExecThreadState(accountId string) *structures.Account {

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(accountId), nil)

	if err != nil {
		return nil
	}

	var account structures.Account

	err = json.Unmarshal(data, &account)

	if err != nil {
		return nil
	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache[accountId] = &account

	return &account

}

func GetPoolFromExecThreadState(poolId string) *structures.PoolStorage {

	if val, ok := globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.PoolsCache[poolId]; ok {
		return val
	}

	data, err := globals.STATE.Get([]byte(poolId), nil)

	if err != nil {
		return nil
	}

	var poolStorage structures.PoolStorage

	err = json.Unmarshal(data, &poolStorage)

	if err != nil {
		return nil
	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.PoolsCache[poolId] = &poolStorage

	return &poolStorage

}

func ExecutionThread() {

	for {

		globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RLock()

		epochHandlerRef := &globals.EXECUTION_THREAD_METADATA_HANDLER.Handler

		currentEpochIsFresh := utils.EpochStillFresh(epochHandlerRef)

		shouldMoveToNextEpoch := false

		if epochHandlerRef.LegacyEpochAlignmentData.Activated {

			infoFromAefpAboutLastBlocksByPools := epochHandlerRef.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch

			var localExecMetadataForLeader, metadataFromAefpForLeader structures.ExecutionStatsPerPool

			dataExists := false

			for {

				indexOfLeaderToExec := epochHandlerRef.LegacyEpochAlignmentData.CurrentLeaderToExecBlocksFrom

				pubKeyOfLeader := epochHandlerRef.EpochDataHandler.LeadersSequence[indexOfLeaderToExec]

				localExecMetadataForLeader = epochHandlerRef.ExecutionData[pubKeyOfLeader]

				metadataFromAefpForLeader, dataExists = infoFromAefpAboutLastBlocksByPools[pubKeyOfLeader]

				if !dataExists {

					metadataFromAefpForLeader = structures.NewExecutionStatsTemplate()

				}

				finishedToExecBlocksByThisLeader := localExecMetadataForLeader.Index == metadataFromAefpForLeader.Index

				if finishedToExecBlocksByThisLeader {

					itsTheLastLeaderInSequence := len(epochHandlerRef.EpochDataHandler.LeadersSequence) == indexOfLeaderToExec+1

					if itsTheLastLeaderInSequence {

						break

					} else {

						epochHandlerRef.LegacyEpochAlignmentData.CurrentLeaderToExecBlocksFrom++

						continue

					}

				}

				// ___________ Now start a cycle to fetch blocks and exec ___________

				for {

					// Try to get the next block + proof and do it until block will be unavailable or we finished with current block creator

					blockId := strconv.Itoa(epochHandlerRef.EpochDataHandler.Id) + ":" + pubKeyOfLeader + ":" + strconv.Itoa(localExecMetadataForLeader.Index+1)

					response := getBlockAndProofFromPoD(blockId)

					if response != nil {

						if localExecMetadataForLeader.Index+1 == metadataFromAefpForLeader.Index && response.Block.GetHash() == metadataFromAefpForLeader.Hash {

							// Let it execute without AFP verification

							ExecuteBlock(response.Block)

						} else if common_functions.VerifyAggregatedFinalizationProof(response.Afp, &epochHandlerRef.EpochDataHandler) {

							ExecuteBlock(response.Block)

						} else {

							break

						}

					} else {

						break

					}

				}

			}

			allBlocksWereExecutedInLegacyEpoch := len(epochHandlerRef.EpochDataHandler.LeadersSequence) == epochHandlerRef.LegacyEpochAlignmentData.CurrentLeaderToExecBlocksFrom+1

			finishedToExecBlocksByLastLeader := localExecMetadataForLeader.Index == metadataFromAefpForLeader.Index

			if allBlocksWereExecutedInLegacyEpoch && finishedToExecBlocksByLastLeader {

				shouldMoveToNextEpoch = true
			}

		} else if currentEpochIsFresh && epochHandlerRef.CurrentEpochAlignmentData.Activated {

			// Take the pool by it's position

			currentEpochAlignmentData := &epochHandlerRef.CurrentEpochAlignmentData

			leaderPubkeyToExecBlocks := epochHandlerRef.EpochDataHandler.LeadersSequence[currentEpochAlignmentData.CurrentLeaderToExecBlocksFrom]

			execStatsOfLeader := epochHandlerRef.ExecutionData[leaderPubkeyToExecBlocks] // {index,hash}

			infoAboutLastBlockByThisLeader, exists := currentEpochAlignmentData.InfoAboutLastBlocksInEpoch[leaderPubkeyToExecBlocks] // {index,hash}

			if exists && execStatsOfLeader.Index == infoAboutLastBlockByThisLeader.Index {

				// Move to the next leader

				epochHandlerRef.CurrentEpochAlignmentData.CurrentLeaderToExecBlocksFrom++

				if !currentEpochIsFresh {

					TryToFinishCurrentEpoch(&epochHandlerRef.EpochDataHandler)

				}

				// Here we need to skip the following logic and start next iteration

				globals.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

				continue

			}

			// Now, when we have connection with some entity which has an ability to give us blocks via WS(s) tunnel

			// ___________ Now start a cycle to fetch blocks and exec ___________

			for {

				// Try to get the next block + proof and do it until block will be unavailable or we finished with current block creator

				blockId := strconv.Itoa(epochHandlerRef.EpochDataHandler.Id) + ":" + leaderPubkeyToExecBlocks + ":" + strconv.Itoa(execStatsOfLeader.Index+1)

				response := getBlockAndProofFromPoD(blockId)

				if response != nil {

					if execStatsOfLeader.Index+1 == infoAboutLastBlockByThisLeader.Index && response.Block.GetHash() == infoAboutLastBlockByThisLeader.Hash {

						// Let it execute without AFP verification

						ExecuteBlock(response.Block)

					} else if common_functions.VerifyAggregatedFinalizationProof(response.Afp, &epochHandlerRef.EpochDataHandler) {

						ExecuteBlock(response.Block)

					} else {

						break

					}

				} else {

					break

				}

			}

		}

		if !currentEpochIsFresh && !epochHandlerRef.LegacyEpochAlignmentData.Activated {

			TryToFinishCurrentEpoch(&epochHandlerRef.EpochDataHandler)

		}

		if shouldMoveToNextEpoch {

			SetupNextEpoch(&epochHandlerRef.EpochDataHandler)

		}

	}

}

func ExecuteBlock(block *block.Block) {

	epochHandlerRef := &globals.EXECUTION_THREAD_METADATA_HANDLER.Handler

	if epochHandlerRef.ExecutionData[block.Creator].Hash == block.PrevHash {

		currentEpochIndex := epochHandlerRef.EpochDataHandler.Id

		currentBlockId := strconv.Itoa(currentEpochIndex) + ":" + block.Creator + ":" + strconv.Itoa(block.Index)

		// To change the state atomically - prepare the atomic batch

		stateBatch := new(leveldb.Batch)

		for _, transaction := range block.Transactions {

			ExecuteTransaction(&transaction)

		}

		//_____________________________________SHARE FEES AMONG POOL OWNER AND STAKERS__________________________________

		/*

		   Distribute fees among:

		       [0] Block creator itself
		       [1] Stakers of his pool

		*/

		DistributeFeesAmongStakersAndPool(block.Creator, 0)

		for accountID, accountData := range epochHandlerRef.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				stateBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for poolID, poolStorage := range epochHandlerRef.PoolsCache {

			if dataBytes, err := json.Marshal(poolStorage); err == nil {

				stateBatch.Put([]byte(poolID), dataBytes)

			} else {

				panic("Impossible to add pool data to atomic batch")

			}

		}

		// Update the execution data for progress

		blockHash := block.GetHash()

		blockCreatorData := epochHandlerRef.ExecutionData[block.Creator]

		blockCreatorData.Index = block.Index

		blockCreatorData.Hash = blockHash

		epochHandlerRef.ExecutionData[block.Creator] = blockCreatorData

		// Finally set the updated execution thread handler to atomic batch

		epochHandlerRef.LastHeight++

		epochHandlerRef.LastBlockHash = blockHash

		if execThreadRawBytes, err := json.Marshal(epochHandlerRef); err == nil {

			stateBatch.Put([]byte("ET"), execThreadRawBytes)

		} else {

			panic("Impossible to store updated execution thread version to atomic batch")

		}

		if err := globals.STATE.Write(stateBatch, nil); err == nil {

			utils.LogWithTime(fmt.Sprintf("Executed block %s ✅", currentBlockId), utils.CYAN_COLOR)

		} else {

			panic("Impossible to commit changes in atomic batch to permanent state")

		}

	}

}

func DistributeFeesAmongStakersAndPool(blockCreator string, totalFee uint64) {

	// Stub
}

func ExecuteTransaction(tx *structures.Transaction) {

	if cryptography.VerifySignature(tx.Hash(), tx.From, tx.Sig) {

		// totalSpend := tx.Fee + tx.Amount

		// FEES_COLLECTOR += tx.Fee

	}

}

/*
The following 3 functions are responsible of final sequence alignment before we finish
with epoch X and move to epoch X+1
*/
func FindInfoAboutLastBlocks(epochHandler *structures.EpochDataHandler, aefp *structures.AggregatedEpochFinalizationProof) {

	emptyTemplate := make(map[string]structures.ExecutionStatsPerPool)

	infoAboutFinalBlocksByPool := make(map[string]map[string]structures.ExecutionStatsPerPool)

	// Start the cycle in reverse order from <aefp.lastLeader>

	lastLeaderPubkey := epochHandler.LeadersSequence[aefp.LastLeader]

	emptyTemplate[lastLeaderPubkey] = structures.ExecutionStatsPerPool{
		Index: int(aefp.LastIndex),
		Hash:  aefp.LastHash,
	}

	infoAboutLastBlocksByPreviousPool := make(map[string]structures.ExecutionStatsPerPool)

	for position := aefp.LastLeader; position > 0; position-- {

		leaderPubKey := epochHandler.LeadersSequence[position]

		// In case we know that pool on this position created 0 block - don't return from function and continue the cycle iterations

		if infoAboutLastBlocksByPreviousPool[leaderPubKey].Index == -1 {

			continue

		} else {

			// Get the first block in this epoch by this pool

			firstBlockInThisEpochByPool := common_functions.GetBlock(epochHandler.Id, leaderPubKey, 0, epochHandler)

			if firstBlockInThisEpochByPool == nil {

				return

			}

			// In this block we should have ALRPs for all the previous pools

			alrpChainIsOk, infoAboutFinalBlocks := common_functions.ExtendedCheckAlrpChainValidity(
				firstBlockInThisEpochByPool, epochHandler, int(position), true,
			)

			if alrpChainIsOk {

				infoAboutFinalBlocksByPool[leaderPubKey] = infoAboutFinalBlocks

				infoAboutLastBlocksByPreviousPool = infoAboutFinalBlocks

			}

		}

	}

	for _, poolPubKey := range epochHandler.LeadersSequence {

		if finalBlocksData, ok := infoAboutFinalBlocksByPool[poolPubKey]; ok {

			for poolPub, alrpData := range finalBlocksData {

				if _, exists := emptyTemplate[poolPub]; !exists {

					emptyTemplate[poolPub] = alrpData

				}

			}

		}

	}

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch = emptyTemplate

	/*


		   After execution of this function we have:

		   [0] globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.LeadersSequence with structure: [Pool0A,Pool1A,....,PoolNA]

		   Using this chains we'll finish the sequence alignment process

		   [1] globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch with structure:

		   {

		       Pool0A:{index,hash},
		       Pool1A:{index,hash},
		       ....,
		       PoolNA:{index,hash}

		   }

		   ___________________________________ So ___________________________________

		   Using the order in LeadersSequence - finish the execution based on index:hash pairs

		   Example:

		   	1) We have LeadersSequence: [Validator12, Validator3 , Validator7]

			2) Take the data from globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch

			3) Imagine it looks like this:

			{

		       Validator12:{index:14,hash:"0xaaaa"},
		       Validator7:{index:35,hash:"0xbbbb"},
		       ....,
		       Validator3:{index:10,hash:"0xcccc"}

		    }

			4) Using data about last block height and its hash - complete the execution process in this sequence:

			1. Validator 12 - execute untill index=14
			2. Validator3 - execute untill index=35
			3. Validator7 - execute untill index=10

			Exec untill block 14 of Validator12, then move to blocks by Validator3 - execute from 0 to 35
			Finally move to Validator7 and execute from index 0 to 10


	*/

}

func TryToFinishCurrentEpoch(epochHandler *structures.EpochDataHandler) {

	epochIndex := epochHandler.Id

	nextEpochIndex := epochIndex + 1

	var nextEpochData *structures.NextEpochDataHandler

	rawHandler, dbErr := globals.EPOCH_DATA.Get([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochIndex)), nil)

	if dbErr == nil {

		json.Unmarshal(rawHandler, &nextEpochData)

	}

	if nextEpochData != nil {

		// Find the first blocks for epoch X+1

		var firstBlockDataOnNextEpoch structures.FirstBlockDataForNextEpoch

		rawHandler, dbErr := globals.EPOCH_DATA.Get([]byte("FIRST_BLOCKS_IN_NEXT_EPOCH:"+strconv.Itoa(epochIndex)), nil)

		if dbErr == nil {

			json.Unmarshal(rawHandler, &firstBlockDataOnNextEpoch)

		}

		if firstBlockDataOnNextEpoch.FirstBlockCreator == "" {

			findResult := GetFirstBlockDataFromDB(nextEpochIndex)

			if findResult != nil {

				firstBlockDataOnNextEpoch.FirstBlockCreator = findResult.FirstBlockCreator

				firstBlockDataOnNextEpoch.FirstBlockHash = findResult.FirstBlockHash

			}

			// Store the info about first blocks on next epoch

			serializedData, serialErr := json.Marshal(firstBlockDataOnNextEpoch)

			if serialErr == nil {

				globals.EPOCH_DATA.Put([]byte("FIRST_BLOCKS_IN_NEXT_EPOCH:"+strconv.Itoa(epochIndex)), serializedData, nil)

			}

		}

		//____________After we get the first blocks for epoch X+1 - get the AEFP from it and build the data for VT to finish epoch X____________

		firstBlockInThisEpoch := common_functions.GetBlock(nextEpochIndex, firstBlockDataOnNextEpoch.FirstBlockCreator, 0, epochHandler)

		if firstBlockInThisEpoch != nil && firstBlockInThisEpoch.GetHash() == firstBlockDataOnNextEpoch.FirstBlockHash {

			firstBlockDataOnNextEpoch.Aefp = firstBlockInThisEpoch.ExtraData.AefpForPreviousEpoch

		}

		if firstBlockDataOnNextEpoch.Aefp != nil {

			// Activate to start get data from it

			globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.Activated = true

			FindInfoAboutLastBlocks(epochHandler, firstBlockDataOnNextEpoch.Aefp)

		}

	}

}

func SetupNextEpoch(epochHandler *structures.EpochDataHandler) {

	currentEpochIndex := epochHandler.Id

	nextEpochIndex := currentEpochIndex + 1

	var nextEpochData *structures.NextEpochDataHandler

	// Take from DB

	rawHandler, dbErr := globals.EPOCH_DATA.Get([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochIndex)), nil)

	if dbErr == nil {

		json.Unmarshal(rawHandler, &nextEpochData)

	}

	if nextEpochData != nil {

		dbBatch := new(leveldb.Batch)

		// Exec delayed txs here

		for _, delayedTx := range nextEpochData.DelayedTransactions {

			ExecuteDelayedTransaction(delayedTx, "EXECUTION_THREAD")

		}

		// Prepare epoch handler for next epoch

		templateForNextEpoch := &structures.EpochDataHandler{
			Id:              nextEpochIndex,
			Hash:            nextEpochData.NextEpochHash,
			PoolsRegistry:   nextEpochData.NextEpochPoolsRegistry,
			StartTimestamp:  epochHandler.StartTimestamp + uint64(globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters.EpochTime),
			Quorum:          nextEpochData.NextEpochQuorum,
			LeadersSequence: nextEpochData.NextEpochLeadersSequence,
		}

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler = *templateForNextEpoch

		// Nullify values for the upcoming epoch

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ExecutionData = make(map[string]structures.ExecutionStatsPerPool)

		for _, poolPubkey := range globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.PoolsRegistry {

			globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ExecutionData[poolPubkey] = structures.NewExecutionStatsTemplate()

		}

		// Finally, clean useless data

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CurrentEpochAlignmentData = structures.AlignmentDataHandler{
			Activated:                  true,
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerPool),
		}

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData = structures.AlignmentDataHandler{
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerPool),
		}

		// Commit the changes of state using atomic batch. Because we modified state via delayed transactions when epoch finished

		for accountID, accountData := range globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				dbBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for poolID, poolStorage := range globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.PoolsCache {

			if dataBytes, err := json.Marshal(poolStorage); err == nil {

				dbBatch.Put([]byte(poolID), dataBytes)

			} else {

				panic("Impossible to add pool data to atomic batch")

			}

		}

		if err := globals.STATE.Write(dbBatch, nil); err != nil {

			panic("Impossible to modify the state when epoch finished")

		}

		// Version check once new epoch started

		if utils.IsMyCoreVersionOld(&globals.EXECUTION_THREAD_METADATA_HANDLER.Handler) {

			utils.LogWithTime("New version detected on EXECUTION_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

			utils.GracefulShutdown()

		}

	}

}
