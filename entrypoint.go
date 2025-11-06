package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/ModulrCloud/ModulrCore/databases"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/handlers"
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

	//✅ 6.This thread will be responsible to find the first block in each epoch
	go threads.FirstBlockInEpochMonitor()

	//✅ 7.Logical thread to build the temporary sequence of blocks to execute them (prepare for execution thread)
	go threads.SequenceAlignmentThread()

	//✅ 8.Start execution process - take blocks and execute transactions
	go threads.ExecutionThread()

	// ------------------ Anchors subnetwork related stuff ------------------

	//✅ 9.Start to generate anchor blocks
	go threads.AnchorBlocksGenerationThread()

	//✅ 10.Start to generate
	go threads.AnchorBlocksSharingAndProofsGrabingThread()

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

	databases.BLOCKS = utils.OpenDb("BLOCKS")
	databases.STATE = utils.OpenDb("STATE")
	databases.EPOCH_DATA = utils.OpenDb("EPOCH_DATA")
	databases.APPROVEMENT_THREAD_METADATA = utils.OpenDb("APPROVEMENT_THREAD_METADATA")
	databases.FINALIZATION_VOTING_STATS = utils.OpenDb("FINALIZATION_VOTING_STATS")

	// Anchors databases

	databases.ANCHOR_BLOCKS = utils.OpenDb("ANCHOR_BLOCKS")
	databases.ANCHOR_EPOCH_DATA = utils.OpenDb("ANCHOR_EPOCH_DATA")
	databases.ANCHOR_FINALIZATION_VOTING_STATS = utils.OpenDb("ANCHOR_FINALIZATION_VOTING_STATS")

	// Load GT - Generation Thread handler
	if data, err := databases.BLOCKS.Get([]byte("GT"), nil); err == nil {

		var gtHandler structures.GenerationThreadMetadataHandler

		if err := json.Unmarshal(data, &gtHandler); err == nil {

			handlers.GENERATION_THREAD_METADATA = gtHandler

		} else {

			fmt.Printf("failed to unmarshal GENERATION_THREAD: %v\n", err)

			return

		}
	} else {

		// Create initial generation thread handler

		handlers.GENERATION_THREAD_METADATA = structures.GenerationThreadMetadataHandler{

			EpochFullId: utils.Blake3("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"+globals.GENESIS.NetworkId) + "#-1",
			PrevHash:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			NextIndex:   0,
		}

	}

	// Load AT - Approvement Thread handler

	if data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("AT"), nil); err == nil {

		var atHandler structures.ApprovementThreadMetadataHandler

		if err := json.Unmarshal(data, &atHandler); err == nil {

			if atHandler.ValidatorsStoragesCache == nil {

				atHandler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)

			}

			handlers.APPROVEMENT_THREAD_METADATA.Handler = atHandler

		} else {

			fmt.Printf("failed to unmarshal APPROVEMENT_THREAD: %v\n", err)

			return

		}

	}

	// Load ET - Execution Thread handler

	if data, err := databases.STATE.Get([]byte("ET"), nil); err == nil {

		var etHandler structures.ExecutionThreadMetadataHandler

		if err := json.Unmarshal(data, &etHandler); err == nil {

			if etHandler.AccountsCache == nil {

				etHandler.AccountsCache = make(map[string]*structures.Account)

			}

			if etHandler.ValidatorsStoragesCache == nil {

				etHandler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)

			}

			handlers.EXECUTION_THREAD_METADATA.Handler = etHandler

		} else {

			fmt.Printf("failed to unmarshal EXECUTION_THREAD: %v\n", err)

			return

		}

	}

	// Init genesis if version is -1
	if handlers.EXECUTION_THREAD_METADATA.Handler.CoreMajorVersion == -1 {

		if genesisWriteErr := setGenesisToState(); genesisWriteErr != nil {

			panic("Error with writing genesis to state. Try to delete chaindata and repeat node launch")

		}

		serializedApprovementThread, err := json.Marshal(handlers.APPROVEMENT_THREAD_METADATA.Handler)

		serializedExecutionThread, err2 := json.Marshal(handlers.EXECUTION_THREAD_METADATA.Handler)

		if err != nil || err2 != nil {

			fmt.Printf("failed to marshal handlers: %v\n", err)

			return

		}

		if err := databases.APPROVEMENT_THREAD_METADATA.Put([]byte("AT"), serializedApprovementThread, nil); err != nil {

			fmt.Printf("failed to save APPROVEMENT_THREAD: %v\n", err)

			return

		}

		if err := databases.STATE.Put([]byte("ET"), serializedExecutionThread, nil); err != nil {

			fmt.Printf("failed to save EXECUTION_THREAD: %v\n", err)

			return

		}
	}

	// Version check
	if utils.IsMyCoreVersionOld(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

		utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

		utils.GracefulShutdown()

	}

}

func setGenesisToState() error {

	approvementThreadBatch := new(leveldb.Batch)

	execThreadBatch := new(leveldb.Batch)

	epochTimestamp := globals.GENESIS.FirstEpochStartTimestamp

	validatorsRegistryForEpochHandler := []string{}

	validatorsRegistryForEpochHandler2 := []string{}

	// __________________________________ Load info about accounts __________________________________

	for accountPubkey, accountData := range globals.GENESIS.State {

		serialized, err := json.Marshal(accountData)

		if err != nil {
			return err
		}

		execThreadBatch.Put([]byte(accountPubkey), serialized)

	}

	// __________________________________ Load info about validators __________________________________

	for _, validatorStorage := range globals.GENESIS.Validators {

		validatorPubkey := validatorStorage.Pubkey

		serializedStorage, err := json.Marshal(validatorStorage)

		if err != nil {
			return err
		}

		approvementThreadBatch.Put([]byte(validatorPubkey+"_VALIDATOR_STORAGE"), serializedStorage)

		execThreadBatch.Put([]byte(validatorPubkey+"_VALIDATOR_STORAGE"), serializedStorage)

		validatorsRegistryForEpochHandler = append(validatorsRegistryForEpochHandler, validatorPubkey)

		validatorsRegistryForEpochHandler2 = append(validatorsRegistryForEpochHandler2, validatorPubkey)

		handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[validatorPubkey] = structures.NewExecutionStatsTemplate()

	}

	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	handlers.EXECUTION_THREAD_METADATA.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()

	handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()

	// Commit changes
	if err := databases.APPROVEMENT_THREAD_METADATA.Write(approvementThreadBatch, nil); err != nil {
		return err
	}

	if err := databases.STATE.Write(execThreadBatch, nil); err != nil {
		return err
	}

	hashInput := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + globals.GENESIS.NetworkId

	initEpochHash := utils.Blake3(hashInput)

	epochHandlerForApprovementThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		ValidatorsRegistry: validatorsRegistryForEpochHandler,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	epochHandlerForExecThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		ValidatorsRegistry: validatorsRegistryForEpochHandler2,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	// Assign quorum - pseudorandomly and in deterministic way

	epochHandlerForApprovementThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForApprovementThread, handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize, initEpochHash)

	epochHandlerForExecThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForExecThread, handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.QuorumSize, initEpochHash)

	// Now set the block generators for epoch pseudorandomly and in deterministic way

	utils.SetLeadersSequence(&epochHandlerForApprovementThread, initEpochHash)

	utils.SetLeadersSequence(&epochHandlerForExecThread, initEpochHash)

	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForApprovementThread

	handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForExecThread

	// Store epoch data for API

	currentEpochDataHandler := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	jsonedCurrentEpochDataHandler, _ := json.Marshal(currentEpochDataHandler)

	databases.EPOCH_DATA.Put([]byte("EPOCH_HANDLER:"+strconv.Itoa(currentEpochDataHandler.Id)), jsonedCurrentEpochDataHandler, nil)

	return nil

}
