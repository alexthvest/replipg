# Item System Validation

Status: **SOFTWARE_VERIFIED / MANUAL_VERIFY_REQUIRED**. Requirements, design and tasks are approved by the user's 2026-11-04 request to implement the plan. Tasks 1.1 and 1.2 passed independent review and automated verification. Task 1.3 remains open for live delivery evidence.

Fresh evidence below covers catalog/contract/inventory/composition tests, existing-schema/receipt compatibility, owner/non-owner/admin/config/disabled/zero-state fake-Telegram cases, pinned scripts/check and scripts/smoke. Live Telegram item inspection and owner-access evidence remain pending in manual-verification.md. Historical combined inventory checks do not prove this extraction.

## Final integration validation — 2026-11-04

**Decision: MANUAL_VERIFY_REQUIRED.** No software defect or blocked implementation task remains. The sole remaining gate is the real test-group demonstration described in manual-verification.md. All automated Telegram traffic used isolated fake endpoints and temporary SQLite storage.

| Fresh command | Exit | Evidence |
| --- | --- | --- |
| `devenv shell -- golangci-lint fmt ./...` | 0 | Applied required formatting. |
| `./scripts/check` | 0 | Configuration verification, formatting verification, zero lint issues, all normal tests, build and race tests pass through devenv. |
| `./scripts/smoke` | 0 | Built production executable starts, handles fake Telegram and shuts down successfully. |

Independent operator review also ran the full suite, focused inventory/settings/localization tests, build, configuration verification and lint successfully after concurrent runtime edits settled. Both task reviews approved their assigned boundaries.

| Requirements | Current implementation/evidence |
| --- | --- |
| 1.1–1.4, 2.1–2.5 | Pure item contracts/catalog, strict content and contract tests, immutable lookup and constructor-copy tests. |
| 3.1–3.3, 3.6–3.7 | Strict configured owner, owner-first handler checks, valid ephemeral human group context, disabled routes/menu and unchanged player access; command/unit/executable tests. |
| 3.4–3.5 | Bilingual category/mechanics/usage/unknown output; read-only locale resolution and eleven empty feature-table assertions during actual executable inspection. |
| 4.1–4.2 | Literal legacy SQL/JSON replay fixture; unchanged migrations/dependencies; full normal/race suites, smoke and direct import inspection. |
| 4.3 | Commands, prerequisites, results and access rejection documented; actual Telegram observations remain pending. |

Cross-task contracts, starter validation, persistence compatibility, localization coverage and startup composition align with the designs. Item has no inventory, localization, runtime or persistence imports; inventory consumes exact item contracts. Existing grants, equipment, healing and replay regressions pass. No compatibility aliases, new dependencies, schema changes or unresolved local findings were introduced. Ownership of the remaining evidence is LOCAL; upstream remediation is not required.

## Extraction evidence — 2026-11-04

Task 1.1 and inventory task 8.1 passed independent review. Parent verification ran `devenv shell -- go test -count=1 ./internal/item/... ./internal/inventory/...` with exit 0. The independent reviewer ran `devenv shell -- go test ./...` and `devenv shell -- golangci-lint run ./...`, both exit 0 with zero lint issues. These results precede operator integration and do not establish its completion.

`TestInjectedContentIsValidatedAndCopied` and `TestInjectedInvalidCatalogAndStarter` verify defensive copies, invalid ports and inventory error translation. `TestLegacyInventoryEquipmentAndReceiptFixture` reopens literal pre-extraction SQL/JSON and checks ownership, equipment and original grant/equipment/frozen-healing receipts against current definitions. Migration files and schema remain unchanged. `go list` confirmed item production imports only standard library and the item contract, while inventory imports item contracts rather than the catalog implementation. The concurrently retired architecture checker was preserved as removed; direct import inspection supplies this boundary evidence.
