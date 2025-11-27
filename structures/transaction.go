package structures

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"lukechampine.com/blake3"
)

type TransactionLocation struct {
	Block    string `json:"block"`
	Position int    `json:"position"`
}

type Transaction struct {
	V       uint           `json:"v"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	Amount  uint64         `json:"amount"`
	Fee     uint64         `json:"fee"`
	Sig     string         `json:"sig"`
	Nonce   uint64         `json:"nonce"`
	Payload map[string]any `json:"payload"`
}

func (t *Transaction) Hash() string {
	payloadJSON, err := marshalSortedPayload(normalizePayload(t.Payload))
	if err != nil {
		return ""
	}

	preimage := strings.Join([]string{
		strconv.FormatUint(uint64(t.V), 10),
		t.From,
		t.To,
		strconv.FormatUint(t.Amount, 10),
		strconv.FormatUint(t.Fee, 10),
		strconv.FormatUint(uint64(t.Nonce), 10),
		string(payloadJSON),
	}, ":")

	sum := blake3.Sum256([]byte(preimage))
	return hex.EncodeToString(sum[:])
}

func normalizePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return make(map[string]any)
	}

	return payload
}

func marshalSortedPayload(payload map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}

		marshaledKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}

		marshaledValue, err := json.Marshal(payload[key])
		if err != nil {
			return nil, err
		}

		buf.Write(marshaledKey)
		buf.WriteByte(':')
		buf.Write(marshaledValue)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (t Transaction) MarshalJSON() ([]byte, error) {
	payloadJSON, err := marshalSortedPayload(normalizePayload(t.Payload))
	if err != nil {
		return nil, err
	}

	type alias Transaction

	aux := struct {
		alias
		Payload json.RawMessage `json:"payload"`
	}{
		alias:   alias(t),
		Payload: payloadJSON,
	}

	return json.Marshal(aux)
}

func (t *Transaction) UnmarshalJSON(data []byte) error {
	type alias Transaction
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Payload == nil {
		aux.Payload = make(map[string]any)
	}
	*t = Transaction(aux)
	return nil
}
