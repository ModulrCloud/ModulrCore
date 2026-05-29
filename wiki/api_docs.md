# ModulrCore HTTP API

This document describes every HTTP endpoint registered in [`server.go`](../http_pack/server.go), the expected request inputs, and the response formats returned by the node.

## Native units

- Modulr native token uses fixed-point units.
- `1 coin = 1_000_000_000 units`.
- Fields such as `amount`, `fee`, `balance`, and `totalFees` are integer units (`uint64` semantics).
- UI/SDK should convert:
  - coin -> units for inputs
  - units -> coin for display

## Recovery mode

When `RECOVERY_MODE=true`, the node starts a read-only recovery HTTP API and does not start normal consensus/generation execution routes. In this mode, recovery endpoints are available only for the restart procedure, and the node is expected not to sign normal consensus/proof messages.

The recovery router exposes:

- `GET /recovery/last_finalized_height`
- `GET /recovery/genesis_template`
- `GET /get_validator_endpoints`
- `GET /get_validator_ws_endpoints`

Normal transaction submission and regular live node APIs are not part of the recovery router.

## Live statistics

### `GET /last_height`
Returns the latest executed block height tracked by the node.

- **Success (200)**: `{ "lastHeight": <int> }`.
- **Errors**
  - `404` — the node has not executed any blocks yet.
  - `500` — failed to marshal the response.

**Example request**
```bash
curl https://localhost:7332/last_height
```

**Example response**
```json
{ "lastHeight": 1024 }
```

### `GET /live_stats`
Returns a snapshot of the node's current view of the chain along with runtime parameters.

- **Success (200)**: JSON object with the following shape:
  - `statistics`: Aggregated chain metrics.
    - `lastHeight`: Absolute height of the latest executed block (starts at `-1` before any block is executed).
    - `lastBlockHash`: Hash of the latest executed block.
    - `totalTransactions`: Total number of transactions observed in executed blocks.
    - `successfulTransactions`: Transactions that passed validation and were applied.
    - `failedTransactions`: Transactions that failed validation.
    - `totalFees`: Total amount of fees collected across executed blocks.
  - `networkParameters`: Current [`structures.NetworkParameters`](../structures/network_parameters.go).
  - `epoch`: Current [`structures.EpochDataHandler`](../structures/epoch.go).
- **Errors**
  - `500` — failed to marshal response.

**Example request**
```bash
curl https://localhost:7332/live_stats
```

**Example response**
```json
{
  "statistics": {
    "lastHeight": 1024,
    "lastBlockHash": "00f9...",
    "totalTransactions": 18230,
    "successfulTransactions": 17980,
    "failedTransactions": 250,
    "totalFees": 941000
  },
  "networkParameters": {
    "epochDuration": 60000,
    "quorumSize": 5,
    "minimalStakePerStaker": 1000000,
    "validatorRequiredStake": 5000000,
    "leadersCount": 12
  },
  "epoch": {
    "id": 42,
    "hash": "2ad1...",
    "validatorsRegistry": ["validator_1", "validator_2"],
    "startTimestamp": 1714042385123,
    "quorum": ["validator_1", "validator_3", "validator_7", "validator_8", "validator_12"],
    "leadersSequence": ["validator_1", "validator_3", "validator_7"],
    "currentLeaderIndex": 1
  }
}
```

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
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    }
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
    "delayedTxsBatch": {
      "epochIndex": 42,
      "delayedTransactions": [],
      "proofs": {}
    }
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
- **Success (200)**: Object containing both the transaction and its receipt:
  - `tx`: [`structures.Transaction`](../structures/transaction.go).
  - `receipt`: [`structures.TransactionReceipt`](../structures/transaction.go).
    - `reason`: Empty string when `success=true`, otherwise contains the failure reason.
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
  "tx": {
    "v": 1,
    "from": "ed25519_sender...",
    "to": "ed25519_receiver...",
    "amount": 50000000,
    "fee": 1000,
    "sig": "aabbcc...",
    "nonce": 57,
    "payload": {
      "memo": "Invoice #582"
    }
  },
  "receipt": {
    "block": "42:ed25519_abcd...:0",
    "position": 3,
    "success": true,
    "reason": ""
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
- **Success (200)**: [`structures.Account`](../structures/account.go) with `balance`, `nonce`, `initiatedTransactions`, and `successfulInitiatedTransactions` counters for the sender's activity.
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
  "nonce": 12,
  "initiatedTransactions": 34,
  "successfulInitiatedTransactions": 30
}
```

## Validators

### `GET /validator/{validatorPubkey}`
Fetches validator storage (staking and metadata) from the state store.

- **Path parameters**
  - `validatorPubkey`: Validator public key string.
- **Success (200)**: [`structures.ValidatorStorage`](../structures/genesis.go).
- **Errors**
  - `400` — invalid validator pubkey.
  - `404` — validator not found.
  - `500` — storage read or JSON parse error.

**Example request**
```bash
curl https://localhost:7332/validator/6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE
```

**Example response**
```json
{
  "pubkey": "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE",
  "percentage": 10,
  "totalStaked": 1500000000,
  "stakers": {
    "staker_1_pubkey": 1000000000,
    "staker_2_pubkey": 500000000
  },
  "validatorURL": "https://validator.example.com",
  "wssValidatorURL": "wss://validator.example.com/ws"
}
```

### `GET /get_validator_endpoints`
Returns HTTP and WSS endpoints for a comma-separated list of validator public keys.

This endpoint is available in both normal mode and `RECOVERY_MODE`. During recovery, anchors and devops scripts use it to resolve validator URLs without relying on a local file.

- **Query parameters**
  - `pubkeys`: comma-separated validator public keys. The node caps the list size to avoid accidental oversized responses.
- **Success (200)**: Object keyed by validator pubkey. Each value contains:
  - `validatorUrl`: HTTP endpoint.
  - `wssValidatorUrl`: WebSocket endpoint.
- **Errors**
  - `400` — missing or empty `pubkeys` query parameter.

**Example request**
```bash
curl 'https://localhost:7332/get_validator_endpoints?pubkeys=6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE,9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK'
```

**Example response**
```json
{
  "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE": {
    "validatorUrl": "https://validator-1.example.com",
    "wssValidatorUrl": "wss://validator-1.example.com/ws"
  },
  "9GQ46rqY238rk2neSwgidap9ww5zbAN4dyqyC7j5ZnBK": {
    "validatorUrl": "https://validator-2.example.com",
    "wssValidatorUrl": "wss://validator-2.example.com/ws"
  }
}
```

### `GET /get_validator_ws_endpoints`
Returns only WSS endpoints for a comma-separated list of validator public keys.

This endpoint is available in both normal mode and `RECOVERY_MODE`. Anchors use it when they need WebSocket URLs for core quorum members.

- **Query parameters**
  - `pubkeys`: comma-separated validator public keys.
- **Success (200)**: Object keyed by validator pubkey, with WSS URL string values.
- **Errors**
  - `400` — missing or empty `pubkeys` query parameter.

**Example request**
```bash
curl 'https://localhost:7332/get_validator_ws_endpoints?pubkeys=6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE'
```

**Example response**
```json
{
  "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE": "wss://validator-1.example.com/ws"
}
```

## Recovery API

Recovery API endpoints are available only when the node is started with `RECOVERY_MODE=true`. Responses are signed by the responding node and wrapped in a common envelope:

```json
{
  "pubKey": "validator_pubkey",
  "payload": {},
  "signature": "base64_ed25519_signature"
}
```

The signature is generated over the raw JSON `payload` bytes. Recovery scripts verify the envelope against `pubKey` before using the payload.

### `GET /recovery/last_finalized_height`
Returns the node's latest finalized absolute height as a signed recovery response.

The recovery script queries the discovered latest core quorum and accepts only responses that can be verified and grouped into a core quorum majority.

- **Success (200)**: Signed envelope whose payload is:
  - `lastHeight`: latest finalized absolute height.
  - `blockId`: block id at that height.
  - `blockHash`: block hash.
  - `epochId`: epoch id for that finalized height.
  - `proof`: full `AggregatedHeightProof`, including quorum signatures.
- **Errors**
  - `404` — no finalized height or no aggregated height proof is available.
  - `500` — failed to marshal the signed payload.

**Example response**
```json
{
  "pubKey": "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE",
  "payload": {
    "lastHeight": 12048,
    "blockId": "42:6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE:18",
    "blockHash": "f7c0...",
    "epochId": 42,
    "proof": {
      "absoluteHeight": 12048,
      "blockId": "42:6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE:18",
      "blockHash": "f7c0...",
      "epochId": 42,
      "heightInEpoch": 18,
      "proofs": {
        "validator_1_pubkey": "base64_signature",
        "validator_2_pubkey": "base64_signature"
      }
    }
  },
  "signature": "MEUCIQ..."
}
```

### `GET /recovery/genesis_template`
Returns a signed template used by the devops recovery script to build the new restart genesis.

The template is derived from the node's current `APPROVEMENT_THREAD_METADATA`, because the approvement thread has the next validator registry and network parameters needed for the restart. The script requires a majority of the discovered core quorum to return the same template and checks that `sourceEpochId` and `sourceEpochHash` match the AERP selected from anchors.

- **Success (200)**: Signed envelope whose payload is:
  - `sourceEpochId`: epoch id of the template source.
  - `sourceEpochHash`: epoch hash of the template source.
  - `coreMajorVersion`: core major version to place into the generated genesis.
  - `networkParameters`: network parameters to place into the generated genesis.
  - `validators`: validator set to place into the generated genesis.
- **Errors**
  - `404` — no approvement thread metadata is available.
  - `500` — failed to load validator storage or marshal the signed payload.

**Example response**
```json
{
  "pubKey": "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE",
  "payload": {
    "sourceEpochId": 43,
    "sourceEpochHash": "9f8c6d...",
    "coreMajorVersion": 1,
    "networkParameters": {
      "VALIDATOR_REQUIRED_STAKE": 5000000,
      "MINIMAL_STAKE_PER_STAKER": 1000000,
      "QUORUM_SIZE": 5,
      "EPOCH_DURATION": 60000,
      "LEADERSHIP_DURATION": 15000,
      "BLOCK_TIME": 1000,
      "MAX_BLOCK_SIZE_IN_BYTES": 1048576,
      "TXS_LIMIT_PER_BLOCK": 1000
    },
    "validators": [
      {
        "pubkey": "6XvZpuCDjdvSuot3eLr24C1wqzcf2w4QqeDh9BnDKsNE",
        "percentage": 10,
        "totalStaked": 1500000000,
        "stakers": {},
        "validatorURL": "https://validator-1.example.com",
        "wssValidatorURL": "wss://validator-1.example.com/ws"
      }
    ]
  },
  "signature": "MEUCIQ..."
}
```

## Epoch data

### `GET /epoch_data/{epochIndex}`
Returns serialized epoch snapshot stored under `EPOCH_HANDLER:{epochIndex}`.

- **Path parameters**
  - `epochIndex`: Epoch number as a string.
- **Success (200)**: Raw JSON payload for the epoch snapshot: the latest `EpochDataHandler` persisted for the epoch plus the `networkParameters` used during that epoch.
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
  "startTimestamp": 1714042385123,
  "currentLeaderIndex": 3,
  "leadersSequence": [
    "ed25519_leader_0",
    "ed25519_leader_1",
    "ed25519_leader_2",
    "ed25519_leader_3"
  ],
  "quorum": [
    "ed25519_validator_0",
    "ed25519_validator_5",
    "ed25519_validator_8",
    "ed25519_validator_9",
    "ed25519_validator_12"
  ],
  "validatorsRegistry": [
    "ed25519_validator_0",
    "ed25519_validator_1",
    "ed25519_validator_2"
  ],
  "networkParameters": {
    "epochDuration": 60000,
    "quorumSize": 5,
    "minimalStakePerStaker": 1000000,
    "validatorRequiredStake": 5000000,
    "leadersCount": 12
  }
}
```