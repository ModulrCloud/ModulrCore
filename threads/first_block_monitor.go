package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ModulrCloud/ModulrCore/block_pack"
	"github.com/ModulrCloud/ModulrCore/globals"
	"github.com/ModulrCloud/ModulrCore/structures"
	"github.com/ModulrCloud/ModulrCore/utils"
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

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RLock()

		epochHandlerRef := &globals.APPROVEMENT_THREAD_METADATA_HANDLER.Handler.EpochDataHandler

		firstBlockData := GetFirstBlockDataFromDB(epochHandlerRef.Id)

		// If we found first block data for current epoch - no sense to do smth else. Just sleep and keep the cycle active

		if firstBlockData != nil {

			globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

			time.Sleep(time.Second)

			continue

		}

		firstBlockData = getFirstBlockInEpoch(epochHandlerRef)

		if firstBlockData != nil {

			// Store the info about first block on current epoch in DB

			serializedData, serialErr := json.Marshal(firstBlockData)

			if serialErr == nil {

				globals.EPOCH_DATA.Put([]byte("FIRST_BLOCK_IN_EPOCH:"+strconv.Itoa(epochHandlerRef.Id)), serializedData, nil)

				msg := fmt.Sprintf(
					"%sFirst block for epoch %s%d %sis %s(hash:%s...) %sby %s%s",
					utils.DEEP_GREEN_COLOR,
					utils.CYAN_COLOR,
					epochHandlerRef.Id,
					utils.DEEP_GREEN_COLOR,
					utils.CYAN_COLOR,
					firstBlockData.FirstBlockHash[:8],
					utils.DEEP_GREEN_COLOR,
					utils.CYAN_COLOR,
					firstBlockData.FirstBlockCreator,
				)

				fmt.Println()
				utils.LogWithTime(msg, utils.WHITE_COLOR)
				fmt.Println()
			}

		}

		globals.APPROVEMENT_THREAD_METADATA_HANDLER.RWMutex.RUnlock()

	}

}

func GetFirstBlockDataFromDB(epochIndex int) *structures.FirstBlockResult {

	if rawBytes, err := globals.EPOCH_DATA.Get([]byte("FIRST_BLOCK_IN_EPOCH:"+strconv.Itoa(epochIndex)), nil); err == nil {

		var firstBlockData *structures.FirstBlockResult

		if err := json.Unmarshal(rawBytes, &firstBlockData); err == nil {

			return firstBlockData

		}

	}

	return nil

}

func getFirstBlockInEpoch(epochHandler *structures.EpochDataHandler) *structures.FirstBlockResult {

	if PIVOT == nil {

		allKnownNodes := utils.GetQuorumUrlsAndPubkeys(epochHandler)

		var wg sync.WaitGroup

		responses := make(chan *structures.FirstBlockAssumption, len(allKnownNodes))

		for _, node := range allKnownNodes {
			wg.Add(1)

			go func(nodeUrl string) {
				defer wg.Done()

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				req, err := http.NewRequestWithContext(ctx, "GET", nodeUrl+"/first_block_assumption/"+strconv.Itoa(epochHandler.Id), nil)
				if err != nil {
					return
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return
				}
				defer resp.Body.Close()

				var prop structures.FirstBlockAssumption
				if err := json.NewDecoder(resp.Body).Decode(&prop); err != nil {
					return
				}

				responses <- &prop

			}(node.Url)
		}

		wg.Wait()

		close(responses)

		minimalIndexOfLeader := 1_000_000_000

		var afpForSecondBlock *structures.AggregatedFinalizationProof

		for prop := range responses {

			if prop == nil {
				continue
			}

			if prop.IndexOfFirstBlockCreator < 0 || prop.IndexOfFirstBlockCreator >= len(epochHandler.LeadersSequence) {
				continue
			}

			firstBlockCreator := epochHandler.LeadersSequence[prop.IndexOfFirstBlockCreator]

			if utils.VerifyAggregatedFinalizationProof(&prop.AfpForSecondBlock, epochHandler) {

				expectedSecondBlockID := strconv.Itoa(epochHandler.Id) + ":" + firstBlockCreator + ":1"

				if expectedSecondBlockID == prop.AfpForSecondBlock.BlockId && prop.IndexOfFirstBlockCreator < minimalIndexOfLeader {

					minimalIndexOfLeader = prop.IndexOfFirstBlockCreator

					afpForSecondBlock = &prop.AfpForSecondBlock

				}

			}

		}

		if afpForSecondBlock != nil {

			position := minimalIndexOfLeader

			pivotPubKey := epochHandler.LeadersSequence[position]

			firstBlockByPivot := block_pack.GetBlock(epochHandler.Id, pivotPubKey, uint(0), epochHandler)

			firstBlockHash := afpForSecondBlock.PrevBlockHash

			if firstBlockByPivot != nil && firstBlockHash == firstBlockByPivot.GetHash() {

				PIVOT = &PivotSearchData{

					Position:          position,
					PivotPubKey:       pivotPubKey,
					FirstBlockByPivot: firstBlockByPivot,
					FirstBlockHash:    firstBlockHash,
				}
			}
		}
	}

	if PIVOT != nil {

		// In pivot we have first block created in epoch by some pool

		// Try to move closer to the beginning of the epochHandler.leadersSequence to find the real first block

		// Based on ALRP in pivot block - find the real first block

		blockToEnumerateAlrp := PIVOT.FirstBlockByPivot

		if PIVOT.Position == 0 {

			defer func() {

				PIVOT = nil

			}()

			return &structures.FirstBlockResult{

				FirstBlockCreator: PIVOT.PivotPubKey,
				FirstBlockHash:    PIVOT.FirstBlockHash,
			}
		}

		for position := PIVOT.Position - 1; position >= 0; position-- {

			previousPool := epochHandler.LeadersSequence[position]

			leaderRotationProof, ok := blockToEnumerateAlrp.ExtraData.AggregatedLeadersRotationProofs[previousPool]

			if !ok {

				leaderRotationProof.SkipIndex = -1
			}

			if position == 0 {

				defer func() {

					PIVOT = nil

				}()

				if leaderRotationProof.SkipIndex == -1 {
					return &structures.FirstBlockResult{
						FirstBlockCreator: PIVOT.PivotPubKey,
						FirstBlockHash:    PIVOT.FirstBlockHash,
					}
				} else {
					return &structures.FirstBlockResult{
						FirstBlockCreator: previousPool,
						FirstBlockHash:    leaderRotationProof.FirstBlockHash,
					}
				}

			} else if leaderRotationProof.SkipIndex != -1 {

				// Found new potential pivot

				firstBlockByNewPivot := block_pack.GetBlock(epochHandler.Id, previousPool, 0, epochHandler)

				if firstBlockByNewPivot == nil {
					return nil
				}

				if firstBlockByNewPivot.GetHash() == leaderRotationProof.FirstBlockHash {

					PIVOT = &PivotSearchData{
						Position:          position,
						PivotPubKey:       previousPool,
						FirstBlockByPivot: firstBlockByNewPivot,
						FirstBlockHash:    leaderRotationProof.FirstBlockHash,
					}

					break // break cycle to run the cycle later with new pivot

				} else {

					return nil

				}
			}
		}
	}

	return nil

}
