package globals

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ModulrCloud/ModulrCore/structures"
)

var CORE_MAJOR_VERSION = func() int {

	data, err := os.ReadFile("version.txt")

	if err != nil {
		panic("Failed to read version.txt: " + err.Error())
	}

	version, err := strconv.Atoi(string(data))

	if err != nil {
		panic("Invalid version format: " + err.Error())
	}

	return version

}()

var CHAINDATA_PATH = func() string {

	dirPath := os.Getenv("CHAINDATA_PATH")

	if dirPath == "" {

		panic("CHAINDATA_PATH environment variable is not set")

	}

	dirPath = strings.TrimRight(dirPath, "/")

	if !filepath.IsAbs(dirPath) {

		panic("CHAINDATA_PATH must be an absolute path")

	}

	// Check if exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {

		// If no - create
		if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {

			panic("Error with creating directory for chaindata: " + err.Error())

		}

	}

	return dirPath

}()

var CONFIGURATION structures.NodeLevelConfig

var GENESIS structures.Genesis

var MEMPOOL struct {
	Slice []structures.Transaction
	Mutex sync.Mutex
}

var GENERATION_THREAD_METADATA_HANDLER structures.GenerationThreadMetadataHandler

var APPROVEMENT_THREAD_METADATA_HANDLER = struct {
	RWMutex sync.RWMutex
	Handler structures.ApprovementThreadMetadataHandler
}{
	Handler: structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion:        -1,
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	},
}

var EXECUTION_THREAD_METADATA_HANDLER = struct {
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

// Flag to use in websocket & http routes to prevent flood of .RLock() calls on mutexes

var FLOOD_PREVENTION_FLAG_FOR_ROUTES atomic.Bool
