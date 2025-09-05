package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/common_functions"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/threads"
	"github.com/ModulrCloud/ModulrCore/utils"
	"github.com/ModulrCloud/ModulrCore/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

func RunBlockchain() {

	prepareBlockchain()

	//_________________________ RUN SEVERAL LOGICAL THREADS _________________________

	//✅ 1.Thread to find AEFPs and change the epoch for AT
	go threads.EpochRotationThread()

	//✅ 2.Share our blocks within quorum members and get the finalization proofs
	go threads.BlocksSharingAndProofsGrabingThread()

	//✅ 3.Thread to propose AEFPs to move to next epoch
	go threads.NewEpochProposerThread()

	//✅ 4.Start to generate blocks
	go threads.BlocksGenerationThread()

	//✅ 5.Start a separate thread to work with voting for blocks in a sync way (for security)
	go threads.LeaderRotationThread()

	//✅ 6.Logical thread to build the temporary sequence of blocks to verify them
	//go threads.SequenceAlignmentThread()

	//✅ 7.Start execution process - take blocks and execute transactions
	//go threads.ExecutionThread()

	//✅ 8.This thread will be responsible to find the first block in each epoch
	go threads.FirstBlockInEpochMonitor()

	//___________________ RUN SERVERS - WEBSOCKET AND HTTP __________________

	// Set the atomic flag to true

	globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

	go websocket_pack.CreateWebsocketServer()

	serverAddr := globals.CONFIGURATION.Interface + ":" + strconv.Itoa(globals.CONFIGURATION.Port)

	utils.LogWithTime(fmt.Sprintf("Server is starting at http://%s ...✅", serverAddr), utils.CYAN_COLOR)

	err := fasthttp.ListenAndServe(serverAddr, NewRouter())

	if err != nil {

		utils.LogWithTime(fmt.Sprintf("Error in server: %s", err), utils.RED_COLOR)

	}

}

func prepareBlockchain() {

	// Create dir for chaindata
	if _, err := os.Stat(globals.CHAINDATA_PATH); os.IsNotExist(err) {

		if err := os.MkdirAll(globals.CHAINDATA_PATH, 0755); err != nil {

			return

		}

	}

	globals.BLOCKS = utils.OpenDb("BLOCKS")
	globals.STATE = utils.OpenDb("STATE")
	globals.EPOCH_DATA = utils.OpenDb("EPOCH_DATA")
	globals.APPROVEMENT_THREAD_METADATA = utils.OpenDb("APPROVEMENT_THREAD_METADATA")
	globals.FINALIZATION_VOTING_STATS = utils.OpenDb("FINALIZATION_VOTING_STATS")

	// Load GT - Generation Thread handler
	if data, err := globals.BLOCKS.Get([]byte("GT"), nil); err == nil {

		var gtHandler structures.GenerationThreadMetadataHandler

		if err := json.Unmarshal(data, &gtHandler); err == nil {

			globals.GENERATION_THREAD_METADATA_HANDLER = gtHandler

		} else {

			fmt.Printf("failed to unmarshal GENERATION_THREAD: %v\n", err)

			return

		}
	} else {

		// Create initial generation thread handler

		globals.GENERATION_THREAD_METADATA_HANDLER = structures.GenerationThreadMetadataHandler{

			EpochFullId: utils.Blake3("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"+globals.GENESIS.NetworkId) + "#-1",
			PrevHash:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			NextIndex:   0,
		}

	}

	// Load AT - Approvement Thread handler

	if data, err := globals.APPROVEMENT_THREAD_METADATA.Get([]byte("AT"), nil); err == nil {

		var atHandler structures.ApprovementThreadMetadataHandler

		if err := json.Unmarshal(data, &atHandler); err == nil {

			if atHandler.Cache == nil {

				atHandler.Cache = make(map[string]*structures.PoolStorage)

			}

			globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler = atHandler

		} else {

			fmt.Printf("failed to unmarshal APPROVEMENT_THREAD: %v\n", err)

			return

		}

	}

	// Load ET - Execution Thread handler

	if data, err := globals.STATE.Get([]byte("ET"), nil); err == nil {

		var etHandler structures.ExecutionThreadMetadataHandler

		if err := json.Unmarshal(data, &etHandler); err == nil {

			if etHandler.AccountsCache == nil {

				etHandler.AccountsCache = make(map[string]*structures.Account)

			}

			if etHandler.PoolsCache == nil {

				etHandler.PoolsCache = make(map[string]*structures.PoolStorage)

			}

			globals.EXECUTION_THREAD_METADATA_HANDLER.Handler = etHandler

		} else {

			fmt.Printf("failed to unmarshal EXECUTION_THREAD: %v\n", err)

			return

		}

	}

	// Init genesis if version is -1
	if globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CoreMajorVersion == -1 {

		setGenesisToState()

		serializedApprovementThread, err := json.Marshal(globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler)

		serializedExecutionThread, err2 := json.Marshal(globals.EXECUTION_THREAD_METADATA_HANDLER.Handler)

		if err != nil || err2 != nil {

			fmt.Printf("failed to marshal handlers: %v\n", err)

			return

		}

		if err := globals.APPROVEMENT_THREAD_METADATA.Put([]byte("AT"), serializedApprovementThread, nil); err != nil {

			fmt.Printf("failed to save APPROVEMENT_THREAD: %v\n", err)

			return

		}

		if err := globals.STATE.Put([]byte("ET"), serializedExecutionThread, nil); err != nil {

			fmt.Printf("failed to save EXECUTION_THREAD: %v\n", err)

			return

		}
	}

	// Version check
	if utils.IsMyCoreVersionOld(&globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler) {

		utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

		utils.GracefulShutdown()

	}

}

func setGenesisToState() error {

	approvementThreadBatch := new(leveldb.Batch)

	execThreadBatch := new(leveldb.Batch)

	epochTimestamp := globals.GENESIS.FirstEpochStartTimestamp

	poolsRegistryForEpochHandler := []string{}

	poolsRegistryForEpochHandler2 := []string{}

	// __________________________________ Load info about accounts __________________________________

	for accountPubkey, accountData := range globals.GENESIS.State {

		serialized, err := json.Marshal(accountData)

		if err != nil {
			return err
		}

		execThreadBatch.Put([]byte(accountPubkey), serialized)

	}

	// __________________________________ Load info about pools __________________________________

	for _, poolStorage := range globals.GENESIS.Pools {

		poolPubKey := poolStorage.Pubkey

		serializedStorage, err := json.Marshal(poolStorage)

		if err != nil {
			return err
		}

		approvementThreadBatch.Put([]byte(poolPubKey+"(POOL)_STORAGE_POOL"), serializedStorage)

		execThreadBatch.Put([]byte(poolPubKey+"(POOL)_STORAGE_POOL"), serializedStorage)

		poolsRegistryForEpochHandler = append(poolsRegistryForEpochHandler, poolPubKey)

		poolsRegistryForEpochHandler2 = append(poolsRegistryForEpochHandler2, poolPubKey)

		globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.ExecutionData[poolPubKey] = structures.NewExecutionStatsTemplate()

	}

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters = structures.CopyNetworkParameters(globals.GENESIS.NetworkParameters)

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters = structures.CopyNetworkParameters(globals.GENESIS.NetworkParameters)

	// Commit changes
	if err := globals.APPROVEMENT_THREAD_METADATA.Write(approvementThreadBatch, nil); err != nil {
		return err
	}

	if err := globals.STATE.Write(execThreadBatch, nil); err != nil {
		return err
	}

	hashInput := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + globals.GENESIS.NetworkId

	initEpochHash := utils.Blake3(hashInput)

	epochHandlerForApprovementThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		PoolsRegistry:      poolsRegistryForEpochHandler,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	epochHandlerForExecThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		PoolsRegistry:      poolsRegistryForEpochHandler2,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	// Assign quorum - pseudorandomly and in deterministic way

	epochHandlerForApprovementThread.Quorum = common_functions.GetCurrentEpochQuorum(&epochHandlerForApprovementThread, globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.NetworkParameters.QuorumSize, initEpochHash)

	epochHandlerForExecThread.Quorum = common_functions.GetCurrentEpochQuorum(&epochHandlerForExecThread, globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.NetworkParameters.QuorumSize, initEpochHash)

	// Now set the block generators for epoch pseudorandomly and in deterministic way

	common_functions.SetLeadersSequence(&epochHandlerForApprovementThread, initEpochHash)

	common_functions.SetLeadersSequence(&epochHandlerForExecThread, initEpochHash)

	globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler = epochHandlerForApprovementThread

	globals.EXECUTION_THREAD_METADATA_HANDLER.Handler.EpochDataHandler = epochHandlerForExecThread

	return nil

}
