package tests

import (
	"encoding/json"
	"testing"

	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"github.com/modulrcloud/modulr-core/structures"
)

func TestTransactionHashStableAcrossJSONRoundTrip(t *testing.T) {
	tx := structures.Transaction{
		V:      1,
		From:   "alice",
		To:     "bob",
		Amount: 10,
		Fee:    1,
		Sig:    "signature",
		Nonce:  42,
	}

	originalHash := tx.Hash()

	payload, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("failed to marshal transaction: %v", err)
	}

	var decoded structures.Transaction
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("failed to unmarshal transaction: %v", err)
	}

	if decoded.Hash() != originalHash {
		t.Fatalf("expected transaction hash to remain the same after JSON round trip, got %s before and %s after", originalHash, decoded.Hash())
	}
}
