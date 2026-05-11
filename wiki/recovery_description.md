# Recovery Description

This document gives a short, schematic view of the recovery flow after a `modulr-core` network crash.

## Goal

After a crash, operators need to answer two questions:

1. What was the latest valid `modulr-core` epoch known by the network?
2. What was the latest finalized absolute height in that epoch?

Recovery uses `modulr-anchors-core` first, because anchors persist `AggregatedEpochRotationProof` data received from `modulr-core`. That proof tells us the latest known core epoch transition and the validator data needed to query the correct core quorum.

## Phase 1: Discover the Latest Core Epoch

The recovery script queries anchor nodes for their latest known core quorum data.

These queries are possible because every normal `modulr-core` epoch rotation is anchored first:

1. The core quorum builds an `AggregatedEpochRotationProof` (AERP) for epoch `N -> N+1`.
2. The core quorum sends that AERP to `modulr-anchors-core`.
3. Anchors persist the AERP and sign acknowledgements.
4. `modulr-core` waits for a majority of anchor acknowledgements.
5. Only after that majority ACK exists can the core network start sequencing blocks for epoch `N+1`.

This means the latest core epoch accepted by the running network must already be known by a majority of anchors.

```mermaid
sequenceDiagram
    participant Core as modulr-core quorum
    participant Anchors as modulr-anchors-core majority
    participant NextEpoch as Core epoch N+1

    Core->>Core: Build AERP (N -> N+1)
    Core->>Anchors: Send AERP
    Anchors->>Anchors: Persist AERP
    Anchors-->>Core: Return majority anchor ACKs
    Core->>Core: Build AggregatedAnchorEpochAckProof
    Core->>NextEpoch: Start sequencing epoch N+1
```

```mermaid
sequenceDiagram
    participant Script as Recovery script
    participant A1 as Anchor 1
    participant A2 as Anchor 2
    participant A3 as Anchor 3

    Script->>A1: GET /recovery/latest_core_quorum
    Script->>A2: GET /recovery/latest_core_quorum
    Script->>A3: GET /recovery/latest_core_quorum

    A1-->>Script: Signed latest AERP payload
    A2-->>Script: Signed latest AERP payload
    A3-->>Script: Signed latest AERP payload
```

The script verifies anchor signatures and groups responses by the reported `AggregatedEpochRotationProof`.

```mermaid
flowchart TD
    A["Anchor responses"]
    B["Verify anchor signatures"]
    C["Group by AERP<br/>NextEpochId + NextEpochHash"]
    D{"Majority agrees?"}
    E["Latest known core epoch data"]
    F["Recovery cannot continue safely"]

    A --> B --> C --> D
    D -- yes --> E
    D -- no --> F
```

The winning AERP gives the recovery process the latest known core epoch data:

- latest epoch id
- latest epoch hash
- next quorum
- next leaders sequence
- validator HTTP/WSS endpoints collected by anchors

Under the normal fault model, recovery should eventually be able to discover this data. Since `modulr-core` requires a majority of anchor ACKs before moving to the next epoch, and less than one third of anchors are assumed malicious, that majority contains at least one honest anchor. An honest anchor can provide a valid AERP chain, allowing lagging anchors to align their local recovery state and allowing the script to obtain a majority agreement on the latest epoch.

```mermaid
flowchart TD
    A["Core moved to epoch N+1"]
    B["Therefore: majority anchors<br/>ACKed AERP N -> N+1"]
    C["Fault model:<br/>< 1/3 malicious anchors"]
    D["At least one ACKing anchor<br/>is honest"]
    E["Honest anchor has valid AERP chain"]
    F["Lagging anchors can align"]
    G["Recovery script obtains<br/>majority agreement"]

    A --> B --> D
    C --> D
    D --> E --> F --> G
```

Example with 5 anchors:

```mermaid
flowchart TD
    subgraph Anchors["Anchor network: 5 nodes"]
        A1["Anchor 1<br/>honest<br/>valid AERP chain"]
        A2["Anchor 2<br/>honest<br/>valid AERP chain"]
        A3["Anchor 3<br/>honest<br/>valid AERP chain"]
        A4["Anchor 4<br/>honest<br/>valid AERP chain"]
        A5["Anchor 5<br/>malicious or stale"]
    end

    R["Recovery script queries<br/>majority = 4 anchors"]
    M["Any 4-of-5 response set<br/>contains at least 3 honest anchors"]
    V["Honest anchors provide<br/>valid AERP chain"]
    E["Script reaches the latest<br/>valid core epoch index"]

    R --> A1
    R --> A2
    R --> A3
    R --> A5

    A1 --> M
    A2 --> M
    A3 --> M
    A5 --> M
    M --> V --> E
```

Because the core network could only enter epoch `N+1` after a majority of anchors acknowledged the AERP for `N -> N+1`, at least one honest anchor in the queried majority can provide the valid proof path. With 5 anchors and fewer than one third malicious, a majority query gives enough honest responses to recover the valid AERP chain up to the latest accepted epoch.

## Phase 2: Query the Latest Core Quorum

Once the script knows the latest core quorum, it queries those validators for their latest finalized height. Each validator can return its local view of the latest height, but that height is only accepted if it is backed by a proof signed by the core quorum majority.

```mermaid
flowchart LR
    A["Winning AERP from anchors"]
    B["Extract latest core quorum"]
    C["Collect validator endpoints"]
    D["Query validators:<br/>/recovery/last_finalized_height"]
    E["Verify quorum-signed<br/>height proof"]
    F["Latest finalized height"]

    A --> B --> C --> D --> E --> F
```

The script tries all known HTTP endpoints for each validator until one succeeds.

```mermaid
sequenceDiagram
    participant Script as Recovery script
    participant V1 as Core validator 1
    participant V2 as Core validator 2
    participant V3 as Core validator 3

    Script->>V1: GET /recovery/last_finalized_height
    Script->>V2: GET /recovery/last_finalized_height
    Script->>V3: GET /recovery/last_finalized_height

    V1-->>Script: Height + quorum proof
    V2-->>Script: Height + quorum proof
    V3-->>Script: Height + quorum proof
```

The script verifies the quorum signatures inside each height proof and keeps only valid finalized heights. Since a valid height must be signed by a majority of the discovered core quorum, the script can reject unfinalized or fabricated answers and select the latest finalized height from the valid proofs.

```mermaid
flowchart TD
    A["Height responses from validators"]
    B["Extract height proof"]
    C["Discard invalid responses"]
    D["Verify majority signatures<br/>from latest core quorum"]
    E["Compare valid finalized heights"]
    F["Select maximum valid<br/>finalized height"]
    G["Recovery point:<br/>epoch Y, height X"]

    A --> B --> D --> C --> E --> F --> G
```

## Phase 3: Build the Restart Genesis

After the recovery script knows the recovery point `(Y, X)`, it asks the same discovered core quorum for a signed genesis template.

The template is not a full genesis file. It contains only the fields that must be carried into the restarted network:

- `CORE_MAJOR_VERSION`
- `NETWORK_PARAMETERS`
- `VALIDATORS`
- source epoch id and source epoch hash

The script verifies validator signatures and requires a core quorum majority to return the same template. It also checks that the template source matches the winning AERP from Phase 1.

```mermaid
flowchart TD
    A["Recovery point:<br/>epoch Y, height X"]
    B["Query discovered core quorum:<br/>/recovery/genesis_template"]
    C["Verify validator signatures"]
    D["Group identical templates"]
    E{"Majority agrees?"}
    F["Accepted genesis template"]
    G["Reject recovery artifact generation"]

    A --> B --> C --> D --> E
    E -- yes --> F
    E -- no --> G
```

The devops recovery script then builds the new genesis itself:

1. Generates a new `NETWORK_ID`.
2. Chooses a fresh `FIRST_EPOCH_START_TIMESTAMP`.
3. Copies `CORE_MAJOR_VERSION`, `NETWORK_PARAMETERS`, and `VALIDATORS` from the accepted template.
4. Omits `STATE` and `EVM_ALLOC`, because recovery preserves `STATE` from the old chain instead of seeding balances from genesis.

```mermaid
flowchart LR
    T["Majority genesis template"]
    N["Generate new NETWORK_ID"]
    S["Set FIRST_EPOCH_START_TIMESTAMP"]
    G["New restart genesis<br/>without STATE / EVM_ALLOC"]

    T --> G
    N --> G
    S --> G
```

## Phase 4: Produce a Team-Signed Recovery Artifact

The devops recovery script writes a `recovery.json` artifact for node operators. The artifact contains:

- `lastEpochIndex`: `Y`
- `lastAbsoluteHeight`: `X`
- `genesis`: the generated restart genesis
- `teamSig`: project-team signature

The signed payload is:

```text
RECOVERY_RESTART:<lastEpochIndex>:<lastAbsoluteHeight>:<blake3(canonicalGenesisJSON)>
```

```mermaid
flowchart TD
    A["Recovery point:<br/>Y, X"]
    B["Generated restart genesis"]
    C["Hash canonical genesis JSON"]
    D["Build payload:<br/>RECOVERY_RESTART:Y:X:genesisHash"]
    E["Sign with team private key"]
    F["Write recovery.json"]

    A --> D
    B --> C --> D --> E --> F
```

## Phase 5: Local Operator Registration

Each validator operator runs the local `modulr-core/scripts/recovery` command with the team-signed `recovery.json`.

The local script does not immediately rewrite `CHAIN_CURSOR` to the new network. Instead, it verifies the team signature and stores the recovery plan in `STATE`:

- `RECOVERY_DATA:<X>` -> signed recovery object
- `RECOVERY_ACTIVE` -> `X`

This lets the node start immediately with the new genesis while still knowing the exact old-chain height where execution must switch to the new era.

```mermaid
sequenceDiagram
    participant Operator
    participant LocalScript as local recovery script
    participant State as STATE DB

    Operator->>LocalScript: recovery.json
    LocalScript->>LocalScript: Verify team signature
    LocalScript->>LocalScript: Validate local cursor height
    LocalScript->>State: Put RECOVERY_DATA:X
    LocalScript->>State: Put RECOVERY_ACTIVE = X
```

## Phase 6: Runtime Transition

On startup, the node loads the normal `globals.GENESIS`, which is already the new restart genesis.

If `CHAIN_CURSOR.NetworkId` still points to the old network, the mismatch is allowed only when a valid active recovery plan exists in `STATE`. Until the execution thread reaches `lastAbsoluteHeight = X`, it continues executing old-era blocks and can read them from the old network-specific `BLOCKS` database.

When execution reaches height `X`, the recovery transition is applied atomically:

1. Store final epoch statistics for epoch `Y`.
2. Delete stale delayed transactions from future old-era epochs.
3. Patch `CHAIN_CURSOR.EpochOffset = Y + 1`.
4. Keep `CHAIN_CURSOR.Statistics.LastHeight = X`.
5. Replace cursor network data with the generated genesis data.
6. Build the new genesis epoch handler.
7. Stage new genesis validators/accounts when needed.
8. Remove `RECOVERY_ACTIVE` and `RECOVERY_DATA:<X>`.

After that, the node continues in the restarted network. For API and explorer consumers, history remains linear: the next block is absolute height `X+1`, and the next epoch is absolute epoch `Y+1`.

```mermaid
flowchart TD
    A["Node starts with new genesis"]
    B{"Cursor NetworkId<br/>matches genesis?"}
    C["Normal startup"]
    D{"Valid RECOVERY_ACTIVE<br/>plan exists?"}
    E["Continue old-era execution<br/>until height X"]
    F["Reject startup"]
    G{"LastHeight == X?"}
    H["Apply recovery transition"]
    I["Continue new era:<br/>height X+1, epoch Y+1"]

    A --> B
    B -- yes --> C
    B -- no --> D
    D -- no --> F
    D -- yes --> E --> G
    G -- no --> E
    G -- yes --> H --> I
```

## End-to-End Recovery View

```mermaid
flowchart TD
    A["Network crashed"]
    B["Query anchors for latest AERP"]
    C["Verify anchor signatures"]
    D["Select majority AERP"]
    E["Extract latest core quorum<br/>and validator endpoints"]
    F["Query core quorum for<br/>last finalized height"]
    G["Verify validator signatures"]
    H["Select majority height"]
    I["Recovery point:<br/>latest epoch Y, latest height X"]
    J["Query core quorum for<br/>genesis template"]
    K["Build new restart genesis"]
    L["Sign recovery.json<br/>with team key"]
    M["Operator stores recovery plan<br/>in STATE"]
    N["Node transitions at height X<br/>during runtime"]

    A --> B --> C --> D --> E --> F --> G --> H --> I
    I --> J --> K --> L --> M --> N
```

## Result

The recovery process produces a signed `recovery.json` artifact, not just two scalar values.

It contains:

- `Y`: the latest valid core epoch known through anchors.
- `X`: the latest finalized absolute height agreed by the latest core quorum.
- a generated restart genesis.
- a team signature over the recovery restart payload.

Operators register that artifact locally. The node then performs the cursor transition itself when execution reaches `X`.

After the transition, the new era begins at absolute epoch `Y+1` and absolute height `X+1`.
