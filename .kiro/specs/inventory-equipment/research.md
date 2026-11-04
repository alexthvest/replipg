# Research & Design Decisions

## Summary

- **Feature**: `inventory-equipment`
- **Discovery Scope**: Extension; integration-focused light discovery of the existing runtime and character implementation.
- **Key Findings**:
  - All gameplay changes can share caller-owned `DBTX`; character reads can normalize recovery and healing accepts a full-health no-op, requiring inventory-specific rejection and operation isolation.
  - Runtime registration exposes no pre-admission database preparation hook. Catalog compatibility therefore requires a small generic startup seam, without importing inventory into runtime.
  - Ephemeral inventory navigation must send refreshed restricted responses; existing contracts reject replacing an ephemeral callback message. Permanent operation receipts and short-lived opaque controls have different retention needs.
- **Steering limitation**: Core `product.md`, `tech.md` and `structure.md` are absent. Existing `roadmap.md`, `development-workflow.md`, AGENTS.md, requirements and source are the project context; no replacement steering is invented.

## Research Log

### Transaction and Character Coordination

- **Context**: A consumed healing item must not be spent without healing, nor heal twice after retries.
- **Sources Consulted**: `internal/runtimecontract/database.go`, `internal/persistence/tx.go`, `internal/interaction/dispatch.go`, `internal/character/service.go`, `internal/character/contract/types.go`, `internal/character/contract/errors.go`, `internal/character/coordination_test.go`.
- **Findings**: Immediate transactions serialize writers; handlers receive one transaction capability; mutations and processed-update receipts commit before immediate delivery. Character Lookup/Ensure can write due recovery. ApplyHealth caps healing, refuses pending incapacitation, and records accepted no-ops. Matching character replay returns current state and original application revision.
- **Implications**: Inventory checks its own receipt before quantity and eligibility. It rejects full-health consumption explicitly and derives a separate bounded character receipt identity. Operation-local savepoints undo tentative character recovery or partial initialization on domain rejection; outer battle/runtime commit remains authoritative. Storage errors, including scan/close/cleanup failures, must retain runtime storage classification.

### Composition, Migration and Dependency Direction

- **Context**: Saved item identities must remain valid before any inventory operation is admitted.
- **Sources Consulted**: `internal/app/runtime.go`, `internal/app/composition.go`, `cmd/replipg/character.go`, `internal/persistence/migrate.go`, `internal/persistence/migrations/0002_characters.sql`, `internal/architecture/boundaries_test.go`.
- **Findings**: Options.Register receives Registry/Outbox/Scheduler only. Migrations execute before registrations and readiness. Migration lists are explicitly embedded and ordered. Gameplay can import exact character contract but not character implementation; current rules do not allow future battle imports of inventory contract.
- **Implications**: Add optional generic Options.Prepare in a bounded caller-owned startup transaction. Add migration 0003 to both lists; do not rewrite prior migrations. Extend import checks with exact inventory-contract allowances and restrictions. Share the character service instance through command composition and a consumer-owned interface.

### Personal Interaction and Control Lifetimes

- **Context**: Inventory contains navigation and mutating buttons, unlike the current read-only character command.
- **Sources Consulted**: `internal/character/commands.go`, `internal/interaction/events.go`, `internal/runtimecontract/responses.go`, `internal/telegram/mapper.go`, `internal/telegram/ephemeral.go`, `internal/devsmoke/register.go`.
- **Findings**: Initial personal commands need a human actor, supported group, incoming ephemeral identity and unexpired source timestamp. Callbacks carry actor/chat/query identity and opaque data, but no separate actor-bot metadata. Ephemeral callback replacement is forbidden. Message text is validated at 4096 UTF-16 units. Runtime handles invisible callback acknowledgement.
- **Implications**: Generate server-bound random controls only for authorized humans, bind them to owner and group, check revision/expiry, and derive mutation identity from the token. Use refreshed restricted responses and bounded pages. Do not expose item quantities/effects in callback data or add public/private delivery fallback.

### Product Decisions and Incremental Delivery

- **Context**: The brief left starter supplies, slots and initial effects open.
- **Sources Consulted**: User confirmations in this session, `requirements.md`, `brief.md`, `.kiro/steering/roadmap.md`, `.kiro/steering/development-workflow.md`, `internal/character/development.go`, README.md.
- **Findings**: User selected automatic character creation with one-time starter kit; one attack weapon and one defense armor with replacement; fixed-HP self-healing without use at full HP or during incapacitation. Battle remains responsible for permission and loot. Steering requires runnable milestones, automated checks, Telegram demonstration and reproduction instructions.
- **Implications**: Implement inventory inspection and starter before equipment, then composable healing and consistency tests. Supply opt-in self-only development grant/consume probes so verification does not wait for battles. Record real manual observations separately from planned instructions.

### Skills and Local Verification

- **Sources Consulted**: `kiro-spec-quick`, `kiro-spec-design` and its light discovery, design principles, synthesis and review-gate rules; `kiro-verify-completion`.
- **Guidance Applied**: Interactive phase approvals; explicit boundaries and concrete file plan; requirement traceability; draft-before-write gate; evidence-limited completion claims. A fresh explorer examined code seams and a separate fresh reviewer checks the design draft.
- **Scope**: No new third-party dependency or external service contract is introduced. Repository-pinned versions are reported from go.mod/devenv, without asserting latest-release or external security verification. No web research is necessary for these local integration decisions. No Go implementation or runtime test is claimed in this documentation phase.

### Approved Compact UI and Inert Items

- **Context**: The user approved a numbered `Inventory • <owner>` list and clarified that Coin is a dummy item rather than currency.
- **Decision**: Add the closed `inert` item category with ordinary stacking, zero combat/healing fields and no equip/use actions. No economy, spending, or new balance model is added.
- **Presentation**: Numbered `Item (quantity)` rows, equipped marker, weapon/armor summary, numbered selection buttons, item details and Back; Previous/Next when paginated. Example names and amounts are illustrative.
- **Integration**: Existing mapped events lack usernames. Add optional transient actor-label metadata through the existing mapper/identity seam for command and callback headers; fall back to `you` and retain identifier-based authorization. No inventory profile storage is introduced.
- **Validation**: Extend catalog constraints, inert-item behavior, numbered navigation and label privacy tests; retain the existing transaction, replay and starter semantics.

## Architecture Pattern Evaluation

| Option | Strengths | Risks / Limitations | Decision |
|---|---|---|---|
| Extend existing feature/service pattern with caller transactions | Fits character/runtime design; atomic cross-feature work; minimal operational change | Requires disciplined failure propagation | Selected |
| Let battle or handlers write inventory tables | Fewer initial methods | Duplicates ownership, replay and consumption invariants | Rejected |
| New inventory worker or external service | Independent scheduling/scaling | Unneeded lifecycle and distributed consistency | Rejected |

## Design Decisions

### Decision: One Aggregate and One Mutation Receipt Model

- **Context**: Starter supplies, loot grants, equipment callbacks and consumption all require replay-safe changes.
- **Alternatives Considered**: Separate receipt implementations per workflow; one canonical operation ledger plus starter marker.
- **Selected Approach**: One inventory root/revision, canonical typed request equality and permanent operation receipts. Starter uses a deterministic private operation identity; controls use random persistent tokens.
- **Rationale**: Generalizes replay semantics while keeping effect implementation limited to fixed self-healing.
- **Trade-offs**: Receipts grow permanently; retention needs a future bounded-replay policy.
- **Follow-up**: Verify matching replay after last-copy consumption, later state changes and restart; grant-before-first-open preserves starter eligibility.

### Decision: Immutable Mechanical Catalog

- **Context**: Renaming/rebalancing IDs can invalidate ownership and historical effects.
- **Alternatives Considered**: Silent in-place mechanics update; per-owned-instance versioning; saved mechanical registry.
- **Selected Approach**: Persist registered category/bonuses/healing; allow display-only changes and new IDs; reject missing or mechanically changed registered definitions before admission.
- **Rationale**: Smallest deterministic policy preserving stable identities and receipts without item instances or hot reload.
- **Trade-offs**: Balance changes require new retained item IDs; catalog grows append-only in mechanics.
- **Follow-up**: Startup failure must roll back newly registered definitions and remain not ready.

### Decision: Adopt Existing Infrastructure, Build Only Domain Rules

- **Context**: Persistence, transport, retries and character health are already implemented.
- **Alternatives Considered**: New inventory framework, effect plugin system or independent transaction manager.
- **Selected Approach**: Standard-library embedded JSON and crypto; existing DBTX, character contract, response validation and runtime lifecycle. Add only typed inventory values, cohesive helpers, local savepoints and a generic startup callback.
- **Rationale**: No new dependency, background worker, generic repository interface or event bus is needed.
- **Trade-offs**: DBTX does not statically distinguish transactions; callers retain the established transaction-only contract.
- **Follow-up**: Fault tests must cover savepoint cleanup and outer commit failure.

## Risks & Mitigations

- Ephemeral delivery can fail after commit: preserve state and let the next inventory command inspect it; never fallback.
- Domain rejection after normalized recovery can leave tentative writes: savepoint rollback and tests that intentionally commit a rejected outer handler.
- Replay or control retention mistakes can duplicate effects: separate permanent receipts from expiring/prunable tokens.
- Future battle may assume loadout locks or effect permissions: keep those choices downstream; revalidate the shared contract before battle implementation.
- Live visibility cannot be proven by fake transport alone: require documented test-group milestone evidence during implementation.

## References

- [Approved requirements](requirements.md)
- [Inventory brief](brief.md)
- [Project guidelines](../../../AGENTS.md)
- [Roadmap](../../steering/roadmap.md)
- [Incremental delivery](../../steering/development-workflow.md)
- [Runtime transaction contract](../../../internal/runtimecontract/database.go)
- [Character service](../../../internal/character/service.go)
- [Runtime startup](../../../internal/app/runtime.go)
- [Production import boundaries](../../../internal/architecture/boundaries_test.go)
