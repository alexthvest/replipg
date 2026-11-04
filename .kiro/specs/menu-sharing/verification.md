> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

# Final automated sharing verification — task 5.3

Date: 2026-10-28. Tested implementation: `2b5d30e` (bilingual inventory
milestone), with its reviewed prerequisites. This task adds only this record;
it changes no runtime behavior, dependency, schema, task checkbox or live record.

The automated gates passed. Feature-level GO is **not established**: mandatory
human-client tasks 3.4 and 4.4 remain blocked and unchecked. At recording time,
17 of 20 executable subtasks are checked; accepting and marking 5.3 after review
would make that 18 of 20. Parent task headings are not counted as subtasks.

## Commands actually run

All commands below ran from the repository root using the pinned devenv
environment. The scripts enter that environment themselves. No deployment,
real Telegram message, production database or local credential inspection was
part of this verification.

| Command | Actual result | Exit |
| --- | --- | --- |
| `./scripts/check` | PASS: formatting produced no violations; `go test ./...`, `go vet ./...`, `golangci-lint run` (`0 issues.`), and `go test -race ./...` passed | 0 |
| `./scripts/build` | PASS: production executable built at ignored `bin/replipg` | 0 |
| `./scripts/smoke` | PASS: uncached `TestProductionBinarySmoke`, command package `0.575s` | 0 |
| `devenv shell -- go test -count=1 -run 'TestSharing\|TestShare\|TestInventorySource\|TestProjector' ./internal/persistence ./internal/sharing ./cmd/replipg` | PASS: persistence `0.016s`, sharing `0.648s`, command package `0.129s` | 0 |

The regular full-suite command reported cached results for several unchanged
packages. Its race stage freshly executed the command package (`14.330s`),
sharing (`12.539s`) and architecture (`1.041s`); other results included cache
hits. The focused command above explicitly disabled Go's test-result cache for
migration, sharing policy/recovery/retention and production sharing milestones.
`internal/sharing/contract` has no standalone test files; its consumers and
architecture tests are covered by the full gate.

The smoke gate builds and boots a production executable against isolated
temporary SQLite and a test-owned local TLS Telegram fake. It checks startup
readiness, active durable workers, public/restricted transport, invisible
acknowledgement, ordinary intake, rejected personal delivery without fallback,
SIGTERM shutdown and reopened durable state. It establishes executable/runtime
integration, not actual Telegram-client visibility. The separate sharing
milestones establish sharing through production composition.

This is verification-only work. RED phase: **N/A**; no behavioral implementation
or new failing-test claim is made.

## Grouped coverage and source evidence

| Coverage | Concrete tests and implementation | Relevant requirements |
| --- | --- | --- |
| Personal browsing, immutable explicit publication, no gameplay mutation, excluded settings/development surfaces | `cmd/replipg/sharing_character_milestone_test.go`: `TestSharingCharacterMilestone`; `sharing_inventory_milestone_test.go`: `TestSharingInventoryMilestone/en` and `/ru`; production composition in `sharing.go` and `localization.go`; character/inventory injected presentation | 1.3, 1.4, 1.5, 3.4, 5.5, 6.5, 7.4 |
| Exact displayed content and authorization | `internal/sharing/inventory_source_integration_test.go`: selected pages/details, failed edits, mapper authorization/expiry, changed item presentation and oversized attribution; `source_test.go`; `internal/telegram/mapper_source_test.go`; public payload validation in `delivery.go` | 2.1–2.7, 4.1–4.6, 7.1 |
| Per-view 30-second cooldown and inherited usability | `TestShareRefreshCooldownAndRetry` covers 29.999 seconds and equality at 30 seconds; `TestShareInheritedDeadlineAndCharacterWithoutExpiry` preserves inventory's existing deadline and adds no character timer; `TestShareConcurrentPending` and `TestShareAcceptanceExactSnapshotAndReplay` cover reservation/replay fencing | 3.1–3.7, 4.4, 4.5, 7.1 |
| Restart, interrupted sends and truthful outcomes | `internal/sharing/recovery_integration_test.go`: pending frozen delivery, attempting quarantine without automatic repost, completion before projection, queued feedback, definite rejection/retry, concurrent dispatch/projection/completion, projection failure without resend; `worker_test.go`: terminal timestamps, feedback expiry and bounded fair processing | 3.1, 3.3, 3.5–3.7, 5.1–5.7, 7.1 |
| Retention, replay and diagnostics | `retention_integration_test.go`: independent outcomes, transactional rollback, unresolved/unrelated guards, inventory retirement and usable character metadata; `retention_test.go`; `internal/outbox/retention_test.go`: kind/state/age guards, deletion ordering and rollback; existing runtime/privacy suites in the full gate | 3.5, 5.4, 5.6, 6.3, 6.4, 7.4 |
| English/Russian integration and unchanged authority | Both production inventory milestone cases share pages/details, equip/unequip/Back, retain earlier copies, switch language in both directions and check old source language versus current guidance; settings member/admin controls and three ephemeral command declarations are asserted | 1.3, 1.5, 2.2, 2.4, 3.4, 6.1, 6.2, 6.5, 7.4 |
| Additive upgrade and incompatible older schema knowledge | `internal/persistence/sharing_schema_test.go`: `TestSharingSchemaConstraints`, `TestSharingUpgradePreservesSchema4Data`; migration `0005_menu_sharing.sql` and migration loader | 5.6, 7.1 |

Sharing public deliveries are ordinary new sends with no controls, edit target
or ephemeral receiver. Earlier public messages remain unchanged; retention
removes eligible local delivery content, not Telegram messages. Each displayed
view has its own cooldown; opening another view does not create a group-wide
cooldown. Definite rejection permits a deliberate retry; unknown outcomes use a
fixed hold and are never automatically reposted. Personal view failure has no
public/private-chat fallback, and failed confirmation cannot compensate with
another public post.

## Migration and operator compatibility

`TestSharingUpgradePreservesSchema4Data` migrates an isolated schema-4 database
containing character, catalog, inventory quantities, Russian group settings and
settings-control fixtures. It compares the existing gameplay/settings tables
and processed-update table before upgrade and after reopening schema 5, checks
both new sharing tables, then invokes the schema-4 migration set against that
database and requires `SchemaCompatibilityError` plus `ErrFutureSchema`.
This tests the old schema-knowledge refusal path; it does not run an archived
schema-4 executable or perform a production backup/restore drill.

[README](../../../README.md), under Sharing personal views, documents the
additive 0005 migration and rollback expectations. Stop the single active
replica and back up the **entire** local SQLite storage directory before
upgrade; keep database and sidecars together. A schema-4 binary refuses schema
5. Roll forward, or restore a compatible stopped whole-directory snapshot with
its matching binary. Do not downgrade by removing sharing tables or copying an
individual active database file. Sharing introduces no Telegram command-menu
scope changes. No `go.mod` or `go.sum` changes appear in the implementation
range `024ab71..2b5d30e`.

## Reproduction and live evidence boundary

For automated restart and retention reproduction, use isolated test databases:

```sh
devenv shell -- go test -count=1 -run 'TestSharingRecovery|TestSharingRetention' ./internal/sharing
devenv shell -- go test -count=1 -run TestSharingUpgradePreservesSchema4Data ./internal/persistence
```

These narrower commands select tests included in the successful uncached command
above; they are reproduction instructions, not additional claimed executions.

The [character instructions](manual-verification.md) include exact setup,
`./scripts/build`, `./scripts/run`, `./scripts/probe readyz`, no-username
attribution, pending/double-tap suppression, deliberate repost after 30 seconds,
retained copies, long-lived character controls and failed-delivery limitations.
The [bilingual inventory instructions](inventory-manual-verification.md) include
the same build/run entry points, English/Russian page/detail flows, equipment
browsing, inherited five-minute controls, settings authority, old snapshot
language/current guidance and retained reposts.

An operator can additionally check restart in that same isolated live setup:
after observing a confirmed public share, stop the runtime with Ctrl-C; restart
with `./scripts/run` using the same isolated storage, then run
`./scripts/probe readyz` in another terminal. Restart itself must add no public
copy. If restart finishes within 30 seconds of the confirmed publication and
the source is still usable, press its Share and expect only personal cooldown
guidance. After at least 30 seconds, deliberately press it again: a new copy
must appear and the earlier one must remain. If startup exceeds the cooldown,
that run cannot establish the unelapsed-cooldown observation. Reopen expired
inventory controls; do not infer character expiry from inventory behavior.
Do not interrupt a real send merely to manufacture uncertainty: automated
recovery tests establish that boundary. For an actual uncertain outcome,
guidance must distinguish uncertainty and the operator should check the group
before deliberately requesting another copy when permitted.

**LIVE NOT PERFORMED:** no human Telegram client was operated, no feature was
deployed, and no real message or live restart was observed. Character task 3.4,
bilingual inventory task 4.4 and the optional live restart reproduction above
remain unverified. Requirements 7.2 and 7.3 have runnable instructions and
truthful evidence separation; mandatory actual client observations are still
outstanding. Automated PASS does not close those tasks or authorize GO.

## Residual-marker and credential audit

The tracked feature implementation range was inspected with:

```sh
git diff --name-only -z 024ab71..HEAD | xargs -0 rg -n '\b(TODO|FIXME|TBD|XXX)\b'
git diff --name-only -z 024ab71..HEAD | xargs -0 rg -l 'BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY|[0-9]{6,}:[A-Za-z0-9_-]{20,}|AKIA[A-Z0-9]{16}|gh[pousr]_[A-Za-z0-9]{20,}'
```

The marker search returned no matches (expected `rg` no-match status). The
credential-pattern search returned only
`internal/sharing/retention_integration_test.go`: its declared
`retentionCredential` and SQL fault fixture use an explicit synthetic privacy
sentinel, not a real credential. No credential values or environment content
were printed by that scan. This bounded pattern/source audit is not a claim of
general secret-scanner coverage. This record was also checked for unresolved
markers before handoff; the regex examples above are documentation, not pending
work. Unrelated existing roadmap/devenv changes and untracked files were left
intact.
