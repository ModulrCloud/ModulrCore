# ModulrCore HTTP API

This document describes every HTTP endpoint registered in [`routes_root.go`](../routes_root.go), the expected request inputs, and the response formats returned by the node.

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
curl https://node.example.com/block/42:ed25519_abcd...:0
```

**Example response**
```json
{
  "creator": "ed25519_abcd...",
  "time": 1714042385123,
  "epoch": "9f8c6d#42",
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
curl https://node.example.com/height/1024
```

**Example response**
```json
{
  "creator": "ed25519_bf19...",
  "time": 1714042550123,
  "epoch": "af01d2#43",
  "transactions": [],
  "extraData": {
    "rest": {
      "comment": "Rotation"
    },
    "aefpForPreviousEpoch": {
      "lastLeader": 5,
      "lastIndex": 3,
      "lastHash": "98ab...",
      "hashOfFirstBlockByLastLeader": "42e1...",
      "proofs": {
        "ed25519_q1...": "b640..."
      }
    },
    "delayedTxsBatch": {
      "epochIndex": 43,
      "delayedTransactions": [],
      "proofs": {}
    },
    "aggregatedLeadersRotationProofs": {}
  },
  "index": 7,
  "prevHash": "ddaa...",
  "sig": "81ff..."
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
curl https://node.example.com/aggregated_finalization_proof/42:ed25519_abcd...:0
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
curl https://node.example.com/transaction/ab5f5cb2...
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
  -X POST https://node.example.com/transaction \
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
curl https://node.example.com/account/ed25519_receiver...
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
curl https://node.example.com/epoch_data/42
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
curl https://node.example.com/aggregated_epoch_finalization_proof/42
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

### `GET /first_block_assumption/{epochIndex}`
Returns information about the first block expected for the epoch.

- **Path parameters**
  - `epochIndex`: Epoch number as a string.
- **Success (200)**: [`structures.FirstBlockAssumption`](../structures/misc.go).
- **Errors**
  - `400` — invalid epoch index.
  - `404` — assumption record not found.

**Example request**
```bash
curl https://node.example.com/first_block_assumption/42
```

**Example response**
```json
{
  "indexOfFirstBlockCreator": 2,
  "afpForSecondBlock": {
    "prevBlockHash": "aa11...",
    "blockId": "42:ed25519_leader_2:1",
    "blockHash": "bb22...",
    "proofs": {
      "ed25519_validator_3": "ccee..."
    }
  }
}
```

### `GET /sequence_alignment`
Provides alignment data for the current epoch, or an error message if the node cannot serve it yet.

- **Success (200)**
  - `AlignmentData` payload with:
    - `proposedIndexOfLeader`: Current leader index.
    - `firstBlockByCurrentLeader`: [`block_pack.Block`](../block_pack/block.go) object.
    - `afpForSecondBlockByCurrentLeader`: [`structures.AggregatedFinalizationProof`](../structures/proofs.go).
  - or `{"err":"..."}` when data is unavailable (`Try later`, `No first block`, `No AFP for second block`).

**Example request**
```bash
curl https://node.example.com/sequence_alignment
```

**Example success response**
```json
{
  "proposedIndexOfLeader": 3,
  "firstBlockByCurrentLeader": {
    "creator": "ed25519_leader_3",
    "time": 1714042800456,
    "epoch": "9f8c6d#42",
    "transactions": [],
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
    "prevHash": "c0ffee...",
    "sig": "feed01..."
  },
  "afpForSecondBlockByCurrentLeader": {
    "prevBlockHash": "c0ffee...",
    "blockId": "42:ed25519_leader_3:1",
    "blockHash": "deadbeef...",
    "proofs": {
      "ed25519_validator_4": "d3f1..."
    }
  }
}
```

**Example error response**
```json
{
  "err": "Try later"
}
```

### `POST /epoch_proposition`
Handles epoch-rotation propositions submitted by leaders.

- **Request body**: [`structures.EpochFinishRequest`](../structures/epoch_finish_proposition.go).
- **Method restrictions**: Requests with any method other than `POST` receive HTTP 405.
- **Success (200)**
  - `EpochFinishResponseOk` — `{ "status": "OK", "sig": "..." }` when the proposition is confirmed.
  - `EpochFinishResponseUpgrade` — `{ "status": "UPGRADE", "currentLeader": <int>, "lastBlockProposition": { ... } }` when an updated proposal is required.
- **Errors (200)**: JSON object with `err` (e.g. `"Wrong format"`, `"Too early"`, `"Try later"`, `"Can't verify hash"`).

**Example request**
```bash
curl \
  -X POST https://node.example.com/epoch_proposition \
  -H 'Content-Type: application/json' \
  -d '{
        "currentLeader": 3,
        "afpForFirstBlock": {
          "prevBlockHash": "aa11...",
          "blockId": "42:ed25519_leader_3:0",
          "blockHash": "bb22...",
          "proofs": {"ed25519_validator_1": "ccee..."}
        },
        "lastBlockProposition": {
          "index": 11,
          "hash": "deadbeef...",
          "afp": {
            "prevBlockHash": "aa11...",
            "blockId": "42:ed25519_leader_3:1",
            "blockHash": "bb33...",
            "proofs": {"ed25519_validator_2": "ddee..."}
          }
        }
      }'
```

**Example success response**
```json
{
  "status": "OK",
  "sig": "9f77..."
}
```

**Example upgrade response**
```json
{
  "status": "UPGRADE",
  "currentLeader": 4,
  "lastBlockProposition": {
    "index": 12,
    "hash": "feedface...",
    "afp": {
      "prevBlockHash": "cc44...",
      "blockId": "42:ed25519_leader_4:1",
      "blockHash": "dd55...",
      "proofs": {
        "ed25519_validator_5": "f1f1..."
      }
    }
  }
}
```

**Example error response**
```json
{
  "err": "Too early"
}
```