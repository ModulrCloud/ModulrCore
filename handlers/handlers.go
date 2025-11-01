package handlers

import (
	"sync"

	"github.com/ModulrCloud/ModulrCore/structures"
)

var GENERATION_THREAD_METADATA structures.GenerationThreadMetadataHandler

var APPROVEMENT_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ApprovementThreadMetadataHandler
}{
	Handler: structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion:        -1,
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	},
}

var EXECUTION_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ExecutionThreadMetadataHandler
}{
	Handler: structures.ExecutionThreadMetadataHandler{
		CoreMajorVersion:        -1,
		AccountsCache:           make(map[string]*structures.Account),
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
		LastHeight:              -1,
		ExecutionData:           make(map[string]structures.ExecutionStatsPerLeaderSequence),
		CurrentEpochAlignmentData: structures.AlignmentDataHandler{
			Activated:                  true,
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerLeaderSequence),
		},
		LegacyEpochAlignmentData: structures.AlignmentDataHandler{
			InfoAboutLastBlocksInEpoch: make(map[string]structures.ExecutionStatsPerLeaderSequence),
		},
	},
}
