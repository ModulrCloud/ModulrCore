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
