# Research & Design Decisions

## Summary

- **Feature**: `character-system`
- **Discovery Scope**: Extension; integration-focused discovery of a new gameplay domain on the implemented runtime.
- **Key Findings**:
  - Existing transaction-scoped `DBTX` is sufficient for character persistence and later coordinated gameplay; no feature-owned database lifecycle is needed.
  - Generic command-menu publication and additional command metadata are necessary for an authorized ephemeral first-use flow.
  - Current migration composition and architecture enforcement need narrow, explicit extensions.
  - Lazy deadline normalization satisfies automatic recovery without a timer, scan, or scheduled job.
- **Guidance consulted**: `kiro-spec-quick`, `kiro-spec-design`, its light-discovery, synthesis, principles, and review-gate rules; root `AGENTS.md`; roadmap and incremental-delivery steering. No additional design/UX skill is listed for this command-only feature.
- **Context limitation**: `.kiro/steering/product.md`, `tech.md`, and `structure.md` are absent. Existing runtime code/specs and approved character requirements supply the available architectural guidance.

## Research Log

### Runtime Transaction and Composition Seams

- **Context**: Health changes must commit with future battle/inventory state and update completion.
- **Sources Consulted**: `internal/runtimecontract/database.go`, `work.go`; `internal/interaction/dispatch.go`; `internal/persistence/tx.go`, `errors.go`; `internal/app/runtime.go`; `cmd/replipg/main.go`. A read-only explorer independently inspected these same integration seams.
- **Findings**:
  - Handlers run in a runtime-owned transaction and can pass `WorkScope.DBTX()` to feature operations.
  - Immediate responses become externally visible only after commit. Failed delivery cannot undo a committed character change.
  - The router tracks `ExecContext` and `QueryContext` errors, but cannot track errors returned by `QueryRowContext().Scan`; feature code must return those errors.
  - Safe storage wrappers must preserve the underlying cause so existing persistence classification can identify storage errors and cancellation.
  - Feature registration is an injected function receiving registry/outbox/scheduler; feature construction can capture configuration and clock without adding database access to registration.
- **Implications**: Keep SQL and service cohesive in `internal/character`; inject transaction capabilities per call and a clock at construction; do not create transactions, goroutines, or transport calls inside the feature.

### Migration and Import Ownership

- **Context**: Runtime migration list currently contains only version 1, and the import checker forbids cross-feature imports.
- **Sources Consulted**: `internal/persistence/migrate.go`, `migrations/0001_runtime.sql`; `internal/architecture/boundaries_test.go`; adjacent character, inventory, encounter, and battle briefs.
- **Findings**:
  - `RuntimeMigrations()` is an explicitly ordered deployment-wide set; a standalone migration 2 set would fail contiguous-version validation.
  - Gameplay can use its own subpackages and runtime contracts/registries, but cannot import concrete persistence or another feature's contract today.
  - Putting gameplay state into `runtimecontract` would violate the runtime's domain-neutral commitment.
- **Implications**: Embed migration 0002 in the existing persistence list and allow gameplay imports of exactly `internal/character/contract`. Runtime-to-gameplay and gameplay-to-implementation imports remain forbidden. No generic migration-source hook is needed.

### Ephemeral Command Entry and Authorization

- **Context**: A first-use personal interface must be usable without a public message or private chat.
- **Sources Consulted**: `internal/telegram/mapper.go`, `ephemeral.go`, `client.go`; `internal/runtimecontract/responses.go`; `internal/devsmoke/register.go`; locally cached pinned module `github.com/go-telegram/bot@v1.27.0/models/bot_command.go`, `models/bot_commands_scope.go`, `methods_params.go`, `methods.go`; [Telegram ephemeral interactions](https://core.telegram.org/bots/api#ephemeral-messages-and-commands) and [setMyCommands](https://core.telegram.org/bots/api#setmycommands).
- **Findings**:
  - Telegram's command declaration supports ephemeral entry, and eligible incoming ephemeral messages authorize a restricted reply for 15 seconds from the incoming action. Administrator privileges provide a separate authorization path; this feature does not depend on it.
  - The pinned module already exposes `BotCommand.IsEphemeral`, `SetMyCommandsParams`, `BotCommandScopeAllGroupChats`, and `Bot.SetMyCommands`; no upgrade is necessary.
  - Existing runtime routes do not publish a command menu. Mapped commands omit source timestamp, bot flag, and chat-sender context needed for strict personal actor validation.
  - Existing smoke handlers establish an authorization window from handler time; the character handler must instead use the original message timestamp and reject stale input.
- **Implications**: Add generic optional menu declarations to runtime options and a bounded Telegram adapter. Preserve source timestamp and actor flags as transport-neutral command fields. Publish `/character` as ephemeral, accept the incoming-ephemeral route, and emit no fallback for unsupported ordinary commands. Menu setup is described here; no external menu has been changed.

### Character State and Recovery

- **Context**: User decisions require partial recovery, no passive regeneration, and automatic creation only through character-related commands.
- **Sources Consulted**: Approved `requirements.md`, `brief.md`; user clarification in the active session; `internal/testsupport/clock.go` and runtime scheduling interfaces.
- **Findings**:
  - The required recovery behavior is observable during summary, eligibility, and mutation operations; no notification or autonomous recovery-side effect is required.
  - One persisted deadline plus a snapshotted recovery-health amount preserves outcomes across restart and configuration changes.
  - Exact decimal-percentage arithmetic can use the standard library, avoiding binary floating-point rounding and integer intermediate overflow.
- **Implications**: Normalize at the beginning of feature operations. Keep health zero while incapacitated; apply configured partial recovery exactly once when observed at/after deadline. Snapshot base attributes at creation and recovery policy at incapacitation.

## Architecture Pattern Evaluation

| Option | Strengths | Risks / Limitations | Decision |
|---|---|---|---|
| Cohesive character package using transaction capability | Fits existing runtime; short atomic operations; little ceremony | Callers must supply a genuine transaction | Selected |
| Separate generic repositories and transaction managers | Broad substitution | Duplicates runtime responsibilities and adds unneeded boundaries | Rejected |
| Scheduled recovery job per defeat | Eager persisted row update | Additional job schema, registration, claim/retry and stale-job coordination without required notifications | Rejected |
| Deadline normalization on access | Restart safe; no worker; exact effective eligibility | Dormant rows may retain expired recovery fields until observed | Selected |
| Feature-owned migration composition API | Each feature embeds migrations | Expands runtime API and migration assembly for one additive schema | Rejected for this feature |
| Add migration to existing ordered persistence set | Preserves migration API and startup | Deployment-wide schema list includes feature tables | Selected |

## Design Decisions

### Decision: One Transaction-Bound Character Service

- **Context**: Creation, health, recovery, and idempotency are variations of transitions of the same character aggregate.
- **Alternatives Considered**: Separate recovery service/worker; generic repository interface; feature-owned transaction manager.
- **Selected Approach**: One cohesive service with pure rules, concrete capability-bound store, and consumer-owned interfaces over a small pure contract package.
- **Rationale**: Generalize the transition interface without adding speculative infrastructure. Reuse the existing transaction owner.
- **Trade-offs**: Even effective-state reads require a write-capable transaction to normalize recovery. Consumer documentation/tests must enforce this.
- **Follow-up**: Atomic fixture tests combining character and other gameplay state, including failure at commit.

### Decision: Explicit Command Menu and Minimal Metadata

- **Context**: There is no authorized ephemeral first-use route from an ordinary command unless the bot has separate administrator authority.
- **Alternatives Considered**: Require administrator privileges; public button bootstrap; document a command without publishing ephemeral metadata.
- **Selected Approach**: Publish a generic group command menu; `/character` is ephemeral. Require incoming ephemeral authorization and original source timestamp. Add bot/chat sender flags to mapped commands.
- **Rationale**: Uses already pinned Telegram capabilities and keeps all personal interactions inside the group without public bootstrap.
- **Trade-offs**: Startup now performs an idempotent command-menu operation. Unsupported clients or stale/per-chat/localized menu overrides may prevent entry; no visibility fallback is permitted.
- **Follow-up**: Fake API assertions plus a real test-group/client demonstration; document the managed Telegram scope and operator ownership.

### Decision: Persist Replay Receipts and Recovery Snapshot

- **Context**: Runtime update deduplication does not by itself prevent a downstream gameplay operation from applying twice under a different update.
- **Alternatives Considered**: Rely only on update IDs; prune identities; return historical state as the current state.
- **Selected Approach**: A durable globally unique operation identity records target, kind, amount, and applied revision. Matching replay returns current normalized state without replaying its mutation. Deadline and recovery amount are fixed for each incapacitation.
- **Rationale**: Prevents repeated damage and extra healing across restarts while preserving current summaries.
- **Trade-offs**: Receipts grow without retention in the MVP. Configuration edits affect future creation/incapacitation, not established state.
- **Follow-up**: Replay after later damage and later defeat; conflicting replay; rollback and identity reuse; no cleanup until a replay horizon is specified.

### Decision: Build Domain Rules, Adopt Existing Infrastructure

- **Context**: Character rules are specific to this game; persistence, routing, Telegram, and diagnostics are already implemented.
- **Alternatives Considered**: An RPG framework, ORM, new scheduler, or additional datastore.
- **Selected Approach**: Implement the small domain in Go with standard-library arithmetic/configuration/IDs; reuse the pinned runtime and dependencies.
- **Rationale**: No inspected existing library is needed for the required transitions. Local dependency inspection confirms existing Telegram support.
- **Trade-offs**: Character invariants and SQL constraints must be tested together rather than delegated to a framework.
- **Follow-up**: Canonical devenv gates and production boundary tests during implementation.

### Decision: Runnable Vertical Milestones

- **Context**: Steering requires Telegram demonstrations at each milestone.
- **Selected Approach**: Creation/inspection first, health/recovery second, fault/release verification third. Development controls are self-only and use short recovery configuration.
- **Rationale**: Each milestone produces visible user behavior; deterministic tests never need a real bot credential.
- **Trade-offs**: Full feature completion remains dependent on recorded manual live-group evidence.
- **Follow-up**: Create the reproduction document during implementation and record actual observations separately from planned checks.

## Risks & Mitigations

- **Upstream completion record**: Runtime validation still records `MANUAL_VERIFY_REQUIRED`; specification work can proceed, but sequential implementation must retain the outstanding upstream live verification gate.
- **Migration rollback**: Old binaries reject schema 2; use compatible whole-volume restore or a forward fix. Never edit migration 0001.
- **Ephemeral expiry**: No durable summary replay. Preserve the original action timestamp, recheck before commit/send, and allow later inspection after failed delivery.
- **Actor spoofing through anonymous/chat senders**: Retain sender metadata and reject it in personal handlers, even if `from.id` is positive.
- **Import boundary drift**: Permit only the contract edge; keep runtime and other features out of character implementation.
- **Idempotency loss through receipt cleanup**: Keep receipts until a later approved replay/retention policy exists.
- **Defaults and configuration changes**: Proposed values are tuning defaults; stored attributes and active incapacitation outcomes are stable.
- **Development leakage**: Flag is strict, explicit, false by default; omit routes and managed declarations in production; stale client commands remain harmless.

## Validation Performed During Design

- Inspected code, pinned dependency source, templates, skills, requirements, steering, and adjacent briefs; no implementation code was changed and no Telegram request was sent.
- Mechanically checked that all 47 individual acceptance-criterion IDs occur in the design traceability table, the four boundary sections and concrete file plan are populated, and no template placeholders remain.
- Ran the design review gate inline, checking transaction ownership, replay/recovery ordering, ephemeral authorization, schema rollout, dependency direction, and milestone executability. Corrected service-signature presentation and explicit edge-case wording during the bounded review.
- Attempted `devenv shell -- go env GOMODCACHE`; shell startup failed because the sandbox could not access the Nix daemon socket. Inspected the existing cached pinned module read-only instead. Go implementation tests are not applicable to these specification-only changes and were not run.

## References

- [Approved character requirements](requirements.md)
- [Character discovery brief](brief.md)
- [Roadmap](../../steering/roadmap.md)
- [Development workflow](../../steering/development-workflow.md)
- [Telegram runtime design](../telegram-runtime/design.md)
- [Telegram runtime validation](../telegram-runtime/validation.md)
- [Telegram Bot API ephemeral interactions](https://core.telegram.org/bots/api#ephemeral-messages-and-commands)
- [Telegram Bot API command publication](https://core.telegram.org/bots/api#setmycommands)
- [Pinned Telegram library command declaration](https://github.com/go-telegram/bot/blob/v1.27.0/models/bot_command.go)
