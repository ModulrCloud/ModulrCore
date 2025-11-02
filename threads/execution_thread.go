package threads

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/cryptography"
	"github.com/ModulrCloud/ModulrCore/databases"
	"github.com/ModulrCloud/ModulrCore/handlers"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
	"github.com/ModulrCloud/ModulrCore/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
)

func ExecutionThread() {

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

		epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler

		currentEpochIsFresh := utils.EpochStillFresh(epochHandlerRef)

		shouldMoveToNextEpoch := false

		if epochHandlerRef.LegacyEpochAlignmentData.Activated {

			infoFromAefpAboutLastBlocksByLeaders := epochHandlerRef.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch

			var localExecMetadataForLeader, metadataFromAefpForLeader structures.ExecutionStatsPerLeaderSequence

			dataExists := false

			for {

				indexOfLeaderToExec := epochHandlerRef.LegacyEpochAlignmentData.CurrentLeaderToExecBlocksFrom

				pubKeyOfLeader := epochHandlerRef.EpochDataHandler.LeadersSequence[indexOfLeaderToExec]

				localExecMetadataForLeader = epochHandlerRef.ExecutionData[pubKeyOfLeader]

				metadataFromAefpForLeader, dataExists = infoFromAefpAboutLastBlocksByLeaders[pubKeyOfLeader]

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

					// Leave cycle if no response or no block
					if response == nil || response.Block == nil {
						break
					}

					if localExecMetadataForLeader.Index+1 == metadataFromAefpForLeader.Index && response.Block.GetHash() == metadataFromAefpForLeader.Hash {

						// No need to verify AFP
						executeBlock(response.Block)

						localExecMetadataForLeader = epochHandlerRef.ExecutionData[pubKeyOfLeader]

					} else if response.Afp != nil && utils.VerifyAggregatedFinalizationProof(response.Afp, &epochHandlerRef.EpochDataHandler) {

						// Exec only if AFP is valid
						executeBlock(response.Block)

						localExecMetadataForLeader = epochHandlerRef.ExecutionData[pubKeyOfLeader]

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

			// Take the leader by it's position

			currentEpochAlignmentData := &epochHandlerRef.CurrentEpochAlignmentData

			leaderPubkeyToExecBlocks := epochHandlerRef.EpochDataHandler.LeadersSequence[currentEpochAlignmentData.CurrentLeaderToExecBlocksFrom]

			execStatsOfLeader := epochHandlerRef.ExecutionData[leaderPubkeyToExecBlocks] // {index,hash}

			infoAboutLastBlockByThisLeader, exists := currentEpochAlignmentData.InfoAboutLastBlocksInEpoch[leaderPubkeyToExecBlocks] // {index,hash}

			if exists && execStatsOfLeader.Index == infoAboutLastBlockByThisLeader.Index {

				// Move to the next leader

				epochHandlerRef.CurrentEpochAlignmentData.CurrentLeaderToExecBlocksFrom++

				if !currentEpochIsFresh {

					tryToFinishCurrentEpoch(&epochHandlerRef.EpochDataHandler)

				}

				// Here we need to skip the following logic and start next iteration

				// handlers.EXECUTION_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

				handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

				continue

			}

			// Now, when we have connection with some entity which has an ability to give us blocks via WS(s) tunnel

			// ___________ Now start a cycle to fetch blocks and exec ___________

			for {

				// Try to get the next block + proof and do it until block will be unavailable or we finished with current block creator

				blockId := strconv.Itoa(epochHandlerRef.EpochDataHandler.Id) + ":" + leaderPubkeyToExecBlocks + ":" + strconv.Itoa(execStatsOfLeader.Index+1)

				response := getBlockAndProofFromPoD(blockId)

				// If no data - break
				if response == nil || response.Block == nil {
					break
				}

				if execStatsOfLeader.Index+1 == infoAboutLastBlockByThisLeader.Index && response.Block.GetHash() == infoAboutLastBlockByThisLeader.Hash {

					// Let is exec without AFP
					executeBlock(response.Block)

					execStatsOfLeader = epochHandlerRef.ExecutionData[leaderPubkeyToExecBlocks]

				} else if response.Afp != nil && utils.VerifyAggregatedFinalizationProof(response.Afp, &epochHandlerRef.EpochDataHandler) {

					// Exec only if AFP is valid
					executeBlock(response.Block)

					execStatsOfLeader = epochHandlerRef.ExecutionData[leaderPubkeyToExecBlocks]

				} else {

					break

				}

			}

		}

		if !currentEpochIsFresh && !epochHandlerRef.LegacyEpochAlignmentData.Activated {

			tryToFinishCurrentEpoch(&epochHandlerRef.EpochDataHandler)

		}

		if shouldMoveToNextEpoch {

			setupNextEpoch(&epochHandlerRef.EpochDataHandler)

		}

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	}

}

func getBlockAndProofFromPoD(blockID string) *websocket_pack.WsBlockWithAfpResponse {

	req := websocket_pack.WsBlockWithAfpRequest{
		Route:   "get_block_with_afp",
		BlockId: blockID,
	}

	if reqBytes, err := json.Marshal(req); err == nil {

		if respBytes, err := utils.SendWebsocketMessageToPoD(reqBytes); err == nil {

			var resp websocket_pack.WsBlockWithAfpResponse

			if err := json.Unmarshal(respBytes, &resp); err == nil {

				if resp.Block == nil {

					return nil

				}

				return &resp

			}

		}

	}

	return nil

}

func executeBlock(block *block_pack.Block) {

	epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler

	if epochHandlerRef.ExecutionData[block.Creator].Hash == block.PrevHash {

		currentEpochIndex := epochHandlerRef.EpochDataHandler.Id

		currentBlockId := strconv.Itoa(currentEpochIndex) + ":" + block.Creator + ":" + strconv.Itoa(block.Index)

		// To change the state atomically - prepare the atomic batch

		stateBatch := new(leveldb.Batch)

		blockFees := uint64(0)

		for _, transaction := range block.Transactions {

			_, fee := executeTransaction(&transaction)

			blockFees += fee

		}

		//_____________________________________SHARE FEES AMONG VALIDATOR AND STAKERS__________________________________

		/*

		   Distribute fees among:

		       [0] Block creator itself (validator)
		       [1] Stakers of this validator

		*/

		distributeFeesAmongValidatorAndStakers(block.Creator, blockFees)

		for accountID, accountData := range epochHandlerRef.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				stateBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for validatorPubkey, validatorStorage := range epochHandlerRef.ValidatorsStoragesCache {

			if dataBytes, err := json.Marshal(validatorStorage); err == nil {

				stateBatch.Put([]byte(validatorPubkey), dataBytes)

			} else {

				panic("Impossible to add validator storage to atomic batch")

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

		if err := databases.STATE.Write(stateBatch, nil); err == nil {

			utils.LogWithTime2(fmt.Sprintf("Executed block %s ✅ [%d]", currentBlockId, epochHandlerRef.LastHeight), utils.CYAN_COLOR)

		} else {

			panic("Impossible to commit changes in atomic batch to permanent state")

		}

	}

}

func distributeFeesAmongValidatorAndStakers(blockCreatorPubkey string, feeFromBlock uint64) {

	/*

	   _____________________Here we perform the following logic_____________________

	   [*] feeFromBlock - amount of total fees received in this block

	   1) Get the validator storage to extract list of stakers

	   2) In this list (validatorStorage.stakers) we have structure like:

	       {
	           validatorPubkey:{stake},
	           ...
	           stakerPubkey:{stake}
	           ...
	       }

	   3) Send <validatorStorage.percentage * feeFromBlock> to block creator:

	       validatorCreatorAccount.balance += validatorStorage.percentage * feeFromBlock

	   2) Distribute the rest among other stakers

	       For this, we should:

	           2.1) Go through validatorStorage.stakers

	           2.2) Increase balance - stakerAccount.balance += totalStakerPowerPercentage * restOfFees

	*/

	blockCreatorStorage := utils.GetValidatorFromExecThreadState(blockCreatorPubkey)

	blockCreatorAccount := utils.GetAccountFromExecThreadState(blockCreatorPubkey)

	// 1. Transfer part of fees to account with pubkey associated with block creator

	rewardForBlockCreator := (uint64(blockCreatorStorage.Percentage) * feeFromBlock) / 100

	blockCreatorAccount.Balance += rewardForBlockCreator

	// 2. Share the rest of fees among stakers due to their % part in total stake

	feesToShareAmongStakers := feeFromBlock - rewardForBlockCreator

	if feesToShareAmongStakers != 0 {

		for stakerPubkey, stakerData := range blockCreatorStorage.Stakers {

			stakerReward := (stakerData.Stake * feesToShareAmongStakers) / blockCreatorStorage.TotalStaked

			stakerAccount := utils.GetAccountFromExecThreadState(stakerPubkey)

			stakerAccount.Balance += stakerReward

		}

	}

}

func executeTransaction(tx *structures.Transaction) (bool, uint64) {

	if cryptography.VerifySignature(tx.Hash(), tx.From, tx.Sig) {

		accountFrom := utils.GetAccountFromExecThreadState(tx.From)

		accountTo := utils.GetAccountFromExecThreadState(tx.To)

		totalSpend := tx.Fee + tx.Amount

		if accountFrom.Balance >= totalSpend && tx.Nonce == accountFrom.Nonce+1 {

			accountFrom.Balance -= totalSpend

			accountTo.Balance += tx.Amount

			accountFrom.Nonce++

			return true, tx.Fee

		}

		return false, 0

	}

	return false, 0

}

/*
The following 3 functions are responsible of final sequence alignment before we finish
with epoch X and move to epoch X+1
*/
func findInfoAboutLastBlocks(epochHandler *structures.EpochDataHandler, aefp *structures.AggregatedEpochFinalizationProof) {

	emptyTemplate := make(map[string]structures.ExecutionStatsPerLeaderSequence)

	infoAboutFinalBlocksByLeaders := make(map[string]map[string]structures.ExecutionStatsPerLeaderSequence)

	// Start the cycle in reverse order from <aefp.lastLeader>

	lastLeaderPubkey := epochHandler.LeadersSequence[aefp.LastLeader]

	emptyTemplate[lastLeaderPubkey] = structures.ExecutionStatsPerLeaderSequence{
		Index: int(aefp.LastIndex),
		Hash:  aefp.LastHash,
	}

	infoAboutLastBlocksByPreviousLeader := make(map[string]structures.ExecutionStatsPerLeaderSequence)

	for position := int(aefp.LastLeader); position >= 0; position-- {

		leaderPubKey := epochHandler.LeadersSequence[position]

		// In case we know that leader on this position created 0 block - don't return from function and continue the cycle iterations

		if prev, ok := infoAboutLastBlocksByPreviousLeader[leaderPubKey]; ok && prev.Index == -1 {

			continue

		} else {

			// Get the first block in this epoch by this leader

			firstBlockInThisEpochByLeader := block_pack.GetBlock(epochHandler.Id, leaderPubKey, 0, epochHandler)

			if firstBlockInThisEpochByLeader == nil {

				return

			}

			// In this block we should have ALRPs for all the previous leaders

			alrpChainIsOk, infoAboutFinalBlocks := firstBlockInThisEpochByLeader.VerifyAlrpChainExtended(
				epochHandler, int(position), true,
			)

			if alrpChainIsOk {

				infoAboutFinalBlocksByLeaders[leaderPubKey] = infoAboutFinalBlocks

				infoAboutLastBlocksByPreviousLeader = infoAboutFinalBlocks

			}

		}

	}

	for _, leaderPubkey := range epochHandler.LeadersSequence {

		if finalBlocksData, ok := infoAboutFinalBlocksByLeaders[leaderPubkey]; ok {

			for leaderPub, alrpData := range finalBlocksData {

				if _, exists := emptyTemplate[leaderPub]; !exists {

					emptyTemplate[leaderPub] = alrpData

				}

			}

		}

	}

	handlers.EXECUTION_THREAD_METADATA.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch = emptyTemplate

	/*


		   After execution of this function we have:

		   [0] handlers.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler.LeadersSequence with structure: [Pool0A,Pool1A,....,PoolNA]

		   Using this chains we'll finish the sequence alignment process

		   [1] handlers.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch with structure:

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

			2) Take the data from handlers.EXECUTION_THREAD_METADATA_HANDLER.Handler.LegacyEpochAlignmentData.InfoAboutLastBlocksInEpoch

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

func tryToFinishCurrentEpoch(epochHandler *structures.EpochDataHandler) {

	epochIndex := epochHandler.Id

	nextEpochIndex := epochIndex + 1

	var nextEpochData *structures.NextEpochDataHandler

	rawHandler, dbErr := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochIndex)), nil)

	if dbErr == nil {

		json.Unmarshal(rawHandler, &nextEpochData)

	}

	if nextEpochData != nil {

		// Find the first blocks for epoch X+1

		var firstBlockDataOnNextEpoch structures.FirstBlockDataForNextEpoch

		rawHandler, dbErr := databases.EPOCH_DATA.Get([]byte("FIRST_BLOCKS_IN_NEXT_EPOCH:"+strconv.Itoa(epochIndex)), nil)

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

				databases.EPOCH_DATA.Put([]byte("FIRST_BLOCKS_IN_NEXT_EPOCH:"+strconv.Itoa(epochIndex)), serializedData, nil)

			}

		}

		//____________After we get the first blocks for epoch X+1 - get the AEFP from it and build the data for VT to finish epoch X____________

		firstBlockInThisEpoch := block_pack.GetBlock(nextEpochIndex, firstBlockDataOnNextEpoch.FirstBlockCreator, 0, epochHandler)

		if firstBlockInThisEpoch != nil && firstBlockInThisEpoch.GetHash() == firstBlockDataOnNextEpoch.FirstBlockHash {

			firstBlockDataOnNextEpoch.Aefp = firstBlockInThisEpoch.ExtraData.AefpForPreviousEpoch

		}

		if firstBlockDataOnNextEpoch.Aefp != nil {

			// Activate to start get data from it

			handlers.EXECUTION_THREAD_METADATA.Handler.LegacyEpochAlignmentData.Activated = true

			findInfoAboutLastBlocks(epochHandler, firstBlockDataOnNextEpoch.Aefp)

		}

	}

}

func setupNextEpoch(epochHandler *structures.EpochDataHandler) {

	currentEpochIndex := epochHandler.Id

	nextEpochIndex := currentEpochIndex + 1

	var nextEpochData *structures.NextEpochDataHandler

	// Take from DB

	rawHandler, dbErr := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochIndex)), nil)

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
			Id:                 nextEpochIndex,
			Hash:               nextEpochData.NextEpochHash,
			ValidatorsRegistry: nextEpochData.NextEpochValidatorsRegistry,
			StartTimestamp:     epochHandler.StartTimestamp + uint64(handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.EpochDuration),
			Quorum:             nextEpochData.NextEpochQuorum,
			LeadersSequence:    nextEpochData.NextEpochLeadersSequence,
		}

		handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler = *templateForNextEpoch

		// Nullify values for the upcoming epoch

		handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData = make(map[string]structures.ExecutionStatsPerLeaderSequence)

		for _, validatorPubkey := range handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.LeadersSequence {

			handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[validatorPubkey] = structures.NewExecutionStatsTemplate()

		}

		// Finally, clean useless data

		handlers.EXECUTION_THREAD_METADATA.Handler.CurrentEpochAlignmentData = structures.AlignmentDataHandler{
			Activated:                  true,
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerLeaderSequence),
		}

		handlers.EXECUTION_THREAD_METADATA.Handler.LegacyEpochAlignmentData = structures.AlignmentDataHandler{
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerLeaderSequence),
		}

		// Commit the changes of state using atomic batch. Because we modified state via delayed transactions when epoch finished

		for accountID, accountData := range handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				dbBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for validatorPubkey, validatorStorage := range handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache {

			if dataBytes, err := json.Marshal(validatorStorage); err == nil {

				dbBatch.Put([]byte(validatorPubkey), dataBytes)

			} else {

				panic("Impossible to add validator storage to atomic batch")

			}

		}

		if err := databases.STATE.Write(dbBatch, nil); err != nil {

			panic("Impossible to modify the state when epoch finished")

		}

		// Version check once new epoch started

		if utils.IsMyCoreVersionOld(&handlers.EXECUTION_THREAD_METADATA.Handler) {

			utils.LogWithTime("New version detected on EXECUTION_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

			utils.GracefulShutdown()

		}

	}

}
