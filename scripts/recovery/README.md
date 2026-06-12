# Recovery Operator Notes

This document describes the current recovery model for operators and agents. The
implementation supports one active recovery plan at a time.

## Core Model

- `genesis.json` must describe the next active network, not the historical one.
- `recovery.json` / `RECOVERY_DATA:<height>` describes the transition from the
  current execution network to that `genesis.json` network.
- The execution thread catches up to `lastAbsoluteHeight` and applies the
  recovery transition at that exact height.
- Consensus threads (`APPROVEMENT`, block generation, last-mile finalizer) start
  from the loaded `genesis.json`.
- Durable `STATE` is preserved across recovery; network-scoped DB directories
  (`BLOCKS`, `APPROVEMENT_THREAD_METADATA`, `FINALIZATION_THREAD_METADATA`,
  `EPOCH_DATA`) are per-network.

## Multiple Recoveries

If a node starts from scratch and the chain has already gone through multiple
recoveries, apply them one by one.

Example history:

1. Network A starts from `genesisA.json`.
2. Network A stops; operators create `recoveryA.json` and `genesisB.json`.
3. Network B stops; operators create `recoveryB.json` and `genesisC.json`.
4. Network C is the current active network.

Operator sequence for a node syncing from the beginning:

1. Start with `genesisA.json` and no active recovery plan.
2. Let execution sync network A up to `recoveryA.lastAbsoluteHeight`.
3. Stop the node.
4. Replace `genesis.json` with `genesisB.json`.
5. Register `recoveryA.json` in `STATE`:
   - `RECOVERY_DATA:<recoveryA.lastAbsoluteHeight> = recoveryA.json`
   - `RECOVERY_ACTIVE = <recoveryA.lastAbsoluteHeight>`
6. Start the node and let it apply recovery A -> B.
7. Let execution sync network B up to `recoveryB.lastAbsoluteHeight`.
8. Stop the node.
9. Replace `genesis.json` with `genesisC.json`.
10. Register `recoveryB.json` as the active recovery plan.
11. Start the node and let it apply recovery B -> C.
12. Continue syncing/running on network C.

Do not skip directly from `genesisA.json` to `genesisC.json` with
`recoveryA.json`. The active `genesis.json` must match the active recovery plan's
target genesis.

## Delayed Transactions During Recovery

Recovery clears pending delayed transactions at the transition boundary:

- pending `stake` operations are refunded by returning the staked `amount` to
  the staker account;
- pending `unstake`, `createValidator`, `updateValidator`, and `votingAccept`
  operations are dropped;
- delayed transaction keys are removed so the recovered network starts with a
  clean delayed-operation queue.

Delayed transaction keys are network-scoped:

```text
DELAYED_TRANSACTIONS:<networkId>:<absoluteEpoch>
```

This prevents the recovered network's block generation path from reading delayed
transactions that belong to a previous network while execution is still catching
up to the recovery boundary.
