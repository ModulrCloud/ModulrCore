# Test Coverage

This document describes the critical recovery and consensus flows covered by tests.

## Recovery

- Recovery HTTP routes expose the data needed by recovery tooling. Tests verify that `last_finalized_height` and `genesis_template` return signed payloads, so an external recovery script can validate that the response really came from the expected core validator.

- Recovery HTTP routes reject invalid or incomplete local state. Tests cover cases where the node cannot safely produce recovery data yet, instead of returning a misleading successful response.

- Recovery data is accepted only when the team signature matches the exact recovery payload. Tests sign a valid recovery plan, then tamper with the height, epoch, genesis, and team pubkey to ensure any mismatch is rejected.

- Active recovery data is loaded from durable state. Tests write `RECOVERY_ACTIVE` and `RECOVERY_DATA:<height>` into the state database and verify that the node loads the exact registered recovery plan back.

- `ApplyRecoveryTransition` applies a recovery plan atomically. Tests verify that the chain cursor moves to the recovered network, validators and genesis accounts are staged, old recovery markers are removed, and delayed transactions past the recovery boundary are cleaned up.

- Recovery transition refuses unsafe local state. Tests verify that a recovery plan is rejected when the local cursor height does not match the recovery height.

## Leader Finalization

- `get_leader_finalization_proof` does not sign the currently active last leader too early. Tests verify that the route returns `NOT_READY` while the leader can still produce blocks.

- `get_leader_finalization_proof` signs completed leader state. Tests create a completed leader voting stat and verify that the route returns an `OK` response with a signature over the expected leader finalization payload.

- `get_leader_finalization_proof` supports `UPGRADE`. Tests send stale skip data and verify that the validator responds with its higher local voting stat, so callers can converge instead of finalizing an outdated leader state.

## Proof Verification

- Aggregated finalization proof verification checks quorum signatures over the exact block payload. Tests verify that tampering with the block hash or replacing quorum signatures with non-quorum signatures makes the proof invalid.

- Aggregated leader finalization proof verification checks both layers: the embedded AFP and the leader finalization signatures. Tests verify that tampered voting stats or wrong epochs are rejected.

- Aggregated height proof verification binds an absolute height to a specific block tuple. Tests verify that changing the height after signing invalidates the proof.

- Aggregated epoch rotation proof verification protects epoch transitions. Tests verify that the next epoch must be sequential, the epoch data hash must match the signed data, and quorum signatures must be valid.

- Aggregated anchor epoch ack proof verification requires an anchor majority. Tests verify that non-anchor signatures cannot satisfy the anchors quorum.
