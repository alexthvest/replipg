> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

# Research & Design Decisions

## Summary

- **Feature**: `telegram-runtime`
- **Discovery Scope**: Complex Integration / New Feature
- **Consulted guidance**: `kiro-spec-design`, including full discovery, boundary-first design, synthesis, and the design review gate; repository-wide `AGENTS.md`; available roadmap and incremental-delivery steering.
- **Context limitation**: The expected core steering files `product.md`, `tech.md`, and `structure.md` and a repository `README.md` do not exist. The roadmap, workflow steering, requirements, Go module, devenv configuration, and current source were used as the project baseline.
- **Key Findings**:
  - Bot API 10.3 ephemeral responses are real group/supergroup messages with restricted visibility, but delivery is best-effort and non-administrator bots need a qualifying callback or incoming ephemeral-message context within 15 seconds. The runtime contract must carry this authorization context and must never fall back to a public or private-chat response.
  - `github.com/go-telegram/bot` v1.27.0 supports Bot API 10.3 outbound types, but its built-in polling loop advances its in-memory offset before handler completion. Durable completion therefore requires a narrow, explicit `getUpdates` poller instead of `Bot.Start`.
  - SQLite WAL remains a single-writer, local-filesystem design. The published `modernc.org/sqlite` v1.61.0 release, with its matching `modernc.org/libc` v1.79.0, is the selected no-CGO stack; cancellation and bounded lock waits remain application-level verification requirements.
  - Telegram does not provide an idempotency key or lookup for an ordinary send whose response was lost. An indeterminate outbound attempt must stop automatic delivery and enter an operator-visible reconciliation state.

## Research Log

### Repository Baseline and Integration Points

- **Context**: The runtime is the first implementation spec and must establish conventions without overbuilding a greenfield repository.
- **Sources Consulted**: `AGENTS.md`, `go.mod`, `devenv.nix`, `devenv.yaml`, `cmd/replipg/main.go`, `.kiro/steering/roadmap.md`, `.kiro/steering/development-workflow.md`, `brief.md`, and `requirements.md`.
- **Findings**:
  - The only Go source is a `Hello World` command. There are no tests, dependencies, migrations, CI/build scripts, lock file, README, or established internal packages.
  - `go.mod` specifies Go 1.26.9, which satisfies the roadmap requirement of Go 1.26 or newer. The rolling nixpkgs input is not pinned because `devenv.lock` is absent.
  - Repository guidance favors a thin `cmd` entry point, cohesive `internal` packages, standard-library-first code, explicit lifecycle ownership, typed errors, and deterministic tests.
  - Each delivery milestone must include automated checks, a Telegram test-group demonstration, and reproducible commands; Telegram integration cannot be deferred to the final milestone.
- **Implications**:
  - `cmd/replipg` remains composition-only. Runtime contracts and services live under `internal/`, with Telegram and SQLite details isolated in adapters.
  - Implementation must update and pin the Go/devenv/dependency toolchain before relying on Bot API 10.3 or the selected SQLite fixes.

### Telegram Bot API 10.3 Ephemeral Messaging

- **Context**: Requirements 3.2 and 3.3 depend on restricted-visibility responses that were introduced in Bot API 10.2 and reshaped in 10.3.
- **Sources Consulted**: Telegram Bot API 10.3 `EphemeralMessageParameters`, `sendMessage`, `Message`, `ReplyParameters`, ephemeral interaction, and callback-query documentation; `go-telegram/bot` v1.24.0 and v1.27.0 release/source documentation.
- **Findings**:
  - Bot API 10.3 uses `ephemeral_message_parameters` with required `receiver_user_id` and optional `callback_query_id` and `replace_callback_query_message`.
  - A non-administrator bot may send to the triggering client within 15 seconds when it supplies the callback query identifier or replies to the incoming `ephemeral_message_id`. A chat administrator may target a non-bot member without trigger context.
  - Delivery is not guaranteed, especially while the target user is offline. An API acceptance is therefore the strongest completion fact available to the runtime.
  - Ephemeral messages use `ephemeral_message_id`; their ordinary `message_id` is zero. Ephemeral replies must remain ephemeral, and callback replacement is invalid for callbacks that originated from ephemeral messages.
  - `go-telegram/bot` v1.27.0 exposes the 10.3 parameter model. It also contains fixes for HTTP/2 GOAWAY request replay and should be pinned rather than using the minimum compatible v1.24.0.
- **Implications**:
  - The transport-neutral `EphemeralResponse` contains a typed authorization union: callback context, incoming ephemeral-message reply context, or administrator delivery.
  - Validation rejects missing/expired authorization, bot targets, unsupported chat kinds, and replacement combinations before the API call. Failure never changes visibility to public and never starts a private chat.
  - Persisted references to ephemeral messages use `ephemeral_message_id`, not `message_id`.

### Callback Acknowledgement and Error Taxonomy

- **Context**: Callback acknowledgement must remain prompt while domain behavior may block, fail, or enqueue durable work.
- **Sources Consulted**: Telegram `answerCallbackQuery` and `ResponseParameters` documentation; `go-telegram/bot` v1.27.0 `errors.go`, request code, and parameter types.
- **Findings**:
  - Telegram clients display progress until `answerCallbackQuery` is called; acknowledgement is a separate API operation and need not create a chat message.
  - Telegram exposes `retry_after` for flood control and `migrate_to_chat_id` for migrated groups. The Go library exposes a typed `TooManyRequestsError`, common status sentinels, and context-aware requests.
  - Ordinary outbound calls are not application-retried by the library. Retry classification remains a runtime responsibility.
- **Implications**:
  - Update intake acknowledges callbacks before invoking the registered feature handler. Acknowledgement failure is diagnosed and bounded independently; it does not execute inside a SQL transaction and does not suppress domain dispatch.
  - Error mapping uses typed categories: invalid request, unauthorized/forbidden, rate limited with a minimum retry time, transient transport/server failure, permanent Telegram rejection, cancellation, and indeterminate outcome.

### Durable Long Polling and Offset Semantics

- **Context**: Requirements 2.6 and 2.7 require duplicate suppression and restart from the last durably completed update.
- **Sources Consulted**: Telegram `getUpdates` and `deleteWebhook` documentation; `go-telegram/bot` v1.27.0 `get_updates.go`, `options.go`, and worker lifecycle source.
- **Findings**:
  - `getUpdates` confirms all lower update IDs when a higher offset is supplied. Webhooks and polling are mutually exclusive, and `deleteWebhook` with `drop_pending_updates=false` preserves queued work.
  - Telegram retains updates for no longer than 24 hours. This is an upstream retention limit, not a runtime durability guarantee.
  - The library stores `lastUpdateID` before enqueueing an update and may issue another poll before the handler commits. `WithInitialOffset` only seeds that in-memory value; default asynchronous handlers are not joined on shutdown.
- **Implications**:
  - A narrow stdlib HTTP poller calls `getUpdates` directly with `limit=1`, requests `last_completed_update_id + 1`, and does not issue the next poll until the current update reaches a durable terminal state.
  - The rest of the Telegram API remains behind a pinned `go-telegram/bot` adapter. This is a deliberate exception rather than a second general-purpose client.
  - Completed, ignored, decode-failed-with-ID, and handler-failed updates advance a monotonic cursor. Domain state, recoverable effects, and successful completion commit atomically; handler failures roll back domain changes, then record a terminal failed outcome so later updates continue.
  - Telegram calls are forbidden inside the update transaction. Features express recoverable effects through the transaction-scoped outbox and scheduler stores.

### SQLite Driver, WAL, and Transactions

- **Context**: Requirements 5, 6, and 7 need crash-safe local state, bounded contention, migrations, and cancellation-aware shutdown.
- **Sources Consulted**: `modernc.org/sqlite` package documentation and canonical changelog/go.mod; SQLite WAL, synchronous pragma, transaction, result-code, ALTER TABLE, and release documentation; Go `database/sql` transaction documentation.
- **Findings**:
  - The published `modernc.org/sqlite` v1.61.0 release requires Go 1.26 and declares `modernc.org/libc` v1.79.0; bundled SQLite is 3.54.1. The upstream changelog mentions a later cancellation fix under v1.61.1, but that revision is not published, so bounded lock cancellation must be verified against v1.61.0 and enforced at the application boundary.
  - WAL permits readers alongside one writer but requires the database, WAL, and SHM files to share a local host filesystem. Long readers can prevent checkpoints and grow the WAL.
  - WAL with `synchronous=FULL` preserves committed transactions across power loss. `NORMAL` may lose the latest commit after power loss even though the database remains consistent.
  - Busy timeout is connection-local. Per-connection pragmas must be applied through an application-constructed DSN, and operation contexts must independently bound waits.
- **Implications**:
  - The runtime verifies WAL activation, uses `synchronous=FULL`, foreign keys, a bounded busy timeout, immediate write transactions, a small explicit connection cap, and typed `SQLITE_BUSY`/`SQLITE_LOCKED` mapping.
  - Migrations are embedded, immutable, ordered, and applied one at a time in transactions with a `schema_migrations` record. A schema newer than the binary prevents readiness.
  - Transactions never cross Telegram calls; rows and read transactions are short-lived and closed promptly.

### Durable Delivery, Reconciliation, and Scheduling

- **Context**: Requirements 4 and 6 require recovery without overstating exactly-once guarantees.
- **Sources Consulted**: Telegram send method/error contracts; SQLite transaction and WAL behavior; project requirements and roadmap.
- **Findings**:
  - The database can atomically record intent and domain state, but Telegram cannot deduplicate a repeated ordinary send by an application key.
  - A timeout, connection loss, or crash after request transmission but before a response can leave acceptance unknowable.
  - Single-instance scheduling still requires atomic claims because worker overlap and restart recovery can otherwise duplicate execution.
- **Implications**:
  - Outbound delivery uses `pending`, `attempting`, `retrying`, `reconciliation_required`, `completed`, and `permanent_failure`. The attempt is durably marked before network I/O; only a definitive API success completes it.
  - Indeterminate attempts never return automatically to `pending`. A registered reconciler or explicit operator decision may resolve them; otherwise they remain visible and safe from blind replay.
  - Scheduled jobs use `pending`, `claimed`, `retrying`, `completed`, `cancelled`, and `permanent_failure`, with due time, claim lease, bounded retries, and an injected clock. One worker is the default single-replica policy; the database claim remains atomic.

### Application Lifecycle and Health

- **Context**: Startup order, readiness, recovery, and shutdown determine whether durable guarantees are truthful.
- **Sources Consulted**: Go `os/signal.NotifyContext` and `net/http.Server.Shutdown` documentation; project lifecycle and observability requirements.
- **Findings**:
  - Signal-derived cancellation and HTTP shutdown are context-aware, but worker ownership and drain ordering remain application responsibilities.
  - Readiness is distinct from liveness and must not turn true before migrations, webhook cleanup, recovery, workers, and intake initialization succeed.
- **Implications**:
  - Startup is an ordered state machine. Shutdown first marks not-ready and closes admission, then drains tracked work to a deadline, cancels remaining work, and closes HTTP, Telegram intake, timers, and the database.
  - `/livez` reports only process viability, `/readyz` reports admission readiness, and `/statusz` reports aggregate durable-work counts by state without payloads, message bodies, secrets, or unnecessary Telegram identifiers.

## Architecture Pattern Evaluation

| Option | Description | Strengths | Risks / Limitations | Decision |
|--------|-------------|-----------|---------------------|----------|
| Layered modular monolith with ports and adapters | Domain-neutral runtime services define narrow ports; Telegram and SQLite implement adapters; one process composes them | Matches the roadmap, keeps gameplay independent, supports deterministic tests, and avoids deployment overhead | Requires careful dependency direction and transaction-scoped contracts | Selected |
| Direct library-centric bot | Register gameplay handlers directly on `go-telegram/bot` and call SQLite repositories from them | Small initial code surface | Built-in offset advancement violates durable completion; Telegram types leak into gameplay; shutdown joins are weak | Rejected |
| Durable inbox with concurrent consumers | Persist every update before parallel dispatch and track a contiguous acknowledgement watermark | Higher potential throughput and replay tooling | More storage, privacy policy, ordering, and recovery complexity than the single-replica MVP needs | Deferred; revalidate if throughput requires concurrency |
| Event-driven services with PostgreSQL | Separate transport, scheduler, and game domains into services | Scales independently | Contradicts the single-container roadmap and introduces distributed consistency before MVP validation | Rejected |

## Design Decisions

### Decision: Generalize Feature Work as Transactional Handlers and Durable Effects

- **Context**: Commands, callbacks, ordinary messages, scheduled jobs, and outbound responses are different triggers but share atomic state/effect needs.
- **Alternatives Considered**:
  1. Give every feature direct Telegram and `database/sql` access.
  2. Force every behavior through one generic event bus.
  3. Define focused handler, transaction, outbox, and scheduler contracts with shared typed identifiers.
- **Selected Approach**: Use transport-neutral interaction variants and a transaction-scoped handler contract. Features own gameplay meaning; the runtime owns dispatch, transaction completion, and durable effect primitives.
- **Rationale**: This generalizes the boundary without implementing a speculative event platform and makes duplicate suppression enforceable.
- **Trade-offs**: Feature handlers must respect the no-network-in-transaction rule and express recoverable effects through runtime stores.
- **Follow-up**: Downstream specs must revalidate if they require external side effects inside the update transaction.

### Decision: Adopt the Telegram Library Except for Durable Polling

- **Context**: The selected library supports Bot API 10.3 output types but does not expose a handler-completion-aware polling primitive.
- **Alternatives Considered**:
  1. Use `Bot.Start` and persist its latest observed offset.
  2. Fork the library.
  3. Use the library for API/model support and build one narrow `getUpdates` client.
- **Selected Approach**: Pin v1.27.0 and implement only polling directly against the official JSON API.
- **Rationale**: It preserves upstream fixes for outbound methods while preventing acknowledged-but-uncommitted update loss without maintaining a fork.
- **Trade-offs**: The update decoder and Bot API error envelope need contract tests against fixtures and a fake HTTP server.
- **Follow-up**: Replace the narrow poller if the upstream library later exports a completion-controlled polling API.

### Decision: Serialize Update Completion

- **Context**: Telegram offsets are a contiguous acknowledgement boundary, while requirements prioritize correctness and one replica over throughput.
- **Alternatives Considered**:
  1. Parallel handlers with an in-memory watermark.
  2. Durable inbox and concurrent consumers.
  3. One update in flight from polling through durable terminal outcome.
- **Selected Approach**: Poll and complete one update at a time.
- **Rationale**: This is the smallest design that makes the stored cursor truthful after crashes.
- **Trade-offs**: A slow handler delays intake. Callback acknowledgement occurs first, handlers receive deadlines, and metrics expose latency.
- **Follow-up**: Sustained backlog or handler-latency targets trigger revalidation toward a durable inbox.

### Decision: Treat API Acceptance as Delivery Completion and Isolate Uncertainty

- **Context**: Telegram has no general request idempotency or post-send lookup.
- **Alternatives Considered**:
  1. Blindly retry all timeouts.
  2. Mark all transmitted requests completed.
  3. Separate definitive, transient, permanent, and indeterminate outcomes.
- **Selected Approach**: Complete only on a definitive Bot API success. Retry known transient pre-acceptance failures; retain permanent failures; quarantine indeterminate attempts for reconciliation.
- **Rationale**: It prevents silent loss and avoids knowingly creating duplicate user-visible effects.
- **Trade-offs**: Some indeterminate outcomes need human or feature-specific resolution and may remain pending indefinitely.
- **Follow-up**: Each recoverable delivery kind documents whether it has a safe reconciler.

### Decision: Use a Shared SQLite Transaction Kernel, Not a Generic Repository Framework

- **Context**: Downstream domains need atomic state changes with runtime cursors and durable effects.
- **Alternatives Considered**:
  1. Hide all persistence behind runtime-owned key/value storage.
  2. Let domains open independent databases.
  3. Expose a small `DBTX`/`Transactor` contract over the shared database.
- **Selected Approach**: The runtime owns database lifecycle, migrations, transaction creation, and runtime tables; features own their tables and query code while accepting the transaction-scoped `DBTX`.
- **Rationale**: This preserves domain ownership and cross-package atomicity without adding an ORM or generic repository abstraction.
- **Trade-offs**: Feature migration assembly and transaction dependency direction must remain explicit.
- **Follow-up**: Cross-feature migration ordering is revalidated when the first downstream schema is added.

### Decision: Prefer Durability over Maximum SQLite Throughput

- **Context**: Requirements promise restart persistence and atomicity on a single local volume.
- **Alternatives Considered**:
  1. WAL with `synchronous=NORMAL`.
  2. WAL with `synchronous=FULL`.
  3. Rollback-journal mode.
- **Selected Approach**: WAL plus `synchronous=FULL`, bounded busy handling, immediate writes, and short transactions.
- **Rationale**: This matches the stated durability priority and permits concurrent health/status reads while preserving the single-writer model.
- **Trade-offs**: More fsync work and lower peak write throughput; measure before optimizing.
- **Follow-up**: Any relaxation of durability requires explicit product approval and fault-testing evidence.

## Synthesis Outcomes

- **Generalization**: Interaction intake, scheduled execution, and delivery are modeled as typed work with explicit terminal/retry states, but they retain separate services because their idempotency and external-side-effect rules differ.
- **Build vs. adopt**: Adopt `go-telegram/bot`, `database/sql`, `modernc.org/sqlite`, stdlib HTTP/health, signal handling, and embedded migrations. Build only the completion-aware poller, runtime state machines, and domain-neutral ports that no selected dependency provides.
- **Simplification**: Use one update in flight, one scheduler worker, one outbox worker, one SQLite database, and aggregate HTTP status. Do not add an event bus, ORM, distributed lock, webhook server, automatic outbox deduplication fiction, or multi-replica lease system.
- **Boundary outcome**: The runtime owns transport, durable-work mechanics, database lifecycle, and atomic primitives. Gameplay owns all interpretation, tables, validation, and idempotent state transitions specific to game rules.

### Decision: Put Cross-Service Values in an Acyclic Runtime Contract Package

- **Context**: The initial design placed `WorkScope` in interaction while it referenced outbox and scheduler requests; scheduler handlers also consumed `WorkScope`, creating a Go import cycle and ambiguous ownership.
- **Alternatives Considered**:
  1. Allow interaction, outbox, and scheduler to import each other.
  2. Duplicate request and work-scope types in each service.
  3. Move shared values and capability interfaces into a behavior-free foundation package.
- **Selected Approach**: `internal/runtimecontract` owns `DBTX`, `WorkScope`, response, delivery, job, retry, identifier, and versioned-payload values and imports only the standard library.
- **Rationale**: Services depend on one stable inward boundary, adapters implement it, and downstream features avoid Telegram/SQLite driver types without cycles.
- **Trade-offs**: The package must remain deliberately small and behavior-free; service-specific state machines stay in their owning packages.
- **Follow-up**: Import-boundary tests or review checks reject service/adaptor imports from `runtimecontract`.

### Decision: Outbox Owns Reconciler Registration

- **Context**: Reconciliation behavior was defined without an owning registration lifecycle.
- **Selected Approach**: Outbox registers reconcilers by delivery kind and payload version before readiness, rejects duplicates, invokes a matching reconciler with a bound, and leaves unmatched work visible for explicit operator resolution.
- **Rationale**: Ownership aligns with the state machine and avoids routing or application composition interpreting feature payloads.
- **Trade-offs**: Not every delivery kind must have an automatic reconciler; safety still favors an unresolved record over blind replay.

## Risks & Mitigations

- Ephemeral delivery depends on Telegram client state and authorization context — validate the context, expose definitive API failures, and never change visibility as fallback.
- Bot API/library drift may alter fields or errors — pin versions and add adapter contract fixtures; revalidate on dependency upgrades.
- A slow sequential handler can create a Telegram backlog — bound handler duration, diagnose latency, and reconsider a durable inbox only with measured evidence.
- SQLite writer contention or WAL growth can delay work — cap connections, use immediate short writes, close rows promptly, expose contention/checkpoint metrics, and test bounded failure.
- A process crash during Telegram send can leave an unknowable result — mark the attempt before I/O and quarantine it for reconciliation on restart.
- Telegram retains pending updates for at most 24 hours — alert on sustained intake outage; the runtime cannot recover updates Telegram has expired.
- The single-replica precondition is operational, not enforced by distributed consensus — document it in startup/status and reject designs that imply horizontal safety.
- Core steering files are missing — revalidate this design if later steering establishes conflicting package, security, operational, or dependency rules.

## References

- [Telegram Bot API 10.3](https://core.telegram.org/bots/api) — canonical API, update, error, callback, and ephemeral-message contracts.
- [Telegram ephemeral messages and commands](https://core.telegram.org/bots/api#ephemeral-messages-and-commands) — delivery eligibility and best-effort behavior.
- [`go-telegram/bot` v1.27.0 release](https://github.com/go-telegram/bot/releases/tag/v1.27.0) — selected library release and transport fixes.
- [`go-telegram/bot` polling source](https://github.com/go-telegram/bot/blob/v1.27.0/get_updates.go) — in-memory offset advancement behavior.
- [`go-telegram/bot` options](https://github.com/go-telegram/bot/blob/v1.27.0/options.go) — initial offset and worker options.
- [`modernc.org/sqlite` documentation](https://pkg.go.dev/modernc.org/sqlite) — no-CGO driver, DSN, error, and transaction behavior.
- [`modernc.org/sqlite` v1.61.0 release](https://gitlab.com/cznic/sqlite/-/tags/v1.61.0) — selected published no-CGO SQLite release.
- [SQLite write-ahead logging](https://sqlite.org/wal.html) — local-filesystem, single-writer, checkpoint, and sidecar constraints.
- [SQLite synchronous pragma](https://sqlite.org/pragma.html#pragma_synchronous) — durability trade-offs.
- [SQLite transaction semantics](https://sqlite.org/lang_transaction.html) — deferred and immediate write behavior.
- [Go database transaction guidance](https://go.dev/doc/database/execute-transactions) — `database/sql` transaction boundaries.
- [Go signal handling](https://pkg.go.dev/os/signal#NotifyContext) — signal-derived lifecycle cancellation.
- [Go HTTP shutdown](https://pkg.go.dev/net/http#Server.Shutdown) — graceful health server shutdown semantics.
