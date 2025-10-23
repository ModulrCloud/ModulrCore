package structures

type EpochDataHandler struct {
	Id                 int      `json:"id"`
	Hash               string   `json:"hash"`
	ValidatorsRegistry []string `json:"validatorsRegistry"`
	Quorum             []string `json:"quorum"`
	LeadersSequence    []string `json:"leadersSequence"`
	StartTimestamp     uint64   `json:"startTimestamp"`
	CurrentLeaderIndex int      `json:"currentLeaderIndex"`
}

type NextEpochDataHandler struct {
	NextEpochHash               string              `json:"nextEpochHash"`
	NextEpochValidatorsRegistry []string            `json:"nextEpochValidatorsRegistry"`
	NextEpochQuorum             []string            `json:"nextEpochQuorum"`
	NextEpochLeadersSequence    []string            `json:"nextEpochLeadersSequence"`
	DelayedTransactions         []map[string]string `json:"delayedTransactions"`
}
