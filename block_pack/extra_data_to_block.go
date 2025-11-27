package block_pack

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"

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

func (ed ExtraDataToBlock) MarshalJSON() ([]byte, error) {

	const fieldRest = "\"rest\""
	const fieldAefp = "\"aefpForPreviousEpoch\""
	const fieldDelayed = "\"delayedTxsBatch\""
	const fieldAlrps = "\"aggregatedLeadersRotationProofs\""

	if ed.Rest == nil {
		ed.Rest = make(map[string]string)
	}

	if ed.AggregatedLeadersRotationProofs == nil {
		ed.AggregatedLeadersRotationProofs = make(map[string]*structures.AggregatedLeaderRotationProof)
	}

	var buffer bytes.Buffer
	buffer.WriteByte('{')

	buffer.WriteString(fieldRest)
	buffer.WriteByte(':')
	encodeSortedStringMap(&buffer, ed.Rest)
	buffer.WriteByte(',')

	buffer.WriteString(fieldAefp)
	buffer.WriteByte(':')
	encodedAefp, err := json.Marshal(ed.AefpForPreviousEpoch)
	if err != nil {
		return nil, err
	}
	buffer.Write(encodedAefp)
	buffer.WriteByte(',')

	buffer.WriteString(fieldDelayed)
	buffer.WriteByte(':')
	encodedDelayed, err := json.Marshal(ed.DelayedTransactionsBatch)
	if err != nil {
		return nil, err
	}
	buffer.Write(encodedDelayed)
	buffer.WriteByte(',')

	buffer.WriteString(fieldAlrps)
	buffer.WriteByte(':')
	if err := encodeSortedAlrpMap(&buffer, ed.AggregatedLeadersRotationProofs); err != nil {
		return nil, err
	}

	buffer.WriteByte('}')

	return buffer.Bytes(), nil

}

func encodeSortedStringMap(buffer *bytes.Buffer, value map[string]string) {

	keys := make([]string, 0, len(value))

	for key := range value {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	buffer.WriteByte('{')

	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}

		buffer.WriteString(strconv.Quote(key))
		buffer.WriteByte(':')
		buffer.WriteString(strconv.Quote(value[key]))
	}

	buffer.WriteByte('}')

}

func encodeSortedAlrpMap(buffer *bytes.Buffer, value map[string]*structures.AggregatedLeaderRotationProof) error {

	keys := make([]string, 0, len(value))

	for key := range value {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	buffer.WriteByte('{')

	for index, key := range keys {
		if index > 0 {
			buffer.WriteByte(',')
		}

		buffer.WriteString(strconv.Quote(key))
		buffer.WriteByte(':')
		encodedValue, err := json.Marshal(value[key])
		if err != nil {
			return err
		}
		buffer.Write(encodedValue)
	}

	buffer.WriteByte('}')

	return nil

}
