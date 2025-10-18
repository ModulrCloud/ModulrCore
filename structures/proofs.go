package structures

import "encoding/json"

type AggregatedFinalizationProof struct {
	PrevBlockHash string            `json:"prevBlockHash"`
	BlockId       string            `json:"blockId"`
	BlockHash     string            `json:"blockHash"`
	Proofs        map[string]string `json:"proofs"`
}

func (afp *AggregatedFinalizationProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedFinalizationProof
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}
	*afp = AggregatedFinalizationProof(aux)
	return nil
}

type AggregatedEpochFinalizationProof struct {
	LastLeader                   uint              `json:"lastLeader"`
	LastIndex                    uint              `json:"lastIndex"`
	LastHash                     string            `json:"lastHash"`
	HashOfFirstBlockByLastLeader string            `json:"hashOfFirstBlockByLastLeader"`
	Proofs                       map[string]string `json:"proofs"`
}

func (aefp *AggregatedEpochFinalizationProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedEpochFinalizationProof
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}
	*aefp = AggregatedEpochFinalizationProof(aux)
	return nil
}

type AggregatedLeaderRotationProof struct {
	FirstBlockHash string            `json:"firstBlockHash"`
	SkipIndex      int               `json:"skipIndex"`
	SkipHash       string            `json:"skipHash"`
	Proofs         map[string]string `json:"proofs"`
}

func (alrp *AggregatedLeaderRotationProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedLeaderRotationProof
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}
	*alrp = AggregatedLeaderRotationProof(aux)
	return nil
}

type AlrpSkeleton struct {
	AfpForFirstBlock AggregatedFinalizationProof
	SkipData         PoolVotingStat
	Proofs           map[string]string // quorumMemberPubkey => signature
}

func NewAlrpSkeletonTemplate() *AlrpSkeleton {

	return &AlrpSkeleton{

		AfpForFirstBlock: AggregatedFinalizationProof{},
		SkipData:         NewPoolVotingStatTemplate(),
		Proofs:           make(map[string]string),
	}

}
