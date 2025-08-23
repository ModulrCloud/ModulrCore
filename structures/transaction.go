package structures

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
