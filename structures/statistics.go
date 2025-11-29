package structures

// Statistics aggregates blockchain-level insights that can be surfaced in UIs.
// The struct can grow with additional counters or gauges over time.
type Statistics struct {
	LastHeight             int64  `json:"lastHeight"`
	LastBlockHash          string `json:"lastBlockHash"`
	TotalTransactions      uint64 `json:"totalTransactions"`
	SuccessfulTransactions uint64 `json:"successfulTransactions"`
	FailedTransactions     uint64 `json:"failedTransactions"`
	TotalFees              uint64 `json:"totalFees"`
}
