# Modulr load generator

A small helper utility for the Modulr test network that periodically sends a batch of pre-signed transactions to a node, simulating user activity. It also exposes an optional faucet endpoint for sending test tokens from pre-funded accounts to user-provided addresses.

## Setup

1. Copy `config.example.json` to `config.json` and adjust parameters if needed: node URL, send interval, fee/amount, list of senders and recipients.
2. In `recipients`, specify addresses from your genesis. The example uses keys and balances from `templates/testnet_1/genesis.json` and matching private keys from `templates/testnet_*/configs_for_nodes`.

## Run

```bash
cd tx-loadgen

# Download dependencies and build the utility
go mod tidy

go run . -config ./config.json
```

Every `tickMs` milliseconds the utility fetches the latest nonce for each sender via the HTTP endpoint `/account/{address}`, forms `transactionsPerSender` transactions, signs them with the Ed25519 key (PKCS#8, base64), and sends them to `POST /transaction` on the configured node.

If the faucet is enabled, an HTTP server starts (default `:8080`) exposing `POST /faucet` that accepts JSON payloads like `{ "address": "<recipient>", "amount": 123 }` (optional `payload` can override the faucet payload). Each request submits a signed transaction from the configured faucet sender to the requested address.

## Configuration

* `nodeUrl` — HTTP address of the node.
* `tickMs` — send interval in milliseconds.
* `transactionsPerSender` — how many transactions are sent from each sender per tick.
* `requestTimeoutMs` — HTTP client timeout.
* `defaultPayload` — optional payload used if the sender has no custom payload.
* `faucet` — optional section enabling the faucet server:
  * `enabled` — set to true to start the faucet server.
  * `listen` — address/port to bind (default `:8080`).
  * `senderName` — name of the sender (from `senders`) used for faucet transfers.
  * `payload` — optional payload applied to faucet transactions (can be overridden per request).
* `senders` — array of senders with fields `publicKey`, `privateKey`, `recipients`, `amount`, `fee`, `version`, and optional `payload`.
