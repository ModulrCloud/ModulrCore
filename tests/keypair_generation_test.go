package tests

import (
	"fmt"
	"testing"

	"github.com/modulrcloud/modulr-core/cryptography"
)

func TestGenerateKeyPair_PrintToConsole(t *testing.T) {
	// Passing empty mnemonic triggers generation of a new 24-word phrase.
	// NOTE: This prints the PRIVATE KEY to console. Don't run this test on shared CI logs.
	box := cryptography.GenerateKeyPair("", "", nil)

	// fmt output is shown with `go test -v ...` (recommended), and also captured by Go test output.
	fmt.Println("=== Modulr Keypair Generation ===")
	fmt.Printf("MNEMONIC: %s\n", box.Mnemonic)
	fmt.Printf("BIP44_PATH: %v\n", box.Bip44Path)
	fmt.Printf("PUBLIC_KEY: %s\n", box.Pub)
	fmt.Printf("PRIVATE_KEY: %s\n", box.Prv)
	fmt.Println("================================")

	// Basic sanity checks so test fails if generation broke.
	if box.Mnemonic == "" || box.Pub == "" || box.Prv == "" {
		t.Fatalf("generated keypair is incomplete: mnemonic=%t pub=%t prv=%t", box.Mnemonic != "", box.Pub != "", box.Prv != "")
	}
}


