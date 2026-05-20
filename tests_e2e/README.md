# Modulr E2E Harness

This directory contains a local process harness for running `modulr-core` and
`modulr-anchors-core` nodes as background OS processes during E2E testing.

The first milestone is intentionally small: the harness manages processes,
logs, and run state from a manifest. Network config generation and consensus
scenarios will be added on top of this foundation.

## Current Capabilities

- Start multiple node processes from a JSON manifest.
- Set `CHAINDATA_PATH` per node.
- Capture `stdout` and `stderr` logs per node.
- Store run metadata in `tests_e2e/runs/<run-id>/state.json`.
- Check process status.
- Print recent logs for a node.
- Stop the whole run with `SIGTERM`, then `SIGKILL` after a timeout.

## Commands

```bash
go run ./tests_e2e/harness start \
  -manifest tests_e2e/manifests/example.json

go run ./tests_e2e/harness status

go run ./tests_e2e/harness logs \
  -node core-1 \
  -stream stdout \
  -lines 120

go run ./tests_e2e/harness stop
```

## Manifest Format

```json
{
  "name": "manual-local-run",
  "nodes": [
    {
      "name": "core-1",
      "role": "core",
      "repoPath": "/absolute/path/to/modulr-core",
      "chaindataPath": "/absolute/path/to/core-1-chaindata",
      "command": ["go", "run", "."]
    }
  ]
}
```

Each `chaindataPath` must already contain the files required by the node:

- `modulr-core`: `configs.json`, `genesis.json`, `anchors.json`
- `modulr-anchors-core`: `configs.json`, `genesis.json`, `core_genesis.json`

## Next Milestones

1. Generate temporary node directories and config files for `N` core validators
   and `M` anchors.
2. Add health checks that wait for HTTP/WS endpoints to become ready.
3. Add scenario commands, starting with `network_bootstrap_smoke`.
4. Add consensus scenarios:
   - ALFP fallback from anchors to direct core quorum polling.
   - Epoch rotation requiring anchor majority ACK.
   - Recovery latest quorum collection from anchors.
   - Recovery restart smoke flow.
