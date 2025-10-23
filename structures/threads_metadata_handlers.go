package structures

type LogicalThread interface {
	GetCoreMajorVersion() int
	GetNetworkParams() NetworkParameters
	GetEpochHandler() EpochDataHandler
}

type ApprovementThreadMetadataHandler struct {
	CoreMajorVersion        int                          `json:"coreMajorVersion"`
	NetworkParameters       NetworkParameters            `json:"networkParameters"`
	EpochDataHandler        EpochDataHandler             `json:"epoch"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
}

type ExecutionThreadMetadataHandler struct {
	CoreMajorVersion  int               `json:"coreMajorVersion"`
	NetworkParameters NetworkParameters `json:"networkParameters"`
	EpochDataHandler  EpochDataHandler  `json:"epoch"`

	LastHeight    int64  `json:"lastHeight"`
	LastBlockHash string `json:"lastBlockHash"`

	ExecutionData             map[string]ExecutionStatsPerLeaderSequence `json:"executionData"` // PUBKEY => {index:int, hash:""}
	CurrentEpochAlignmentData AlignmentDataHandler                       `json:"currentEpochAlignmentData"`
	LegacyEpochAlignmentData  AlignmentDataHandler                       `json:"legacyEpochAlignmentData"`

	AccountsCache           map[string]*Account          `json:"-"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
}

func (handler *ApprovementThreadMetadataHandler) GetCoreMajorVersion() int {
	return handler.CoreMajorVersion
}

func (handler *ExecutionThreadMetadataHandler) GetCoreMajorVersion() int {
	return handler.CoreMajorVersion
}

func (handler *ApprovementThreadMetadataHandler) GetNetworkParams() NetworkParameters {
	return handler.NetworkParameters
}

func (handler *ApprovementThreadMetadataHandler) GetEpochHandler() EpochDataHandler {
	return handler.EpochDataHandler
}

func (handler *ExecutionThreadMetadataHandler) GetNetworkParams() NetworkParameters {
	return handler.NetworkParameters
}

func (handler *ExecutionThreadMetadataHandler) GetEpochHandler() EpochDataHandler {
	return handler.EpochDataHandler
}

type GenerationThreadMetadataHandler struct {
	EpochFullId          string                            `json:"epochFullId"`
	PrevHash             string                            `json:"prevHash"`
	NextIndex            int                               `json:"nextIndex"`
	AefpForPreviousEpoch *AggregatedEpochFinalizationProof `json:"aefpForPreviousEpoch"`
}

type AlignmentDataHandler struct {
	Activated                     bool                                       `json:"activated"`
	CurrentLeaderAssumption       int                                        `json:"currentLeader"`
	CurrentLeaderToExecBlocksFrom int                                        `json:"currentToExecute"`
	InfoAboutLastBlocksInEpoch    map[string]ExecutionStatsPerLeaderSequence `json:"infoAboutLastBlocksInEpoch"` // PUBKEY => {index:int, hash:""}
}
