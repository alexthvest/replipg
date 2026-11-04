# Localization implementation validation

## Validation Report

- DECISION: GO
- OWNERSHIP: LOCAL
- UPSTREAM_SPEC: N/A
- Completed: all 23 subtasks, including user-confirmed manual milestone gates 3.8, 4.4 and 5.3.
- Manual evidence: the user reported “All works well” after receiving the live testing instructions and explicitly requested confirmation on 2026-10-28. This is user-reported acceptance; the agent did not independently operate Telegram or receive per-step evidence.
- Core steering files product.md, tech.md and structure.md are absent. Repository guidance, approved localization specs and development-workflow.md supplied the available project context.

## Implementation and integration

English and Russian embedded flat catalogs cover settings, character, inventory,
retained items, command descriptions and enabled development probes. Startup
validates exact manifest coverage, restricted parsed templates, Unicode and item
limits. Explicit typed parameters and integer plural rules preserve dynamic
values. Production always receives configured presentation dependencies; legacy
entrypoints retain compatibility.

Settings owns one independent persistent locale per group, revision-bound opaque
controls, fresh per-request administrator evidence and durable menu intent.
Network membership verification runs before the dispatch transaction. Locale
changes, control consumption and update completion share the existing transaction.
Menu publication runs outside short snapshot/result transactions, with revision
and fingerprint fencing, retries, restart refresh and periodic reconciliation.
Generic supervised workers follow existing admission, drain and cancellation
ownership. New feature imports follow the approved architecture edges.

Migration 0004 adds only settings/menu/control tables. Upgrade tests preserve
existing character and inventory state and starter entitlement. Older migration
sets reject schema 4. The user's item seed changes and stable item identities
remain intact; bilingual cloth_armor presentation describes leather armor.

## Requirements coverage

| Requirements | Automated evidence |
|---|---|
| 1.1–1.7 | Independent groups, English default, Russian switch/restart, subsequent old inventory controls, unchanged retained response snapshots |
| 2.1–2.7 | Read-only member settings, administrator controls, restricted-only replies, ordinary-command silence, no gameplay initialization |
| 3.1–3.8 | Fresh bounded membership, revoked administrator, unavailable verification, actor/group/revision controls, expiry during SQL reads, rollback and rejected-control regressions |
| 4.1–4.8 | Complete bilingual catalogs and feature tests, translated buttons/development text/menus, correct dynamic values, integer plural cases and safe names |
| 5.1–5.6 | Every retained item's translated text, unchanged IDs/mechanics, retained ownership/equipment/starter state, old control compatibility |
| 6.1–6.8 | Exact owned chat scopes, full enabled menus, failed publication/pending feedback, both in-flight fences, capped retries, restart with changed enabled commands and periodic refresh |
| 7.1–7.7 | Restart persistence, replay/consumed controls, revision ordering, corrupt selections, failed save/commit, immutable prepared responses and existing restricted-delivery regressions |
| 8.1–8.5 | Malformed/missing/duplicate/unknown resources, restricted template AST, argument errors, plural forms, UTF-16 budgets and shared item-content validation |
| 9.1–9.3 | Local fake Telegram and deterministic catalog/settings/runtime tests; full quality and executable smoke workflows |
| 9.4–9.5 | Runnable milestones and documented live steps/required bot privileges; manual verification confirmed by the user on 2026-10-28 |

Important integration regressions include expired permission/control evidence
across database reads, malformed/foreign callbacks leaving unmanaged groups
unchanged, sanitized actual names remaining names in Russian, failed confirmation
delivery retaining the saved language, remote publication succeeding before a
failed local acknowledgement, and changed enabled-command sets after restart.

## Review and workflow evidence

Independent agents reviewed runtime/transport, settings/persistence,
presentation, and executable composition. Concrete findings were repaired and
covered by regression tests. No unresolved code findings remained at the end of
review. No dependencies or provider calls were added to localization; runtime
packages remain independent of settings and gameplay implementations.

The user explicitly requested parallel agents, overriding the original
sequential scheduling guidance. Ownership boundaries prevented overlapping
production edits. No commits were created.

Tests-first execution was partial. Actual initial catalog compile failures,
composition failure, UTF-16 overflow and permission-freshness regression failures
were observed before fixes. Some settings and renderer tests followed their
implementation. The initial Nix sandbox denial was not a valid failing test.
Code review approval does not imply complete adherence to the prescribed TDD
history; that workflow deviation remains recorded.

## Operational follow-up

Manual verification is confirmed by the user; see manual-verification.md for
the evidence and its limits. For subsequent deployments, give the bot
administrator privileges for reliable membership checks and resolve competing
operator-owned menu overrides deliberately.
Before deployment, stop the single replica and back up the complete local SQLite
directory. An older binary requires a matching schema-compatible stopped backup;
external Telegram menus require deliberate restoration during rollback.

## Executed quality gates

| Command | Result |
|---|---|
| `./scripts/check` | PASS, exit 0: formatting clean, `go test ./...`, `go vet ./...`, `golangci-lint run` (0 issues), `go test -race ./...` |
| `./scripts/smoke` | PASS, exit 0: built production executable reaches readiness and completes the local fake-Telegram lifecycle |
| `./scripts/build` | PASS, exit 0: production binary built with pinned devenv Go |
| Focused localization/settings/persistence and feature tests | PASS, including independent uncached runs, race checks and targeted lint |
| `git diff --check` | PASS, no whitespace errors |

The production executable localization test verifies member read-only access,
lost administrator rejection, rejected confirmation delivery with retained save,
Russian character and retained-item text, group isolation, pending failed menu
publication, restart recovery with enabled-command changes, English switch-back,
old inventory controls and consumed settings controls. An overlaid invalid
embedded catalog fails executable construction before database startup and reports
safe actionable locale/key context. Raw error wrappers and resource content do
not enter startup diagnostics.

## Verification Result

- STATUS: VERIFIED
- CLAIM_TYPE: FEATURE_GO
- CLAIM: Localization implementation, automated verification and user-confirmed manual acceptance are complete.
- EVIDENCE: Previously executed canonical checks, build, fake executable smoke, independent code review, requirement mapping, focused regressions and the user's explicit manual-verification confirmation on 2026-10-28.
- GAPS: Manual acceptance is user-reported, without per-step evidence or independent agent execution. Tests-first workflow history is partial and is not retroactively certified.
- NOTES: This status update changes documentation only. Application checks were not rerun; their prior results are preserved above.
