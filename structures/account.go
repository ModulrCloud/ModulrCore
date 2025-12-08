package structures

type Account struct {
	Balance                         uint64 `json:"balance"`
	Nonce                           uint64 `json:"nonce"`
	InitiatedTransactions           uint64 `json:"initiatedTransactions"`
	SuccessfulInitiatedTransactions uint64 `json:"successfulInitiatedTransactions"`
}
