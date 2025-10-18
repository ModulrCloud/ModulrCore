package structures

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"lukechampine.com/blake3"
)

type Transaction struct {
	V       uint           `json:"v"`
	Type    string         `json:"type"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	Amount  uint64         `json:"amount"`
	Fee     uint64         `json:"fee"`
	Sig     string         `json:"sig"`
	Nonce   uint64         `json:"nonce"`
	Payload map[string]any `json:"payload"`
}

func (t *Transaction) Hash() string {
	payloadJSON, err := json.Marshal(t.Payload)
	if err != nil {
		return ""
	}

	preimage := strings.Join([]string{
		strconv.FormatUint(uint64(t.V), 10),
		t.Type,
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
