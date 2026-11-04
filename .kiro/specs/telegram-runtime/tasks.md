# Implementation Plan

- [x] 1. Establish the pinned runtime foundation
- [x] 1.1 Pin the development environment and runtime dependencies
  - Align Go with the approved minimum and pin the selected Telegram and no-CGO SQLite dependencies.
  - Generate dependency and environment locks with their owning tools while retaining language-server and lint tooling.
  - Done when the repository builds in devenv and exact selected versions are visible in reproducible metadata.
  - _Boundary: Development Environment_
  - _Requirements: 1.1, 1.3, 9.1_

- [x] 1.2 (P) Create deterministic runtime test support
  - Provide an injectable clock, cancellation-aware timers, and a fake Telegram service with recorded requests and programmable outcomes.
  - Keep automated tests isolated from real Telegram and nondeterministic sleeps.
  - Done when tests can advance time and observe success, rate limits, interruption, and malformed responses deterministically.
  - _Boundary: Test Support_
  - _Depends: 1.1_
  - _Requirements: 2.7, 4.3, 4.4, 6.2, 7.2_

- [x] 1.3 (P) Implement validated and redacted operator configuration
  - Parse credentials, local-storage declaration, health address, polling, contention, handler, retry, lease, and shutdown bounds.
  - Reject missing, unsafe, or multi-instance settings before database or network activity and expose only a redacted view.
  - Done when every invalid input returns a typed error naming only the setting, before side effects, with no sensitive value.
  - _Boundary: Config Loader_
  - _Depends: 1.1_
  - _Requirements: 1.1, 1.2, 1.6, 5.7, 7.2, 8.6, 9.4_

- [x] 1.4 (P) Implement shared identifiers and transaction capabilities
  - Define standard-library-only identifiers, database execution, and transaction-scoped work capabilities.
  - Keep Telegram models, SQLite driver values, and service implementations outside the shared boundary.
  - Done when value and capability tests pass and an import check proves this contract slice is standard-library-only.
  - _Boundary: Runtime Contracts_
  - _Depends: 1.1_
  - _Requirements: 4.1, 6.1, 9.1, 9.2_

- [x] 1.5 Implement shared response and durable-work values
  - Define transport-neutral response, delivery, job, retry, and versioned-payload values using identifiers owned by task 1.4.
  - Add pure validation for destinations, content shape, retry bounds, and payload versions.
  - Done when tests accept supported values and return typed reasons for every invalid shape or version.
  - _Boundary: Runtime Contracts_
  - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 4.1, 6.1, 9.1, 9.2_

- [x] 1.6 (P) Implement privacy-safe structured diagnostics
  - Generate correlations and allowlist operational categories, outcomes, durations, retry timing, states, and minimum identifiers.
  - Reject or redact credentials, message text, private responses, opaque payloads, and raw API bodies.
  - Done when diagnostics remain actionable while sensitive fixtures never appear in emitted records.
  - _Boundary: Diagnostics_
  - _Depends: 1.4_
  - _Requirements: 2.5, 8.4, 8.6, 8.7_

- [x] 2. Build the durable persistence kernel
- [x] 2.1 Open and verify bounded local SQLite storage
  - Open only the validated local location with WAL, full synchronous durability, foreign keys, immediate writes, and a connection cap.
  - Verify effective settings and map bounded busy or locked outcomes to a distinguishable error.
  - Done when a temporary database reports required settings, colocates sidecars, and a forced lock returns within bounds.
  - _Boundary: Persistence Kernel_
  - _Requirements: 5.1, 5.4, 5.7_

- [x] 2.2 Build the migration engine and compatibility gate
  - Embed ordered immutable migrations, recording each successful version in its transaction.
  - Reject a newer schema and roll back failed upgrades without readiness.
  - Done when a two-step fixture reapplies idempotently while failed and future fixtures return typed errors.
  - _Boundary: Persistence Kernel_
  - _Requirements: 1.3, 1.4, 5.5, 5.6_

- [x] 2.3 Create the initial runtime schema
  - Create cursor, processed-update, outbound intent and attempt, and scheduled-job tables with constraints and due indexes.
  - Keep content and opaque payloads separate from status-facing data and enforce relationships.
  - Done when schema inspection confirms constraints and invalid state or relationship inserts fail atomically.
  - _Boundary: Persistence Kernel_
  - _Requirements: 2.6, 4.1, 5.5, 6.1, 8.5_

- [x] 2.4 Implement short transactions and durable update completion
  - Run caller database operations in context-bound immediate transactions without driver types.
  - Persist a monotonic cursor and immutable completed, ignored, decode-failed, or handler-failed outcomes.
  - Done when commit and rollback tests show all-or-nothing visibility and duplicate lookup survives reopen.
  - _Boundary: Persistence Kernel_
  - _Requirements: 2.6, 2.7, 5.1, 5.2, 5.3_

- [x] 2.5 Harden persistence failure behavior
  - Exercise contention, lock cancellation, migration rollback, resource closure, and injected commit failure.
  - Assert failures never claim success or expose partial state.
  - Done when persistence tests pass repeatedly under the race detector without leaked resources.
  - _Boundary: Persistence Kernel_
  - _Requirements: 5.2, 5.3, 5.4, 5.6, 7.4_

- [x] 3. Implement transactional durable request stores
- [x] 3.1 Persist recoverable delivery intent and status
  - Insert a validated versioned request and retry policy in the caller transaction before reporting success.
  - Expose delivery states without content and reject conflicting IDs or unsupported versions.
  - Done when accepted intent survives reopen and rollback leaves no delivery.
  - _Boundary: Outbox Service_
  - _Depends: 2.3, 2.4_
  - _Requirements: 4.1, 4.5, 8.5, 9.2_

- [x] 3.2 Register, schedule, and inspect durable jobs
  - Register one owner per job type and payload version before readiness.
  - Persist stable identity, due time, state, versioned payload, and retry policy in the caller transaction.
  - Done when jobs survive reopen while conflicting IDs and unsupported versions return typed errors.
  - _Boundary: Scheduler Service_
  - _Depends: 2.3, 2.4_
  - _Requirements: 6.1, 6.2, 9.2_

- [x] 3.3 Persist scheduled-job cancellation
  - Record cancellation conditionally and define conflicts with claimed, completed, or permanently failed work.
  - Keep cancellation durable and remove later execution eligibility.
  - Done when concurrent cancellation yields one valid transition and cancelled work is never due.
  - _Boundary: Scheduler Service_
  - _Requirements: 6.6_

- [x] 4. Implement transport-neutral interaction processing
- [x] 4.1 Register unambiguous feature-owned routes
  - Register command names, callback prefixes, and one ordinary group-message subscriber before readiness.
  - Reject overlapping ownership while preserving typed identities without gameplay interpretation.
  - Done when each interaction resolves to one owner and the runtime defines no player-facing game command.
  - _Boundary: Interaction Router_
  - _Requirements: 2.1, 2.2, 2.4, 9.1, 9.2, 9.3, 9.5_

- [x] 4.2 Compose successful dispatch with atomic durable effects
  - Compose delivery and scheduler stores into the transaction-scoped work capability with no network access.
  - Commit feature changes, durable effects, completed outcome, and cursor together.
  - Done when success tests show state, delivery and job effects, and cursor appear in one commit or not at all.
  - _Boundary: Interaction Router_
  - _Depends: 2.4, 3.1, 3.2_
  - _Requirements: 2.6, 4.1, 5.2, 6.1, 9.2_

- [x] 4.3 Isolate handler failure and duplicate processing
  - Roll back handler errors or panics before recording terminal failure separately.
  - Skip unsupported and completed updates without invoking state-changing behavior.
  - Done when failures leave no state or effect residue, emit one correlation, and allow the next update.
  - _Boundary: Interaction Router_
  - _Requirements: 2.4, 2.5, 2.6, 5.3_

- [x] 4.4 Validate and return post-commit immediate responses
  - Validate public and ephemeral immediate responses before commit and return them only from completed dispatch.
  - Discard immediate effects on rollback while retaining recoverable responses durably.
  - Done when invalid responses return specific reasons and valid effects appear only after commit.
  - _Boundary: Interaction Router_
  - _Requirements: 3.1, 3.2, 3.3, 3.5, 3.6, 4.1_

- [x] 5. Implement Telegram transport adapters
- [x] 5.1 Integrate the shared Telegram API client for intake and delivery
  - Build one bounded context-aware client with identity verification for all Telegram operations.
  - Classify validation, authorization, conflict, rate-limit, transient, permanent, cancellation, and uncertain outcomes.
  - Done when fake responses map to exact safe categories and preserve retry delay without raw content.
  - _Boundary: Update Intake, Telegram Delivery_
  - _Requirements: 1.3, 1.4, 4.4, 4.5, 4.6_

- [x] 5.2 Deliver public responses and callback acknowledgements
  - Map public text, entities, and buttons without exposing library models upstream.
  - Acknowledge callbacks through a separate invisible operation.
  - Done when the fake service observes exact public and acknowledgement payloads with no visible acknowledgement.
  - _Boundary: Telegram Delivery_
  - _Requirements: 2.3, 3.1, 3.4, 3.5, 4.2_

- [x] 5.3 Deliver restricted ephemeral responses without fallback
  - Map receiver, callback, incoming-ephemeral, and administrator authorization to Bot API 10.3.
  - Enforce group target, authorization expiry, zero ordinary ID, and replacement restrictions before I/O.
  - Done when restricted payloads are exact and invalid or failed cases never use public or private fallback.
  - _Boundary: Telegram Delivery_
  - _Requirements: 3.2, 3.3, 3.5, 3.6, 4.2, 4.5, 4.6_

- [x] 5.4 (P) Initialize completion-controlled polling safely
  - Delete webhooks without dropping queued updates, load the cursor, and prepare the next offset.
  - Enter an owned polling state and signal initialization without waiting indefinitely for an update.
  - Done when initialization preserves backlog, exposes the exact offset, and never signals before ownership.
  - _Boundary: Update Intake_
  - _Depends: 2.4, 5.1_
  - _Requirements: 1.3, 1.4, 1.5, 2.7_

- [x] 5.5 Poll one update through durable terminal completion
  - Request one update at the cursor offset and issue no later poll until routing returns durable completion.
  - Retry interruption without advancing and fail after bounded responses with no usable identifier.
  - Done when tests show crash redelivery, exact resume, cancellation, and no premature confirmation.
  - _Boundary: Update Intake_
  - _Requirements: 1.4, 2.7_

- [x] 5.6 Decode and map raw Telegram updates
  - Decode the update ID first, then map commands, callbacks, group messages, unsupported, and malformed known-ID updates.
  - Preserve transport-neutral identities without gameplay meaning.
  - Done when fixtures yield the expected interaction or terminal outcome for every supported and malformed shape.
  - _Boundary: Update Intake_
  - _Requirements: 2.1, 2.2, 2.4, 9.5_

- [x] 5.7 Integrate callback-first dispatch and post-commit delivery
  - Acknowledge callbacks before feature behavior and send immediate responses sequentially before the next poll.
  - Diagnose each response independently without rollback or visibility changes.
  - Done when a blocked handler receives prompt acknowledgement and failures leave durable dispatch correct.
  - _Boundary: Update Intake, Interaction Router, Telegram Delivery_
  - _Depends: 4.4, 5.2, 5.3, 5.5, 5.6_
  - _Requirements: 2.3, 2.5, 3.1, 3.2, 3.3, 3.4_

- [x] 6. Implement durable workers and recovery
- [x] 6.1 Claim and complete definitive delivery attempts
  - Conditionally claim due intent and commit attempting state before network I/O.
  - Deliver outside transactions and record acceptance once; never reclaim terminal work.
  - Done when concurrent workers produce one request and repeated wakeups never redeliver completion.
  - _Boundary: Outbox Service_
  - _Depends: 3.1, 5.3_
  - _Requirements: 4.2, 4.7_

- [x] 6.2 Apply bounded delivery retry and exhaustion
  - Retain transient failures with capped cancellation-aware backoff and honor Telegram delay as a minimum.
  - Stop at the attempt bound with an operator-safe permanent reason.
  - Done when fake-clock tests observe exact eligibility, prompt cancellation, and no claim after exhaustion.
  - _Boundary: Outbox Service_
  - _Requirements: 4.3, 4.4, 4.5, 4.8_

- [x] 6.3 Recover abandoned attempts into reconciliation
  - Move startup records left attempting into reconciliation rather than pending or completed.
  - Preserve facts needed for resolution without exposing content operationally.
  - Done when restart tests show abandoned and possibly transmitted attempts are never automatically resent.
  - _Boundary: Outbox Service_
  - _Requirements: 4.6, 4.7, 7.5, 7.6_

- [x] 6.4 Register and execute explicit reconciliation decisions
  - Own registrations keyed by delivery kind and version and reject collisions before readiness.
  - Invoke matching reconcilers with a bound and leave unmatched work visible for explicit resolution.
  - Done when confirm, safe-retry, permanent, timeout, collision, and no-handler cases reach only allowed states.
  - _Boundary: Outbox Service_
  - _Requirements: 4.6, 4.7, 4.8, 7.6_

- [x] 6.5 (P) Claim due jobs and commit successful execution atomically
  - Select no work before due time and conditionally grant one lease.
  - Execute the handler transactionally and commit feature changes with completion.
  - Done when fake-clock and claim tests show one execution, no early run, and no repeat after completion.
  - _Boundary: Scheduler Service_
  - _Depends: 2.4, 3.2_
  - _Requirements: 6.2, 6.3, 6.5_

- [x] 6.6 Persist scheduler retry and permanent outcomes
  - Roll back feature changes for retryable, permanent, error, or panic outcomes before recording transition.
  - Apply retry bounds and retain operator-safe permanent reasons.
  - Done when each non-success outcome has the expected state with no partial feature change.
  - _Boundary: Scheduler Service_
  - _Requirements: 6.7, 6.8_

- [x] 6.7 Recover expired claims and overdue scheduled work
  - Reclaim expired leases and make overdue incomplete work eligible during startup.
  - Keep completed, cancelled, and permanent jobs ineligible.
  - Done when reopen tests cover overdue, retrying, expired, completed, cancelled, and permanent states exactly.
  - _Boundary: Scheduler Service_
  - _Requirements: 6.4, 7.3, 7.5_

- [x] 7. Expose operational lifecycle and work visibility
- [x] 7.1 Maintain truthful lifecycle and dependency state
  - Track starting, recovering, ready, draining, stopped, and required dependency health.
  - Report single-instance mode and unsupported multi-instance coordination.
  - Done when readiness is false outside ready and dependency failure returns a safe lifecycle reason.
  - _Boundary: Operational State_
  - _Requirements: 1.6, 8.1, 8.2, 8.3, 9.4_

- [x] 7.2 Aggregate privacy-safe durable-work status
  - Count delivery and job states without loading content, payloads, secrets, paths, or Telegram IDs.
  - Preserve distinctions among pending, active, retrying, reconciliation, completed, cancelled, and permanent.
  - Done when populated fixtures produce only expected aggregate counts and allowlisted fields.
  - _Boundary: Operational State_
  - _Requirements: 8.5, 8.6, 8.7_

- [x] 7.3 Serve liveness, readiness, and status endpoints
  - Return live success only while viable and ready success only while admission is safe.
  - Serve lifecycle reasons and aggregate status without raw errors or sensitive data.
  - Done when HTTP tests verify codes and bodies for every lifecycle state and privacy constraint.
  - _Boundary: Operational State_
  - _Requirements: 8.1, 8.2, 8.3, 8.5, 8.6, 8.7_

- [x] 8. Compose and own the executable runtime lifecycle
- [x] 8.1 Assemble ordered startup and recovery
  - Consume validated configuration, then migrate storage, recover work, verify identity, preserve webhook backlog, and start workers and polling.
  - Complete registrations before admission.
  - Done when runtime stays not-ready until recovery and polling-start handshake finish, then reaches ready.
  - _Boundary: Runtime App_
  - _Depends: 5.7, 6.4, 6.7, 7.3_
  - _Requirements: 1.1, 1.3, 1.4, 7.5, 8.2, 8.3, 8.4_

- [x] 8.2 Verify startup failure unwinding
  - Inject failure at every startup stage and retain a safe cause and correlation.
  - Unwind acquired resources in reverse order without admission.
  - Done when each failure returns non-success, stays not-ready, and leaves no owned resource running.
  - _Boundary: Runtime App_
  - _Requirements: 1.2, 1.3, 1.4, 7.4, 8.2, 8.4_

- [x] 8.3 Supervise critical workers and readiness
  - Own every goroutine, timer, server, cancellation, and wait group under one lifecycle.
  - Isolate record failures but make unsafe intake or required-worker exit remove readiness and begin shutdown.
  - Done when tests distinguish record failures from critical exits and never advance cursor on interruption.
  - _Boundary: Runtime App_
  - _Requirements: 1.4, 2.5, 2.7, 8.1, 8.2, 8.4_

- [x] 8.4 Coordinate admission stop and bounded drain
  - On signal, mark not-ready and stop new polling, dispatch, claims, and retry waits before draining.
  - Allow owned work to finish only within the deadline.
  - Done when readiness drops first and no new work starts after draining begins.
  - _Boundary: Runtime App_
  - _Requirements: 4.8, 7.1, 7.2_

- [x] 8.5 Cancel deadline work and close resources
  - Cancel remaining contexts at deadline while preserving incomplete durable state.
  - Close intake, timers, HTTP, and database once and wait for owned goroutines.
  - Done when deadline tests reopen recoverable work and finish without leaks.
  - _Boundary: Runtime App_
  - _Requirements: 7.3, 7.4, 7.5, 7.6_

- [x] 8.6 Replace the placeholder command with thin composition
  - Load validated configuration, construct dependencies and signal context, and map runtime outcomes to process exits.
  - Test command behavior with a fake runtime boundary rather than duplicating lifecycle integration.
  - Done when invalid configuration and runtime failure exit nonzero while graceful completion exits successfully.
  - _Boundary: Runtime App_
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 7.1, 7.4, 8.1, 8.2, 8.3_

- [x] 8.7 Add executable local setup artifacts
  - Provide a machine-readable non-secret configuration example and executable build, run, probe, and check helpers.
  - Keep secrets and machine-specific paths out of shared artifacts.
  - Done when helpers run through devenv and reject an incomplete configuration example safely.
  - _Boundary: Development Environment_
  - _Requirements: 1.1, 1.2, 5.7, 8.6_

- [x] 8.8 Add an opt-in Telegram transport smoke harness
  - Register development-only public, callback, ephemeral, and ordinary-message behavior without gameplay semantics.
  - Require explicit enablement and reuse production routing and delivery boundaries.
  - Done when fake-Telegram tests exercise all four registrations without printing content or using fallback.
  - _Boundary: Runtime App, Interaction Router, Telegram Delivery_
  - _Depends: 8.6, 8.7_
  - _Requirements: 2.1, 2.3, 3.1, 3.2, 3.3, 3.4, 3.6, 8.6, 9.1, 9.3, 9.5_

- [x] 9. Validate recovery and cross-component guarantees
- [x] 9.1 Verify startup, readiness, and webhook integration
  - Run the executable with temporary SQLite and fake Telegram.
  - Cover configuration handoff, migrations, recovery, webhook preservation, polling-start handshake, and probe transition.
  - Done when startup stays not-ready through prerequisites, preserves queued updates, then becomes ready exactly once.
  - _Boundary: Runtime App, Persistence Kernel, Update Intake, Operational State_
  - _Depends: 8.8_
  - _Requirements: 1.3, 1.4, 1.5, 8.2, 8.3_

- [x] 9.2 Verify cursor, redelivery, and duplicate integration
  - Exercise one update in flight, cursor-derived offsets, crash before commit, restart redelivery, and completed duplicates.
  - Inject transient polling interruption without changing the durable completion boundary.
  - Done when no accepted update is skipped, no uncommitted update is confirmed, and completed behavior never repeats.
  - _Boundary: Persistence Kernel, Update Intake, Interaction Router_
  - _Requirements: 2.1, 2.2, 2.4, 2.6, 2.7_

- [x] 9.3 Verify callback and immediate-response integration
  - Observe acknowledgement while a handler blocks, then exercise sequential public and ephemeral post-commit responses.
  - Inject acknowledgement and response failures and assert isolation, visibility, and no fallback.
  - Done when response paths leave durable dispatch correct and later responses and updates continue.
  - _Boundary: Update Intake, Interaction Router, Telegram Delivery_
  - _Requirements: 2.3, 2.5, 3.1, 3.2, 3.3, 3.4, 3.5, 3.6_

- [x] 9.4 (P) Verify outbound delivery recovery across interruption
  - Interrupt after enqueue, during retry, during transmission, and after acceptance before local persistence, then reopen.
  - Assert retries resume, completion stays terminal, and uncertain transmission requires reconciliation.
  - Done when no reconciliation-required delivery resends without explicit safe resolution.
  - _Boundary: Outbox Service_
  - _Depends: 8.8_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 7.5, 7.6, 8.5_

- [x] 9.5 (P) Verify scheduled-work recovery and claim safety
  - Reopen overdue, retrying, expired, completed, cancelled, and permanent jobs and race claims around due time.
  - Assert success changes commit with completion while non-success changes roll back.
  - Done when race tests show one execution per claim and none after cancellation.
  - _Boundary: Scheduler Service_
  - _Depends: 8.8_
  - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 6.8_

- [x] 9.6 Verify lifecycle admission and resource safety end to end
  - Fault dependency health, worker exit, signal drain, and deadline cancellation across the runtime.
  - Assert readiness drops before admission closes and cancellation preserves incomplete durable work.
  - Done when the suite closes every owned resource within bounds without leaked goroutines or timers.
  - _Boundary: Runtime App, Operational State_
  - _Depends: 9.1, 9.2, 9.3, 9.4, 9.5_
  - _Requirements: 7.1, 7.2, 7.3, 7.4, 8.1, 8.2, 8.3_

- [x] 9.7 Verify diagnostics, privacy, and status end to end
  - Populate every durable state and fault startup, dispatch, delivery, and scheduling diagnostics.
  - Assert probes, status, and logs contain safe categories and correlations but no secrets, content, raw errors, or unnecessary identifiers.
  - Done when privacy fixtures remain absent and aggregate status distinguishes every required work state.
  - _Boundary: Operational State, Diagnostics_
  - _Depends: 9.4, 9.5, 9.6_
  - _Requirements: 8.4, 8.5, 8.6, 8.7_

- [x] 10. Complete runnable validation and quality gates
- [x] 10.1 Automate the completed runtime smoke gate
  - Reuse the development-only smoke harness against fake Telegram to exercise the fully composed runtime and operational probes.
  - Cover the same public, callback, ephemeral, and ordinary-message path used by an operator test group without external credentials.
  - Done when the deterministic smoke gate passes with durable workers and lifecycle integration active and no content output.
  - _Boundary: Runtime App, Interaction Router, Telegram Delivery_
  - _Requirements: 2.1, 2.3, 3.1, 3.2, 3.3, 3.4, 3.6, 8.6, 9.1, 9.3, 9.5_

- [x] 10.2 Exercise all automated quality gates
  - Format Go and run tests, vet, lint, and race checks through devenv.
  - Review generated locks and verify automated tests never contact real Telegram.
  - Done when every prescribed command succeeds and pinned metadata reproduces the build.
  - _Boundary: Runtime App_
  - _Requirements: 1.1, 5.1, 5.2, 5.3, 7.4, 8.6_

- [x] 10.3 Confirm runtime boundaries remain domain-neutral
  - Audit imports for Telegram, SQLite driver, and gameplay leakage and enforce inward dependency direction.
  - Verify existing configuration and status retain single-instance, local-volume, Privacy Mode, and fallback limitations without adding behavior.
  - Done when static cross-component checks fail on forbidden dependencies and confirm only approved capabilities.
  - _Boundary: Runtime Contracts, Config Loader, Operational State_
  - _Requirements: 1.6, 5.7, 9.1, 9.2, 9.3, 9.4, 9.5_

## Implementation Notes

- All 53 implementation subtasks and independent reviews pass. On 2026-10-27 the owner explicitly accepted the remaining live-verification gate with "please also close it for telegram-runtime", following reported successful operation. Feature-level validation is GO; owner acceptance and automated evidence are recorded separately in validation.md. No assistant-observed live results or detailed per-probe outcomes are claimed.

- Task 8.1: Startup must call owning `Store.ReconcilePending` after registration sealing and before readiness; abandoned attempts alone remain quarantined until an explicit registered decision.

- Task 5.4: `Poller.Started` confirms initialization only; runtime readiness must also wait for task 5.5's owned polling-loop handshake.

- Task 4.3: Durable stores must receive the tracked transaction capability so database failures remain retryable even after stores sanitize their errors or handlers swallow the returned error.
- Task 4.4: Invalid immediate responses return a durable `handler_failed` outcome together with a typed validation error; intake must inspect the durable status before deciding to retry. Failure-record persistence errors return no outcome.
- Tasks 6.5–6.7: Application composition must supply a transaction-bound scheduler work-scope factory and call `Service.Recover` before readiness; required health dependencies start unverified.

- Task 1.1: Human approval selected published `modernc.org/sqlite v1.61.0`; its module metadata confirms the matching `modernc.org/libc v1.79.0`, while bounded lock cancellation remains a later persistence-task verification concern.
- Task 3.2: Scheduler registration explicitly takes a payload version and must be sealed before scheduling; due times and retry durations round upward to SQLite milliseconds so work never becomes eligible early.
- Task 3.1: Outbox schemas register feature-owned pure decoders by kind/version to validate response and routing metadata while preserving opaque payload bytes; runtime composition must seal the decoder registry before readiness.
- Task 3.3: Cancellation requires the supplied database capability to implement `persistence.Transactor`; repeated cancellation returns a typed cancelled-state conflict, so concurrent cancellation has exactly one successful transition.
