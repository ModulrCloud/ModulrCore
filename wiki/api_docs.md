# ModulrCore HTTP API

This document describes every HTTP endpoint registered in [`server.go`](../http_pack/server.go), the expected request inputs, and the response formats returned by the node.

## Blocks

### `GET /block/{id}`
Retrieves a block by its unique identifier.

- **Path parameters**
  - `id`: Block identifier, e.g. `<epochIndex>:<leaderPublicKey>:<blockIndex>`.
- **Success (200)**: JSON serialization of [`block_pack.Block`](../block_pack/block.go).
- **Errors**
  - `400` — parameter is missing or not a string.
  - `404` — block is not found.

**Example request**
```bash
curl https://localhost:7332/block/0:9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK:30
```

**Example response**
```json
{
  "creator": "9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK",
  "time": 1714042385123,
  "epoch": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef#0",
  "transactions": [
    {
      "v": 1,
      "type": "transfer",
      "from": "ed25519_sender...",
      "to": "ed25519_receiver...",
      "amount": 125000000,
      "fee": 1000,
      "sig": "7c1a...",
      "nonce": 56,
      "payload": {
        "memo": "Payout"
      }
    }
  ],
  "extraData": {
    "rest": {},
    "aefpForPreviousEpoch": null,
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    },
    "aggregatedLeadersRotationProofs": {}
  },
  "index": 0,
  "prevHash": "00f9...",
  "sig": "4d2e..."
}
```

### `GET /height/{absoluteHeightIndex}`
Loads a block by its absolute height.

- **Path parameters**
  - `absoluteHeightIndex`: Block height as a base-10 string.
- **Success (200)**: Same response body as `GET /block/{id}`.
- **Errors**
  - `400` — parameter is empty or not a valid integer.
  - `404` — the block height is unknown.
  - `500` — internal error while reading from storage.

**Example request**
```bash
curl https://localhost:7332/height/1024
```

**Example response**
```json
{
  "creator": "9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK",
  "time": 1714042385123,
  "epoch": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef#0",
  "transactions": [
    {
      "v": 1,
      "type": "transfer",
      "from": "ed25519_sender...",
      "to": "ed25519_receiver...",
      "amount": 125000000,
      "fee": 1000,
      "sig": "7c1a...",
      "nonce": 56,
      "payload": {
        "memo": "Payout"
      }
    }
  ],
  "extraData": {
    "rest": {},
    "aefpForPreviousEpoch": null,
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    },
    "aggregatedLeadersRotationProofs": {}
  },
  "index": 0,
  "prevHash": "00f9...",
  "sig": "4d2e..."
}
```

### `GET /aggregated_finalization_proof/{blockId}`
Returns the aggregated finalization proof for a block.

- **Path parameters**
  - `blockId`: Identifier of the block whose proof should be returned.
- **Success (200)**: [`structures.AggregatedFinalizationProof`](../structures/proofs.go).
- **Errors**
  - `400` — invalid parameter value.
  - `404` — proof not found.

**Example request**
```bash
curl https://localhost:7332/aggregated_finalization_proof/0:9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK:30
```

**Example response**
```json
{
  "prevBlockHash": "ddaa...",
  "blockId": "42:ed25519_abcd...:0",
  "blockHash": "f7c0...",
  "proofs": {
    "ed25519_validator_1": "f31b...",
    "ed25519_validator_8": "a912..."
  }
}
```

### `GET /transaction/{hash}`
Finds a transaction by its hash.

- **Path parameters**
  - `hash`: Hex-encoded transaction hash.
- **Success (200)**: [`structures.Transaction`](../structures/transaction.go).
- **Errors**
  - `400` — missing or invalid hash.
  - `404` — transaction does not exist.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/transaction/ab5f5cb2...
```

**Example response**
```json
{
  "v": 1,
  "type": "transfer",
  "from": "ed25519_sender...",
  "to": "ed25519_receiver...",
  "amount": 50000000,
  "fee": 1000,
  "sig": "aabbcc...",
  "nonce": 57,
  "payload": {
    "memo": "Invoice #582"
  }
}
```

### `POST /transaction`
Accepts a transaction into the mempool or forwards it to the current leader.

- **Request body**: [`structures.Transaction`](../structures/transaction.go). Required fields are `from`, `nonce`, and `sig`; the remaining fields are validated by the node.
- **Success (200)**
  - `{"status":"OK"}` — transaction enqueued locally.
  - `{"status":"Ok, tx redirected to current leader"}` — forwarded when this node is not the leader.
- **Errors**
  - `400` — invalid JSON or missing required fields (`{"err":"Invalid JSON"}` / `{"err":"Event structure is wrong"}`).
  - `429` — mempool is full (`{"err":"Mempool is fullfilled"}`).
  - `500` — failed to forward the request to the leader (`{"err":"Impossible to redirect to current leader"}`).

**Example request**
```bash
curl \
  -X POST https://localhost:7332/transaction \
  -H 'Content-Type: application/json' \
  -d '{
        "v": 1,
        "type": "transfer",
        "from": "ed25519_sender...",
        "to": "ed25519_receiver...",
        "amount": 50000000,
        "fee": 1000,
        "sig": "aabbcc...",
        "nonce": 57,
        "payload": {"memo": "Invoice #582"}
      }'
```

**Example success response**
```json
{
  "status": "OK"
}
```

**Example error response**
```json
{
  "err": "Mempool is fullfilled"
}
```

## Accounts

### `GET /account/{accountId}`
Fetches account state from the LevelDB-backed state store.

- **Path parameters**
  - `accountId`: Account identifier (public key string).
- **Success (200)**: [`structures.Account`](../structures/account.go) with `balance` and `nonce`.
- **Errors**
  - `400` — invalid account identifier.
  - `404` — account not found.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/account/6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE
```

**Example response**
```json
{
  "balance": 275000000,
  "nonce": 12
}
```

## Epoch data

### `GET /epoch_data/{epochIndex}`
Returns serialized epoch handler state stored under `EPOCH_HANDLER:{epochIndex}`.

- **Path parameters**
  - `epochIndex`: Epoch number as a string.
- **Success (200)**: Raw JSON payload previously stored for the epoch (often an `EpochDataHandler`).
- **Errors**
  - `400` — invalid epoch index.
  - `404` — no data for the requested epoch.

**Example request**
```bash
curl https://localhost:7332/epoch_data/42
```

**Example response**
```json
{
  "id": 42,
  "hash": "9f8c6d",
  "currentLeaderIndex": 3,
  "leadersSequence": [
    "ed25519_leader_0",
    "ed25519_leader_1",
    "ed25519_leader_2",
    "ed25519_leader_3"
  ]
}
```

### `GET /aggregated_epoch_finalization_proof/{epochIndex}`
Reads the aggregated epoch finalization proof stored under `AEFP:{epochIndex}`.

- **Path parameters**
  - `epochIndex`: Epoch number as a string.
- **Success (200)**: [`structures.AggregatedEpochFinalizationProof`](../structures/proofs.go).
- **Errors**
  - `400` — invalid epoch index.
  - `404` — proof not found.

**Example request**
```bash
curl https://localhost:7332/aggregated_epoch_finalization_proof/42
```

**Example response**
```json
{
  "lastLeader": 3,
  "lastIndex": 11,
  "lastHash": "aa99...",
  "hashOfFirstBlockByLastLeader": "bb10...",
  "proofs": {
    "ed25519_validator_2": "0f41...",
    "ed25519_validator_7": "98ac..."
  }
}
```
