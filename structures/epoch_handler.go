package structures

type EpochDataHandler struct {
	Id                 int      `json:"id"`
	Hash               string   `json:"hash"`
	PoolsRegistry      []string `json:"poolsRegistry"`
	Quorum             []string `json:"quorum"`
	LeadersSequence    []string `json:"leadersSequence"`
	StartTimestamp     uint64   `json:"startTimestamp"`
	CurrentLeaderIndex int      `json:"currentLeaderIndex"`
}

type NextEpochDataHandler struct {
	NextEpochHash            string              `json:"nextEpochHash"`
	NextEpochPoolsRegistry   []string            `json:"nextEpochPoolsRegistry"`
	NextEpochQuorum          []string            `json:"nextEpochQuorum"`
	NextEpochLeadersSequence []string            `json:"nextEpochLeadersSequence"`
	DelayedTransactions      []map[string]string `json:"delayedTransactions"`
}
