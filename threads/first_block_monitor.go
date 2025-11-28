package threads

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type PivotSearchData struct {
	Position          int
	PivotPubKey       string
	FirstBlockByPivot *block_pack.Block
	FirstBlockHash    string
}

var PIVOT *PivotSearchData

func FirstBlockInEpochMonitor() {

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

		epochHandlerRef := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

		firstBlockData := GetFirstBlockDataFromDB(epochHandlerRef.Id)

		// If we found first block data for current epoch - no sense to do smth else. Just sleep and keep the cycle active

		if firstBlockData != nil {

			handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

			time.Sleep(time.Second)

			continue

		}

		firstBlockData = getFirstBlockInEpoch(epochHandlerRef)

		if firstBlockData != nil {

			// Store the info about first block on current epoch in DB

			serializedData, serialErr := json.Marshal(firstBlockData)

			if serialErr == nil {

				databases.EPOCH_DATA.Put([]byte("FIRST_BLOCK_IN_EPOCH:"+strconv.Itoa(epochHandlerRef.Id)), serializedData, nil)

				msg := fmt.Sprintf(
					"%sFirst block for epoch %s%d %sis created by %s%s%s",
					utils.DEEP_GREEN_COLOR,
					utils.CYAN_COLOR,
					epochHandlerRef.Id,
					utils.DEEP_GREEN_COLOR,
					utils.CYAN_COLOR,
					firstBlockData.FirstBlockCreator,
					utils.RESET_COLOR,
				)

				fmt.Println()
				utils.LogWithTime2(msg, utils.WHITE_COLOR)
				fmt.Println()
			}

		}

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	}

}

func GetFirstBlockDataFromDB(epochIndex int) *structures.FirstBlockResult {

	if rawBytes, err := databases.EPOCH_DATA.Get([]byte("FIRST_BLOCK_IN_EPOCH:"+strconv.Itoa(epochIndex)), nil); err == nil {

		var firstBlockData structures.FirstBlockResult

		if err := json.Unmarshal(rawBytes, &firstBlockData); err == nil {

			return &firstBlockData

		}

	}

	return nil

}

func getFirstBlockInEpoch(epochHandler *structures.EpochDataHandler) *structures.FirstBlockResult {

	// TBD
	return nil

}
