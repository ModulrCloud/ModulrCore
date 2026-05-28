# AGENTS.md

Guidance for AI coding agents working in this repository.

## Project Shape

- This is the `modulr-core` Go repository.
- Keep changes aligned with existing packages: `threads` for real long-running loops, `utils` for shared helpers, `websocket_pack` for WS routes/RPC structs, `http_pack/routes` for HTTP handlers, and `structures` for shared data types.
- Do not create new top-level packages unless there is a clear architectural need and the existing packages do not fit.
- A file under `threads/` should represent thread-like behavior: a long-running loop or runtime worker. One-shot helpers should live elsewhere, usually `utils`.

## Tests

- Main unit/integration test command:

```bash
go test ./tests -count=1 -v
```

- Use `-count=1` when validating changes; cached test output is not enough.
- E2E scenarios live under `tests_e2e/` and are run with:

```bash
go run ./tests_e2e/harness scenario <name>
```

- Do not run E2E unless explicitly requested. E2E starts real local processes and writes under `tests_e2e/runs/`.

## Go Modules

- Do not modify `go.mod` or `go.sum` just because a local test command rewrote them.
- If Go dependency resolution fails in Cursor, check for a bad `GOMODCACHE` before changing module files.
- Prefer normal repository commands over ad hoc environment workarounds.

## Core Network Rules

- Recovery keeps durable `STATE`; network-specific DB directories can be reset during recovery flows.
- `Statistics` is observational and should not be used as a cursor or control pointer.
- `ChainCursor` and offset fields are the right place for recovery-aware execution/height accounting.
- Be careful with `LAST_MILE_HEIGHT_MAP`, `AggregatedHeightProof`, `AggregatedEpochRotationProof`, and epoch boundary logic: these are consensus-critical.

## Concurrency And Signing

- Never introduce signing paths that can double-sign conflicting data for the same logical ID.
- Existing finalization routes use per-creator mutexes plus first-write-wins vote records; preserve that pattern.
- For anchors/core epoch proofs, reject conflicting data for the same epoch/hash target rather than layering compatibility shims.

## Coding Style

- Keep changes narrowly scoped to the requested behavior.
- Prefer existing helpers and local patterns over new abstractions.
- Avoid unrelated refactors, formatting churn, or metadata changes.
- Run `gofmt` on changed Go files.
