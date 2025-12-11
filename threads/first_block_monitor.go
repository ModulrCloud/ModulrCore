package threads

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func FirstBlockMonitorThread() {

	var epochUnderObservation int
	initialized := false

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		currentEpoch := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if !initialized || currentEpoch != epochUnderObservation {
			epochUnderObservation = currentEpoch
			initialized = true
		}

		if getFirstBlockDataFromDB(epochUnderObservation) != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if firstBlockData := findFirstBlockDataFromAlignment(epochUnderObservation); firstBlockData != nil {
			if err := storeFirstBlockData(epochUnderObservation, firstBlockData); err != nil {
				utils.LogWithTime(fmt.Sprintf("failed to store first block data for epoch %d: %v", epochUnderObservation, err), utils.RED_COLOR)
			}
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func storeFirstBlockData(epochIndex int, data *FirstBlockData) error {

	raw, err := json.Marshal(data)

	if err != nil {

		return err

	}

	return databases.APPROVEMENT_THREAD_METADATA.Put(firstBlockDataKey(epochIndex), raw, nil)
}

func findFirstBlockDataFromAlignment(epochIndex int) *FirstBlockData {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochIndex {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()
		return nil
	}

	leaderSequence := append([]string{}, handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.LeadersSequence...)
	alignmentData := make(map[string]structures.ExecutionStats, len(handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders))
	for leader, stat := range handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders {
		alignmentData[leader] = stat
	}

	epochHandlerCopy := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	for _, leader := range leaderSequence {
		leaderStats, exists := alignmentData[leader]
		if !exists || leaderStats.Index < 0 {
			continue
		}

		blockID := fmt.Sprintf("%d:%s:%d", epochIndex, leader, 0)
		response := getBlockAndAfpFromPoD(blockID)
		if response == nil || response.Block == nil {
			continue
		}

		block := response.Block

		if block.Creator != leader || block.Index != 0 {
			continue
		}

		if block.Sig != "" && !block.VerifySignature() {
			continue
		}

		firstBlockHash := block.GetHash()

		if leaderStats.Index > 0 {
			if response.Afp == nil || response.Afp.BlockId != blockID || response.Afp.BlockHash != firstBlockHash {
				continue
			}

			if !utils.VerifyAggregatedFinalizationProof(response.Afp, &epochHandlerCopy) {
				continue
			}
		} else {
			if leaderStats.Hash != "" && leaderStats.Hash != firstBlockHash {
				continue
			}
		}

		return &FirstBlockData{FirstBlockCreator: leader, FirstBlockHash: firstBlockHash}
	}

	return nil
}
