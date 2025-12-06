package anchors_pack

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/structures"
)

type ExtraDataToBlock struct {
	AggregatedAnchorRotationProofs     []AggregatedAnchorRotationProof                `json:"aggregatedAnchorRotationProofs,omitempty"`
	AggregatedLeaderFinalizationProofs []structures.AggregatedLeaderFinalizationProof `json:"aggregatedLeaderFinalizationProofs,omitempty"`
	Rest                               map[string]string                              `json:"rest,omitempty"`
}

type blockExtraDataAlias struct {
	AggregatedAnchorRotationProofs     []AggregatedAnchorRotationProof                `json:"aggregatedAnchorRotationProofs,omitempty"`
	AggregatedLeaderFinalizationProofs []structures.AggregatedLeaderFinalizationProof `json:"aggregatedLeaderFinalizationProofs,omitempty"`
	Rest                               map[string]string                              `json:"rest,omitempty"`
}

func (extra ExtraDataToBlock) MarshalJSON() ([]byte, error) {
	if len(extra.AggregatedAnchorRotationProofs) == 0 && len(extra.AggregatedLeaderFinalizationProofs) == 0 {
		if len(extra.Rest) == 0 {
			return []byte("{}"), nil
		}
		return json.Marshal(extra.Rest)
	}
	alias := blockExtraDataAlias(extra)
	if alias.Rest == nil {
		alias.Rest = map[string]string{}
	}
	return json.Marshal(alias)
}

func (extra *ExtraDataToBlock) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*extra = ExtraDataToBlock{}
		return nil
	}
	var alias blockExtraDataAlias
	if err := json.Unmarshal(data, &alias); err == nil && (alias.Rest != nil || alias.AggregatedAnchorRotationProofs != nil || alias.AggregatedLeaderFinalizationProofs != nil) {
		*extra = ExtraDataToBlock(alias)
		return nil
	}
	var fields map[string]string
	if err := json.Unmarshal(data, &fields); err == nil {
		extra.Rest = fields
		extra.AggregatedAnchorRotationProofs = nil
		extra.AggregatedLeaderFinalizationProofs = nil
		return nil
	}
	return fmt.Errorf("invalid extraData payload")
}
