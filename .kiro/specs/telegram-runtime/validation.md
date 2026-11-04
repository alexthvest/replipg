> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

> Historical record: health endpoints, dependency-health state, and the probe helper were removed on 2026-11-04 by user request. Their recorded checks describe the earlier implementation. Current behavior uses structured lifecycle diagnostics and in-process startup synchronization; see requirements.md and design.md.


# Telegram Runtime Implementation and Validation

## Validation Report

- DECISION: **GO**
- Automated implementation: **53/53 subtasks checked**; all 27 subtasks continued in this run received independent approval.
- Requirements: **60/60 numbered acceptance criteria** have implementation and substantive automated-test evidence.
- Concrete code blockers, blocked tasks, and unresolved review findings: **none**.
- Code snapshot: `69e642d`; automated quality-gate record: `2437bfc`. Subsequent report changes are documentation only.
- The project owner accepted closure of the remaining live-verification gate on 2026-10-27 with "please also close it for telegram-runtime", following reported successful live operation. This is owner acceptance, not assistant-observed live evidence; no real Telegram messages were sent by the assistant during implementation or validation.

## Changes and Boundaries

Completed restricted ephemeral delivery, raw update mapping, completion-controlled polling, callback-first dispatch, durable delivery claims/retries/reconciliation, executable startup and recovery, worker supervision, bounded draining, deadline cleanup, configuration examples, and executable development helpers.

The runtime now composes the existing persistence, router, scheduler, outbox, health, diagnostics, and Telegram boundaries through `internal/app`; `cmd/replipg` remains a thin signal-aware entry point. The optional `REPLIPG_DEV_SMOKE` setting defaults to false and enables development probes without adding gameplay semantics.

Cross-component review found and repaired two integration gaps: startup now enumerates reconciliation work through the owning outbox API, and fresh successful/ignored update processing emits correlated diagnostics after commit even when no response is sent. Replay and failed commits emit no misleading processing-success diagnostic.

No dependency, migration, generated lock, or devenv input changes were needed in this continuation. Unrelated unexpected formatting changes were removed while preserving task edits.

## Mechanical Results

The implementation worker and an independent reviewer each executed the canonical gates against the final frozen Go sources. Every command exited 0.

| Gate | Result |
|---|---|
| `./scripts/check` | PASS: formatting, full tests, vet, lint, and race checks through devenv |
| `gofmt -l cmd internal scripts` | No output |
| `go test ./...` | PASS |
| `go vet ./...` | PASS |
| `golangci-lint run` | PASS: zero issues |
| `go test -race ./...` | PASS |
| `./scripts/smoke` | PASS: uncached build and boot of the production executable |
| Dependency/environment diff from `2f17ec1` | Empty for `go.mod`, `go.sum`, `devenv.nix`, `devenv.yaml`, and `devenv.lock` |
| Residual placeholders/secrets audit | No concrete findings; privacy fixtures contain synthetic values only |

Verified toolchain and pins: Go **1.26.10**, `github.com/go-telegram/bot` **v1.27.0**, `modernc.org/sqlite` **v1.61.0**, and `modernc.org/libc` **v1.79.0**.

The smoke gate builds and executes production `main`, confines its HTTPS Telegram traffic to a local CONNECT proxy and TLS fixture, observes live/not-ready startup and ready probes, exercises all four development probes and restricted-response failures, sends SIGTERM, requires exit 0, verifies listener closure, and reopens SQLite at the completed cursor. The proxy dials only its local fixture; no real Telegram requests occur.

## Requirements and Integration Coverage

| Requirement | Criteria evidenced | Principal evidence |
|---|---:|---|
| 1. Startup/configuration | 6/6 | Config validation/redaction; startup fault matrix; command subprocess startup/readiness integration |
| 2. Intake/routing | 7/7 | Mapper fixtures; precommit polling gate; cancellation/reopen/redelivery; duplicate and transient-interruption integration |
| 3. Response visibility | 6/6 | Exact public/restricted payloads; acknowledgement before blocked handling; sequential post-commit delivery; no fallback |
| 4. Outbound recovery | 8/8 | Actual adapter and SQLite interruption tests; retry-delay floor; acceptance-before-local-commit failure; explicit reconciliation |
| 5. Persistence/atomicity | 7/7 | Real SQLite contention, transaction, migration, sidecar placement, reopen, rollback, and commit-failure tests |
| 6. Scheduled work | 8/8 | Reopen across every state; twelve competing claimants at the due boundary; all-store success and rollback assertions |
| 7. Shutdown/recovery | 6/6 | Actual runtime manual-clock deadline tests; active scheduled-work lifecycle tests; request joins and resource closure |
| 8. Health/diagnostics/privacy | 7/7 | Complete `/statusz` JSON with distinct counts for all twelve durable states; composed fault/privacy tests; post-commit diagnostic assertions |
| 9. Boundaries/compatibility | 5/5 | Production AST import gate with negative fixtures; transport-neutral contracts; explicit smoke opt-in; deployment/privacy documentation |
| **Total** | **60/60** | No unmapped acceptance criterion or concrete coverage gap identified |

Cross-task contracts are aligned: recovery and registration sealing precede readiness; durable completion controls polling offsets; acknowledgement precedes feature handling; immediate delivery follows commit; uncertain sends require reconciliation; scheduled success commits feature/outbox/child-job effects atomically. Admission cancellation and admitted-work cancellation remain separate.

Component ownership and dependency direction match the approved design. Focused file splits refine the proposed filenames without changing architecture. The import gate also examines platform-specific production files and proves rejection of forbidden dependencies with synthetic fixtures. No gameplay leakage, unsupported multi-instance behavior, automatic uncertain-send replay, new dependency, or design revalidation trigger was found.

The missing core steering files were already acknowledged by the approved design. Downstream character, inventory, encounter, and battle specifications should consume the runtime contracts and registration seams; changes to those seams, Bot API semantics, database ownership, or deployment mode require the design's stated revalidation.

## Task Delivery Record

Every task below reached implementer status **READY_FOR_REVIEW**, independent verdict **APPROVED**, and fresh task-local verification before its checkbox was marked. Verification-only tasks correctly report RED as not applicable. Focused remediation was reviewed before acceptance.

| Task | Commit | Main affected files or package |
|---|---|---|
| 5.3 | `15e62d3` | `internal/telegram/ephemeral.go`, tests |
| 5.4 | `85609ac` | `internal/telegram/poller.go`, tests |
| 5.5 | `e015853` | `internal/telegram/poll_loop.go`, poller, tests |
| 5.6 | `6f864e7` | `internal/telegram/mapper.go`, tests |
| 5.7 | `8a51c5f` | `internal/telegram/processor.go`, tests |
| 6.1 | `2bdee0a` | `internal/outbox/worker.go`, tests |
| 6.2 | `bc01939` | `internal/outbox/retry.go`, worker, tests |
| 6.3 | `cc9b4d8` | `internal/outbox/recovery.go`, tests |
| 6.4 | `d6c4f16`, `3d5ca5e` | Outbox reconciliation and startup enumeration, tests |
| 8.1 | `5a9757e` | `internal/app/runtime.go`, composition, tests |
| 8.2 | `c171cbf` | `internal/app/startup_failure_test.go` |
| 8.3 | `f3e926a` | Application supervision, bounded router, tests |
| 8.4 | `02c4fd6` | Application admission/draining, tests |
| 8.5 | `0036973` | Private shutdown timer seam and runtime deadline tests |
| 8.6 | `e7e967e` | `cmd/replipg/main.go`, tests |
| 8.7 | `49647d7` | README, `.env.example`, build/run/probe/check helpers |
| 8.8 | `383a8ed` | `internal/devsmoke`, optional configuration and command composition |
| 9.1 | `93aab46` | `cmd/replipg/integration_test.go` |
| 9.2 | `ee0cfad` | `internal/telegram/cursor_integration_test.go` |
| 9.3 | `a91cce7` | `internal/telegram/response_integration_test.go` |
| 9.4 | `46794c3` | `internal/outbox/recovery_integration_test.go` |
| 9.5 | `c5bd479` | `internal/scheduler/recovery_integration_test.go` |
| 9.6 | `98ee8cb` | `internal/app/lifecycle_integration_test.go` |
| 9.7 | `69e642d` | `internal/app/privacy_integration_test.go` |
| 10.1 | `39ae46b` | Production-binary smoke test, `scripts/smoke`, README |
| 10.2 | `2437bfc` | Final automated gate evidence and task record |
| 10.3 | `5f2a17a` | `internal/architecture/boundaries_test.go` |
| Requirement 8.4 audit repair | `677e9ad` | Router post-commit diagnostics and correlated integration expectations |

## Manual Gate and Operational Limits

- OWNERSHIP: LOCAL — all acceptance gates closed.
- UPSTREAM_SPEC: N/A.
- BLOCKED_TASKS: None; all implementation subtasks are checked.
- FEATURE_GO: **GO**.

[Development steering](../../steering/development-workflow.md) requires a user-visible demonstration in a Telegram test group. The owner explicitly accepted the runtime's remaining manual gate after reporting successful operation during character-system testing. The roadmap runtime entry is therefore complete. No detailed runtime-probe observations, client versions, or per-step results were supplied; none are inferred. Automated smoke and owner acceptance are separate evidence sources.

For future reproduction with an authorized dedicated bot/group, follow the [README development probes](../../../README.md): enable `REPLIPG_DEV_SMOKE=true` in ignored local configuration, build/run, probe readiness, demonstrate `/dev_smoke`, invisible callback acknowledgement, a reply restricted to the pressing user, and ordinary-message intake with appropriate Privacy Mode. Exercise incoming-ephemeral reply where supported and verify rejected restricted delivery creates no public/private fallback. Record observations without credentials or private content, then stop and disable smoke mode. These instructions are not a claim that the owner individually reported every probe result.

Operational constraints remain one active instance on a local persistent filesystem, no public/private response fallback, explicit resolution of uncertain sends, and cooperative context cancellation by feature handlers, reconcilers, and transports. No deployment or real-group messaging was performed.

## Character integration revalidation

Character-system fault-injection tests exposed a runtime classification gap:
feature-wrapped SQL row scan, iteration, and close errors could be recorded as
terminal handler failures. The runtime contract now offers a generic
`StorageFailure` marker; persistence recognizes that marker through `errors.As`,
and the character storage wrapper implements it. Runtime packages retain their
prohibition on character imports. The regression tests prove rollback and retry
with the same update and operation identity instead of advancing completion.
Focused character/interaction/persistence/runtimecontract/architecture tests,
vet, lint, and affected-package race checks passed during implementation. Final
repository-wide results are recorded in the character-system validation report.
The automated revalidation remains separate from the owner's subsequent acceptance of the live Telegram gate.

## Closure verification — 2026-10-27

- STATUS: VERIFIED
- CLAIM_TYPE: FEATURE_GO
- CLAIM: Telegram-runtime's implementation and acceptance gates are closed.
- EVIDENCE: All 53 subtasks checked; existing 60/60 requirements, independent design/integration reviews, and explicit owner acceptance of the remaining manual gate. Shared repository checks were refreshed immediately before this documentation-only closure: `./scripts/check` exited 0 (formatting, full tests, vet, lint with 0 issues, and race); `./scripts/smoke` exited 0 (production binary smoke 0.455s).
- GAPS: No remaining runtime acceptance blocker. Detailed live per-probe observations were not supplied; gate closure records the owner's explicit acceptance rather than inventing those observations.
- NOTES: No code, configuration, deployment, or real Telegram messaging changes. Single-instance, local-storage, privacy, and reconciliation constraints remain in force.

## Health removal verification (2026-11-04)

User-approved scope: remove operational HTTP health endpoints, address configuration, listener injection, dependency snapshots, aggregate work status, and the probe helper. Character health and Telegram development commands retain their behavior. Lifecycle events use structured startup/shutdown diagnostics; `Runtime.Ready()` remains the in-process startup handshake.

- `devenv shell -- golangci-lint fmt ./...`: completed.
- `./scripts/check`: configuration verification, formatting verification, lint (0 issues), full tests, build, and race tests passed.
- Built-binary smoke coverage exercises Telegram interactions and signal shutdown while the former default health port is occupied. Startup and recovery tests use controlled Telegram requests and `Ready()`; shutdown tests assert admission rejection and resource cleanup. Queue recovery uses direct database assertions.
- No database migration or deployment service is introduced. Remove old endpoint probes and `REPLIPG_HEALTH_ADDRESS` from deployment configuration.
- Live Telegram verification was not performed in this run. Automated transport and production-binary fixtures supplied the integration evidence above; previous live acceptance is not evidence for this change.
