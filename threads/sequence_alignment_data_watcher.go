package threads

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func SequenceAlignmentDataWatcher() {

	client := &http.Client{Timeout: 5 * time.Second}

	for {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		anchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption
		epochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		checkSequenceAlignmentData(anchorIndex, &epochHandler, client)

		time.Sleep(5 * time.Second)
	}

}

func checkSequenceAlignmentData(anchorIndex int, epochHandler *structures.EpochDataHandler, client *http.Client) {

	if anchorIndex < 0 || anchorIndex >= len(globals.ANCHORS) || client == nil {
		return
	}

	targetAnchor := globals.ANCHORS[utils.RANDOM_GENERATOR.Intn(len(globals.ANCHORS))]

	resp, err := client.Get(fmt.Sprintf("%s/sequence_alignment_data/%d/%d", targetAnchor.AnchorUrl, epochHandler.Id, anchorIndex))

	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return
	}

	defer resp.Body.Close()

	dec := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))

	var alignmentData SequenceAlignmentDataResponse

	if err := dec.Decode(&alignmentData); err != nil {
		return
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	processSequenceAlignmentDataResponse(&alignmentData, anchorIndex, epochHandler, &handlers.EXECUTION_THREAD_METADATA.Handler)

}

func processSequenceAlignmentDataResponse(alignmentData *SequenceAlignmentDataResponse, anchorIndex int, epochHandler *structures.EpochDataHandler, metadata *structures.ExecutionThreadMetadataHandler) bool {

	if alignmentData == nil || alignmentData.Afp == nil || metadata == nil || epochHandler == nil {
		return false
	}

	if alignmentData.FoundInAnchorIndex <= anchorIndex || alignmentData.FoundInAnchorIndex >= len(globals.ANCHORS) {
		return false
	}

	if metadata.SequenceAlignmentData.AnchorCatchUpTargets != nil {
		if _, exists := metadata.SequenceAlignmentData.AnchorCatchUpTargets[anchorIndex]; exists {
			return false
		}
	}

	if !utils.VerifyAggregatedFinalizationProof(alignmentData.Afp, epochHandler) {
		return false
	}

	blockIdParts := strings.Split(alignmentData.Afp.BlockId, ":")

	if len(blockIdParts) != 3 {
		return false
	}

	epochIDFromBlock, err := strconv.Atoi(blockIdParts[0])

	if err != nil || epochIDFromBlock != epochHandler.Id {
		return false
	}

	blockIndexInAfp, err := strconv.Atoi(blockIdParts[2])

	if err != nil {
		return false
	}

	anchorFromBlock := blockIdParts[1]
	expectedAnchor := globals.ANCHORS[alignmentData.FoundInAnchorIndex]

	if anchorFromBlock != expectedAnchor.Pubkey {
		return false
	}

	maxFoundInBlock := -1

	anchorIndexMap := make(map[string]int, len(globals.ANCHORS))
	for idx, anchor := range globals.ANCHORS {
		anchorIndexMap[strings.ToLower(anchor.Pubkey)] = idx
	}

	for _, anchorData := range alignmentData.Anchors {
		if anchorData.FoundInBlock > maxFoundInBlock {
			maxFoundInBlock = anchorData.FoundInBlock
		}
	}

	if blockIndexInAfp != maxFoundInBlock+1 {
		return false
	}

	for i := anchorIndex; i < alignmentData.FoundInAnchorIndex; i++ {
		anchorData, exists := alignmentData.Anchors[i]

		if !exists {
			return false
		}

		anchorForProof := globals.ANCHORS[i]

		if !utils.VerifyAggregatedAnchorRotationProof(anchorData.AggregatedAnchorRotationProof.EpochIndex, anchorData.AggregatedAnchorRotationProof.Anchor, epochHandler, anchorForProof.Pubkey) {
			return false
		}
	}

	earliestRotationStats, found := findEarliestAnchorRotationProof(anchorIndex, alignmentData.FoundInAnchorIndex, blockIndexInAfp, epochHandler, anchorIndexMap)
	if !found {
		return false
	}

	if metadata.SequenceAlignmentData.AnchorCatchUpTargets == nil {
		metadata.SequenceAlignmentData.AnchorCatchUpTargets = make(map[int]structures.ExecutionStatsPerLeaderSequence)
	}

	if _, exists := metadata.SequenceAlignmentData.AnchorCatchUpTargets[anchorIndex]; !exists {
		metadata.SequenceAlignmentData.AnchorCatchUpTargets[anchorIndex] = earliestRotationStats
	}

	return true
}

func findEarliestAnchorRotationProof(currentAnchor, foundInAnchorIndex, blockLimit int, epochHandler *structures.EpochDataHandler, anchorIndexMap map[string]int) (structures.ExecutionStatsPerLeaderSequence, bool) {

	if epochHandler == nil || anchorIndexMap == nil || currentAnchor < 0 || foundInAnchorIndex >= len(globals.ANCHORS) {
		return structures.ExecutionStatsPerLeaderSequence{}, false
	}

	searchLimit := blockLimit
	earliestStats := structures.ExecutionStatsPerLeaderSequence{}

	for anchorIdx := foundInAnchorIndex; anchorIdx > currentAnchor; anchorIdx-- {
		anchor := globals.ANCHORS[anchorIdx]
		neededProofs := anchorIdx - currentAnchor
		foundProofs := make(map[int]int)
		proofStats := make(map[int]structures.ExecutionStatsPerLeaderSequence)

		for blockIndex := 0; blockIndex < searchLimit; blockIndex++ {
			blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, blockIndex)
			response := getAnchorBlockAndAfpFromAnchorsPoD(blockID)

			if response == nil || response.Block == nil {
				return structures.ExecutionStatsPerLeaderSequence{}, false
			}

			block := response.Block

			if block.Creator != anchor.Pubkey || block.Index != blockIndex || !block.VerifySignature() {
				return structures.ExecutionStatsPerLeaderSequence{}, false
			}

			for _, proof := range block.ExtraData.AggregatedAnchorRotationProofs {
				targetIdx, exists := anchorIndexMap[strings.ToLower(proof.Anchor)]

				if !exists || targetIdx < currentAnchor || targetIdx >= anchorIdx {
					continue
				}

				expectedAnchor := globals.ANCHORS[targetIdx]

				if !utils.VerifyAggregatedAnchorRotationProof(proof.EpochIndex, proof.Anchor, epochHandler, expectedAnchor.Pubkey) {
					continue
				}

				if _, alreadyFound := foundProofs[targetIdx]; !alreadyFound {
					foundProofs[targetIdx] = blockIndex
					proofStats[targetIdx] = structures.ExecutionStatsPerLeaderSequence{
						Index: proof.VotingStat.Index,
						Hash:  proof.VotingStat.Hash,
					}
				}
			}

			if len(foundProofs) == neededProofs {
				break
			}
		}

		if len(foundProofs) != neededProofs {
			return structures.ExecutionStatsPerLeaderSequence{}, false
		}

		nextLimit, ok := foundProofs[anchorIdx-1]

		if !ok {
			return structures.ExecutionStatsPerLeaderSequence{}, false
		}

		if stats, ok := proofStats[anchorIdx-1]; ok {
			earliestStats = stats
		}

		searchLimit = nextLimit

		if anchorIdx-1 == currentAnchor {
			return earliestStats, true
		}
	}

	return structures.ExecutionStatsPerLeaderSequence{}, false
}
