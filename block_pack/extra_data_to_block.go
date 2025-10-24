package block_pack

import (
	"encoding/json"

	"github.com/ModulrCloud/ModulrCore/structures"
)

type ExtraDataToBlock struct {
	Rest                            map[string]string                                    `json:"rest"`
	AefpForPreviousEpoch            *structures.AggregatedEpochFinalizationProof         `json:"aefpForPreviousEpoch"`
	DelayedTransactionsBatch        structures.DelayedTransactionsBatch                  `json:"delayedTxsBatch"`
	AggregatedLeadersRotationProofs map[string]*structures.AggregatedLeaderRotationProof `json:"aggregatedLeadersRotationProofs"`
}

func (ed *ExtraDataToBlock) UnmarshalJSON(data []byte) error {
	type alias ExtraDataToBlock
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Rest == nil {
		aux.Rest = make(map[string]string)
	}
	if aux.AggregatedLeadersRotationProofs == nil {
		aux.AggregatedLeadersRotationProofs = make(map[string]*structures.AggregatedLeaderRotationProof)
	}
	*ed = ExtraDataToBlock(aux)
	return nil
}
