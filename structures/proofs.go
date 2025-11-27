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

func (afp AggregatedFinalizationProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedFinalizationProof

	if afp.Proofs == nil {
		afp.Proofs = make(map[string]string)
	}

	return json.Marshal(alias(afp))
}

type AggregatedLeaderRotationProof struct {
	FirstBlockHash string            `json:"firstBlockHash"`
	SkipIndex      int               `json:"skipIndex"`
	SkipHash       string            `json:"skipHash"`
	Proofs         map[string]string `json:"proofs"`
}

func (alrp AggregatedLeaderRotationProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedLeaderRotationProof

	if alrp.Proofs == nil {
		alrp.Proofs = make(map[string]string)
	}

	return json.Marshal(alias(alrp))
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
	SkipData         LeaderVotingStat
	Proofs           map[string]string // quorumMemberPubkey => signature
}

func NewAlrpSkeletonTemplate() *AlrpSkeleton {

	return &AlrpSkeleton{

		AfpForFirstBlock: AggregatedFinalizationProof{},
		SkipData:         NewLeaderVotingStatTemplate(),
		Proofs:           make(map[string]string),
	}

}
