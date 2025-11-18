package structures

import "encoding/json"

type ResponseStatus struct {
	Status string
}

type QuorumMemberData struct {
	PubKey, Url string
}

type FirstBlockResult struct {
	FirstBlockCreator, FirstBlockHash string
}

type FirstBlockDataForNextEpoch struct {
	FirstBlockResult
}

type FirstBlockAssumption struct {
	IndexOfFirstBlockCreator int                         `json:"indexOfFirstBlockCreator"`
	AfpForSecondBlock        AggregatedFinalizationProof `json:"afpForSecondBlock"`
}

type DelayedTransactionsBatch struct {
	EpochIndex          int                 `json:"epochIndex"`
	DelayedTransactions []map[string]string `json:"delayedTransactions"`
	Proofs              map[string]string   `json:"proofs"`
}

func (dtb *DelayedTransactionsBatch) UnmarshalJSON(data []byte) error {
	type alias DelayedTransactionsBatch
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.DelayedTransactions == nil {
		aux.DelayedTransactions = make([]map[string]string, 0)
	}
	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}
	*dtb = DelayedTransactionsBatch(aux)
	return nil
}

type ExecutionStatsPerLeaderSequence struct {
	Index          int
	Hash           string
	FirstBlockHash string
}

func NewExecutionStatsTemplate() ExecutionStatsPerLeaderSequence {

	return ExecutionStatsPerLeaderSequence{
		Index:          -1,
		Hash:           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		FirstBlockHash: "",
	}

}
