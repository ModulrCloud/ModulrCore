package block_pack

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/structures"
)

type ExtraDataToBlock struct {
	Rest                     map[string]string                   `json:"rest"`
	DelayedTransactionsBatch structures.DelayedTransactionsBatch `json:"delayedTxsBatch"`
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

	*ed = ExtraDataToBlock(aux)

	return nil

}
