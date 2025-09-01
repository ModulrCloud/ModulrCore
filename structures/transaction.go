package structures

import (
	"encoding/hex"
	"encoding/json"
	"strconv"

	"lukechampine.com/blake3"
)

type Transaction struct {
	V       uint           `json:"v"`
	Type    string         `json:"type"`
	From    string         `json:"from"`
	To      string         `json:"to"`
	Amount  uint64         `json:"amount"`
	Fee     string         `json:"fee"`
	Sig     string         `json:"sig"`
	Nonce   int            `json:"nonce"`
	Payload map[string]any `json:"payload"`
}

func (t Transaction) Hash() string {

	payloadJSON, err := json.Marshal(t.Payload)
	if err != nil {
		return ""
	}

	data := strconv.FormatUint(uint64(t.V), 10) +
		t.Type +
		t.From +
		t.To +
		strconv.FormatUint(t.Amount, 10) +
		t.Fee +
		t.Sig +
		strconv.Itoa(t.Nonce) +
		string(payloadJSON)

	blake3Hash := blake3.Sum256([]byte(data))

	return hex.EncodeToString(blake3Hash[:])

}
