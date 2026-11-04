> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

# Inventory Equipment Validation

## Decision

**MANUAL_VERIFY_REQUIRED** — software implementation and automated release gates pass. The owner confirmed personal inventory rendering and equipped weapon bonuses with unchanged character base stats, then reported successful validation of that behavior. Other live checklist outcomes were not individually supplied. Tasks 3.3, 4.5 and 7.4 intentionally remain unchecked for their remaining live components; 28 of 31 executable subtasks are fully checked. Their automated portions were independently approved.

Independent requirements review mapped **65/65 acceptance criteria across 9/9 sections**. Software or documentary evidence is sufficient for 64 criteria and the automated portion of 9.7. The live milestone component of 9.7 remains partial under project incremental-delivery steering. No concrete software defect or upstream remediation was identified. Ownership of the remaining delivery evidence is **LOCAL**; upstream spec: N/A.

## Mechanical evidence

Fresh final commands on 2026-10-28:

| Command | Exit | Result |
|---|---:|---|
| `./scripts/check` | 0 | Formatting clean; 19 packages pass normal/race suites; vet passes; lint reports zero issues. Some package results were cached. |
| `./scripts/smoke` | 0 | Built production executable reaches usable runtime state against local fake Telegram and shuts down cleanly. |
| `./scripts/build` | 0 | Production executable builds. |
| `devenv shell -- go test -count=1 -run '^TestInventory' ./cmd/replipg` | 0 | Seven matching top-level inventory tests pass uncached. |
| `devenv shell -- go test -count=1 ./internal/architecture` | 0 | Three top-level boundary tests pass uncached. |

Counts of top-level tests come from source inspection; ordinary command output does not enumerate them. No failures or skips were reported. A final scan of 61 feature source/config files found zero placeholder markers and zero hardcoded credentials; inspected secret-pattern matches were synthetic fixtures, empty configuration settings, SQL predicates and ordinary token variables.

Independent reviewers also ran focused inventory, app, persistence, executable and repeated concurrency/race checks. All final task verdicts approved their automated scope. The effective-stat service-port regression was checked retrospectively using an isolated build overlay that removes only the method: interface satisfaction then fails compilation, while actual-source tests pass. This is regression sensitivity evidence, not a claim that this small supplemental adapter was developed chronologically test-first.

## Requirements coverage

Paths are relative to the repository root. Test files below are under `internal/inventory` unless another package is shown.

| Criteria | Concrete implementation and evidence | Status |
|---|---|---|
| 1.1–1.7 | Embedded strict immutable catalog, four categories and inert Coin; `catalog_test.go`, contract tests, saved-mechanics preparation and production preparation tests. | Software verified |
| 2.1–2.7 | Character-service `Open`, operation savepoint, permanent starter receipt and one-way entitlement; `service_test.go`, `quantities_test.go`, concurrent opening, changed tuning, restart and failure/retry fixtures. | Software verified |
| 3.1–3.9 | Original timestamp/human/group authorization, bounded indexed pages, numbered rendering, transient labels, restricted replies and read-only Coin/potion details; command/render/store tests, binary inspection/pagination and privacy integration. | Software verified |
| 4.1–4.9 | Compatible two-slot assignments, unchanged ownership/base attributes, checked bonuses, revision/receipt mutation and token-bound actions; rule/store/service tests, 16 competing controls across groups and actual binary replacement/restart tests. | Software verified |
| 5.1–5.6 | Existing-target caller-resolved grants, checked sorted duplicate aggregation, root initialization without starter loss and operation isolation; `grants_test.go`, schema constraints and concurrent/restart/replay fixtures. | Software verified |
| 6.1–6.7 | Frozen self-healing effect, character-port normalization and health application, one-copy decrement/deletion, no standalone Use action; healing bounds/recovery/replay tests, caller-owned action faults and executable healing lifecycle. | Software verified |
| 7.1–7.6 | Actor/group/character binding, opaque references, expected revision, absolute expiry, safe reopen and refreshed replies; control/handler tests, concurrency and binary valid/expired controls across restart. | Software verified |
| 8.1–8.7 | Permanent canonical receipts, later-state replay, immediate caller transactions, local savepoints, whole-action rollback and post-commit delivery recovery; operations/coordination/failure/concurrency tests and runtime privacy integration. | Software verified |
| 9.1–9.6 | Numeric identity ownership, transient labels, sanitized diagnostics, strict independent development flag, documented demonstrations, isolated fake-transport fixtures and controlled domain time; privacy/development/composition/executable tests and README/manual checklist. | Software/documentation verified |
| 9.7 | Runnable production scenarios, automated milestone tests and reproduction instructions exist. Required actual test-group observations have not been recorded. | Partial; live verification required |

## Design and integration

Dependency direction matches the approved design. Inventory consumes exact character contracts and delegates health/recovery through an injected port; future gameplay can consume exact inventory contracts without importing implementation. Runtime preparation is domain-neutral and runs transactionally after migrations and before registration, Telegram setup or admission. Composition shares the existing character service and publishes the complete managed menu.

Migration 0003 is additive. Positive ownership backs compatible equipment; operation receipts are permanent and control expiry is separate. Catalog preparation rejects removed or mechanically changed identities before admitting updates, without granting starters. New identities/display edits and existing-character empty inventories behave as specified.

All mutation methods retain caller commit ownership. Replay resolves before current eligibility/revision checks and returns the original application revision with current state. Healing coordinates character and inventory receipts without implementing Battle policy. Cleanup storage failures take precedence over joined domain rejections in handlers, preserving dispatcher rollback and retry semantics.

File organization preserves the design responsibilities; additional equipment, grant, healing and quantity files are cohesive subdivisions. No dependency, module, lock-file or deployment infrastructure change was required. No workspace live database was inspected or modified.

## Remaining delivery gate

Run and record the three dedicated test-group demonstrations in `manual-verification.md`: starter/inspection and cross-group/restart continuity; equipment replacement/unequip/stale controls; grants/healing acceptance and rejection plus disabled probes. Until actual observations are recorded, retain the manual-required decision and unchecked gates 3.3, 4.5 and 7.4. No live Telegram messages were sent during automated verification.

Operational compatibility remains explicit: schema version 3 is rejected by a version-2 binary; roll forward or restore a compatible stopped whole-directory backup with its matching binary. Saved catalog mechanics remain immutable. See README and the manual checklist for reproduction and rollback details.

## In-place inventory navigation follow-up — 2026-10-28

Owner authorized changing button responses to edits. Requirement 7.7 adds the
same-message behavior to the earlier requirements baseline. This follow-up was
reviewed inline; no independent subagent review was performed.

- **Automated change verification: VERIFIED.** Inventory callbacks target the
  originating ephemeral message; commands send new views. Exact transport tests
  cover refreshed keyboards, explicit button removal, invalid edit authorization,
  rejected edits without fallback, and safe failure results. Executable tests
  assert send versus edit behavior for navigation, pagination and equipment.
- Working-tree checks passed with the existing translated catalog:
  `devenv shell -- go test -count=1 -run 'TestInventoryProductionBinaryEditFailurePreservesEquipment|TestEphemeral(Edit|Invalid)' ./cmd/replipg ./internal/telegram`.
  The production executable retains committed equipment after a rejected edit,
  then successfully reopens and unequips it.
- The initial working-tree package run failed existing English display-name
  expectations after local `items.json` translations. Those edits were preserved.
  A temporary source copy containing all changed Go files and the committed
  catalog passed formatting, `go test ./...`, `go vet ./...`,
  `golangci-lint run` (zero issues), `go test -race ./...`, and
  `go test -count=1 -run '^TestProductionBinarySmoke$' ./cmd/replipg`.
  All ran in the repository's pinned devenv shell. `GOFLAGS=-buildvcs=false`
  disabled VCS stamping because the isolated copy has no Git metadata.
- `git diff --check` passed in the working tree. No dependencies or schema changes.
- **Live change verification: MANUAL_VERIFY_REQUIRED.** No live Telegram traffic
  was sent. Follow the in-place navigation checklist in manual-verification.md.
  Existing owner confirmations predate this change and do not verify editing.
