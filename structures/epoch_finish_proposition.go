package structures

type EpochFinishRequest struct {
	CurrentLeader        int                         `json:"currentLeader"`
	AfpForFirstBlock     AggregatedFinalizationProof `json:"afpForFirstBlock"`
	LastBlockProposition LeaderVotingStat            `json:"lastBlockProposition"`
}

type EpochFinishResponseOk struct {
	Status string `json:"status"`
	Sig    string `json:"sig"`
}

type EpochFinishResponseUpgrade struct {
	Status               string           `json:"status"`
	CurrentLeader        int              `json:"currentLeader"`
	LastBlockProposition LeaderVotingStat `json:"lastBlockProposition"`
}
