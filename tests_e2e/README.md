# Modulr E2E Harness

This directory contains a local process harness for running `modulr-core` and
`modulr-anchors-core` nodes as background OS processes during E2E testing.

The first milestone is intentionally small: the harness manages processes,
logs, and run state from a manifest. Network config generation and consensus
scenarios will be added on top of this foundation.

## Current Capabilities

- Generate a small local network layout for `N` core validators and `M` anchors.
- Write `configs.json`, `genesis.json`, `anchors.json`, and `core_genesis.json`.
- Produce a manifest that can be passed directly to `start`.
- Start multiple node processes from a JSON manifest.
- Wait for node HTTP health URLs before returning from `start`.
- Run the `bootstrap_smoke` scenario to verify a generated mini-network.
- Set `CHAINDATA_PATH` per node.
- Capture `stdout` and `stderr` logs per node.
- Store run metadata in `tests_e2e/runs/<run-id>/state.json`.
- Check process status.
- Print recent logs for a node.
- Stop the whole run with `SIGTERM`, then `SIGKILL` after a timeout.

## Commands

```bash
go run ./tests_e2e/harness prepare \
  -core 1 \
  -anchors 1 \
  -overwrite

go run ./tests_e2e/harness scenario bootstrap_smoke

go run ./tests_e2e/harness scenario alfp_pull_smoke

go run ./tests_e2e/harness scenario epoch_anchor_ack_smoke

go run ./tests_e2e/harness start \
  -manifest tests_e2e/runs/latest/manifest.json \
  -health-timeout 25s

go run ./tests_e2e/harness status

go run ./tests_e2e/harness logs \
  -node core-1 \
  -stream stdout \
  -lines 120

go run ./tests_e2e/harness stop
```

`prepare` writes generated node directories under `tests_e2e/runs/<run-id>/network/`.
Use `-core-command` and `-anchor-command` if you want to run prebuilt binaries
instead of `go run .`.
If you reuse a `-run-id`, pass `-overwrite` to remove the previous generated
chaindata first. Scenario commands do this automatically.

## Manifest Format

```json
{
  "name": "manual-local-run",
  "nodes": [
    {
      "name": "core-1",
      "role": "core",
      "repoPath": "/absolute/path/to/modulr-core",
      "workDir": "/absolute/path/to/core-1-chaindata",
      "chaindataPath": "/absolute/path/to/core-1-chaindata",
      "healthURL": "http://localhost:19000/live_stats",
      "command": ["go", "run", "."]
    }
  ]
}
```

Each `chaindataPath` must already contain the files required by the node:

- `modulr-core`: `configs.json`, `genesis.json`, `anchors.json`
- `modulr-anchors-core`: `configs.json`, `genesis.json`, `core_genesis.json`

Generated manifests set `workDir` to the node chaindata directory and use
`go run /absolute/path/to/repo` by default. This allows `modulr-core` to read
the generated `version.txt` from the node directory during local runs. Anchor
nodes keep `workDir` pointed at the `modulr-anchors-core` repository because
`go run .` must execute inside that module; their config still comes from
`CHAINDATA_PATH`.

If `healthURL` is present, `start` waits until the URL returns a non-5xx HTTP
response. Pass `-wait-health=false` to start processes without waiting.

## Scenarios

### `bootstrap_smoke`

```bash
go run ./tests_e2e/harness scenario bootstrap_smoke
```

This scenario prepares a `1 core + 1 anchor` network, starts both nodes, waits
for HTTP health checks, confirms that the core height advances, verifies that
the anchor remains healthy, and then stops the run. On failure it prints recent
logs for each node to make the runtime issue visible immediately.

### `alfp_pull_smoke`

```bash
go run ./tests_e2e/harness scenario alfp_pull_smoke
```

This scenario verifies the anchor-side proactive ALFP fallback. It prepares a
fast `1 core + 1 anchor` network, starts a local proxy that blocks only
`/accept_aggregated_leader_finalization_proof` POSTs from core to anchor, lets
all other anchor HTTP traffic pass through, and then waits until the anchor logs
show that it built an ALFP locally from the core quorum and included it in an
anchor block.

### `epoch_anchor_ack_smoke`

```bash
go run ./tests_e2e/harness scenario epoch_anchor_ack_smoke
```

This scenario verifies the core-to-anchor epoch rotation contract. It prepares a
fast `1 core + 1 anchor` network, waits until core collects and sends an
`AggregatedEpochRotationProof` for `0 -> 1`, verifies that anchors-core applies
that core quorum transition, and checks that core stores an
`AggregatedAnchorEpochAckProof` exposed via `/aggregated_anchor_epoch_ack_proof/1`
for the transition payload `0 -> 1`.

## Next Milestones

1. Add richer readiness checks for WS endpoints and expected runtime state.
2. Add consensus scenarios:
   - Recovery latest quorum collection from anchors.
   - Recovery restart smoke flow.
