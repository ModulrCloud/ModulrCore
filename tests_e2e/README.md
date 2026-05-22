# Modulr E2E Harness

This directory contains a local process harness for running `modulr-core` and
`modulr-anchors-core` nodes as background OS processes during E2E testing.

These tests are not unit tests or mocked integration tests: the harness
generates real local network configs, starts real node processes, captures logs,
and verifies behavior through HTTP endpoints plus runtime log evidence.

```text
                         go run ./tests_e2e/harness scenario <name>
                                           |
                                           v
┌─────────────────────────────────────────────────────────────────────────────┐
│                              E2E Harness                                    │
│                                                                             │
│  1. prepare run directory                                                   │
│  2. generate node configs                                                   │
│  3. spawn real background OS processes                                      │
│  4. verify runtime behavior                                                 │
└─────────────────────────────────────────────────────────────────────────────┘
          |                         |                         |
          v                         v                         v
┌───────────────────┐     ┌───────────────────┐     ┌───────────────────────┐
│ Generated Files   │     │ Background Nodes  │     │ Harness Checks        │
│                   │     │                   │     │                       │
│ runs/<id>/network │     │ core-1 ... core-N │<----│ HTTP health endpoints │
│ manifest.json     │     │ anchor-1 ... N    │<----│ recovery endpoints    │
│ configs/genesis   │     │ real PIDs         │<----│ process liveness      │
└───────────────────┘     └───────────────────┘     └───────────────────────┘
                                    |
                                    v
                          ┌───────────────────┐
                          │ Runtime Evidence  │
                          │                   │
                          │ logs/core-*.log   │
                          │ logs/anchor-*.log │
                          │ state.json        │
                          └───────────────────┘
                                    ^
                                    |
                          ┌───────────────────┐
                          │ Log Assertions    │
                          │                   │
                          │ epoch rotation    │
                          │ ALFP collection   │
                          │ recovery catch-up │
                          │ quorum signatures │
                          └───────────────────┘

Result: PASS, or diagnostics with the recent stdout/stderr logs for each node.
```

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

go run ./tests_e2e/harness scenario recovery_latest_quorum_smoke

go run ./tests_e2e/harness scenario multi_node_quorum_smoke

go run ./tests_e2e/harness scenario multi_node_one_anchor_down_smoke

go run ./tests_e2e/harness scenario multi_node_one_core_down_smoke

go run ./tests_e2e/harness scenario multi_node_alfp_pull_after_push_failure

go run ./tests_e2e/harness scenario multi_node_recovery_majority_latest_quorum

go run ./tests_e2e/harness scenario multi_node_lagging_anchor_catchup

go run ./tests_e2e/harness scenario multi_node_network_partition_no_false_majority

go run ./tests_e2e/harness scenario recovery_script_style

go run ./tests_e2e/harness scenario recovery_full_cycle_smoke

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

### `recovery_latest_quorum_smoke`

```bash
go run ./tests_e2e/harness scenario recovery_latest_quorum_smoke
```

This scenario verifies the recovery-facing anchors-core API. It prepares a fast
`1 core + 1 anchor` network, waits until anchors-core applies the core quorum
transition `0 -> 1`, calls `/recovery/latest_core_quorum`, verifies the outer
anchor signature over the raw payload, and checks that the signed payload points
to the latest core quorum proof for `0 -> 1` with validator endpoints.

### `multi_node_quorum_smoke`

```bash
go run ./tests_e2e/harness scenario multi_node_quorum_smoke
```

This scenario verifies the same contracts with a real quorum shape. By default
it prepares a fast `4 core + 4 anchors` network, waits for the core epoch
rotation `0 -> 1`, confirms every anchor applies that core quorum transition,
checks that core exposes an `AggregatedAnchorEpochAckProof` with at least anchor
majority signatures, and then restarts a majority of anchors in `RECOVERY_MODE`
to verify they each return a signed `/recovery/latest_core_quorum` response.

### `multi_node_one_anchor_down_smoke`

```bash
go run ./tests_e2e/harness scenario multi_node_one_anchor_down_smoke
```

This scenario verifies degraded anchor-quorum behavior. It starts a fast
`4 core + 4 anchors` network, stops one anchor before the first core epoch
rotation, verifies the remaining anchors still let core collect an
`AggregatedAnchorEpochAckProof` with majority `3/4` signatures, and then
restarts a majority of anchors in `RECOVERY_MODE` to confirm recovery latest
quorum responses are still available from enough anchors.

### `multi_node_one_core_down_smoke`

```bash
go run ./tests_e2e/harness scenario multi_node_one_core_down_smoke
```

This scenario verifies degraded core-quorum behavior. It starts a fast
`4 core + 4 anchors` network, stops one core validator before the first core
epoch rotation, verifies the remaining validators still produce the core epoch
rotation proof, confirms every anchor applies that transition, and then checks
that recovery latest quorum responses expose a core rotation proof with majority
`3/4` core signatures.

### `multi_node_alfp_pull_after_push_failure`

```bash
go run ./tests_e2e/harness scenario multi_node_alfp_pull_after_push_failure
```

This scenario verifies anchor-side ALFP recovery in a real quorum network. It
starts a fast `4 core + 4 anchors` network, rewrites all core configs so one
anchor is reached through a local proxy, blocks ALFP POST delivery to that
anchor, and then verifies the anchor proactively builds the missing ALFP from
the core quorum and includes it in an anchor block.

### `multi_node_recovery_majority_latest_quorum`

```bash
go run ./tests_e2e/harness scenario multi_node_recovery_majority_latest_quorum
```

This scenario verifies recovery latest-quorum convergence across an anchor
majority. It starts a fast `4 core + 4 anchors` network, waits until core and
anchors reach at least epoch `2`, stops the normal network, restarts only
majority `3/4` anchors in `RECOVERY_MODE`, and verifies that their signed
`/recovery/latest_core_quorum` responses agree on the same latest core
epoch/hash.

### `multi_node_lagging_anchor_catchup`

```bash
go run ./tests_e2e/harness scenario multi_node_lagging_anchor_catchup
```

This scenario verifies recovery in-memory catch-up for a lagging anchor. It
starts a fast `4 core + 4 anchors` network, waits until all anchors know epoch
`1`, snapshots one anchor, lets the full network advance to at least epoch `2`,
then restores the old anchor snapshot and restarts anchors in `RECOVERY_MODE`.
The lagging anchor must return a signed `/recovery/core_quorum/2` response
showing that its durable view started at epoch `1` and caught up in memory to
the target core quorum proof.

### `multi_node_network_partition_no_false_majority`

```bash
go run ./tests_e2e/harness scenario multi_node_network_partition_no_false_majority
```

This scenario verifies that recovery tooling cannot treat an anchor minority as
a valid recovery majority. It starts a fast `4 core + 4 anchors` network, waits
until all anchors know at least epoch `2`, stops the normal network, restarts
only `2/4` anchors in `RECOVERY_MODE`, and verifies that their individual
signed `/recovery/core_quorum/2` responses are valid but still below the
required `3/4` anchor majority.

### `recovery_script_style`

```bash
go run ./tests_e2e/harness scenario recovery_script_style
```

This scenario models the future recovery client flow. It starts a fast
`4 core + 4 anchors` network, snapshots one anchor after it has durable epoch
`1`, advances the full network to at least epoch `2`, restores the stale anchor
snapshot, and restarts exactly `3/4` anchors in `RECOVERY_MODE`. The harness then
queries `/recovery/latest_core_quorum` from all three recovery anchors and
verifies that the majority returns valid signed responses for the same latest
core quorum. The stale anchor must report `memory_catchup`, proving it caught up
to its peers at runtime and still participates in the recovery majority.

### `recovery_full_cycle_smoke`

```bash
go run ./tests_e2e/harness scenario recovery_full_cycle_smoke
```

This scenario verifies the recovery lifecycle past the script-style collection
step. It starts a fast `4 core + 4 anchors` network, waits until anchors know the
first core quorum transition (`0 -> 1` by default), stops the original network,
restarts an anchor majority in `RECOVERY_MODE`, and verifies that their signed
`/recovery/latest_core_quorum` responses agree. The harness then registers a
signed recovery plan in each core node's persistent `STATE`, switches core
genesis to a new recovery network id, restarts the recovered `4 core + 4 anchors`
network, and checks that core applies the recovery transition, avoids
`network id mismatch`, advances height, and collects a new anchor epoch ACK
proof.

## Planned Scenarios

These scenarios are the next E2E roadmap. They focus on longer live runtime,
process restarts, recovery drills, and failure modes that are difficult to prove
with unit or integration tests alone.

### `long_running_stability`

Run a `4 core + 4 anchors` network for many epochs, for example `10-20` fast
epochs. The scenario should verify that execution, approvement, and finalization
threads do not drift apart, stale ALFPs do not accumulate, and a majority of
anchors agree on the latest recovery core quorum at the end of the run.

### `restart_persistence`

Start a multi-node network, let it reach a stable later epoch, stop all
processes, and start the same run again from existing chaindata. The scenario
should verify that core and anchors continue from persisted state without
breaking network id checks, epoch cursors, proof storage, or DB layout
assumptions.

### `rolling_restarts`

Restart core validators and anchors one by one while the network is live. The
scenario should verify that temporary node restarts do not break quorum progress,
ALFP delivery or pull fallback, anchor ACK collection, or recovery-facing state.

### `bad_stale_proof_live_injection`

Inject stale or tampered proofs through a proxy or fixture endpoint during a live
run. The scenario should verify that nodes reject invalid runtime proofs and do
not persist or apply them, even when the proof arrives through a normal network
path.

### `network_latency_partial_failures`

Extend the harness proxy beyond hard blocking to simulate slow responses,
timeouts, HTTP 500 responses, and flaky delivery. The scenario should verify
that retry and backoff paths keep the network progressing and that no thread
waits forever on a partial failure.

### `state_divergence_detection`

Create or observe incompatible local views across core validators or anchors.
The scenario should verify that quorum logic does not merge incompatible views
into a false proof; the system should either converge through valid upgrade
paths or clearly refuse the conflicting state.

### `real_transaction_flow_across_epochs`

Submit real transactions before an epoch rotation, after an epoch rotation, and
after a restart. The scenario should verify that user-facing execution state
survives consensus transitions and restarts, not just that proof and health
threads keep running.

