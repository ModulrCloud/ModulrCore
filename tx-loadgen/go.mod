module github.com/modulrcloud/modulr-tx-loadgen

go 1.23.1

require (
    github.com/btcsuite/btcutil v1.0.2 // indirect
    github.com/klauspost/cpuid/v2 v2.0.9 // indirect
    github.com/modulrcloud/modulr-core v0.0.0
    github.com/tyler-smith/go-bip32 v1.0.0 // indirect
    github.com/tyler-smith/go-bip39 v1.1.0 // indirect
    lukechampine.com/blake3 v1.4.0
)

replace github.com/modulrcloud/modulr-core => ../

replace golang.org/x/crypto => golang.org/x/crypto v0.37.0
replace golang.org/x/net => golang.org/x/net v0.39.0
