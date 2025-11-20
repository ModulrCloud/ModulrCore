package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/cryptography"
	"lukechampine.com/blake3"
)

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

func (t *Transaction) Hash() (string, error) {
	payloadJSON, err := json.Marshal(t.Payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	preimage := strings.Join([]string{
		strconv.FormatUint(uint64(t.V), 10),
		t.From,
		t.To,
		strconv.FormatUint(t.Amount, 10),
		strconv.FormatUint(t.Fee, 10),
		strconv.FormatUint(t.Nonce, 10),
		string(payloadJSON),
	}, ":")

	sum := blake3.Sum256([]byte(preimage))
	return hex.EncodeToString(sum[:]), nil
}

func sign(tx Transaction, privateKey string) (string, error) {
	hash, err := tx.Hash()
	if err != nil {
		return "", err
	}

	return cryptography.GenerateSignature(privateKey, hash), nil
}

func buildTransaction(sender Sender, nonce uint64, amount uint64, recipient string, payload map[string]any) (Transaction, error) {
	tx := Transaction{
		V:       sender.version(),
		From:    sender.PublicKey,
		To:      recipient,
		Amount:  amount,
		Fee:     sender.Fee,
		Nonce:   nonce,
		Payload: payload,
	}

	sig, err := sign(tx, sender.PrivateKey)
	if err != nil {
		return Transaction{}, err
	}
	tx.Sig = sig

	return tx, nil
}
