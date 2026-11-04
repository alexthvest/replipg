# Item System Tasks

Approved by the user's “Implement the plan” on 2026-11-04. Check tasks only after implementation verification and independent review; specification creation is not evidence of execution. Inventory split tasks 8.1–8.3 track the coordinated work.

- [x] 1.1 Extract immutable item contracts/catalog and integrate inventory consumption
  - Move values, strict validation, mechanics and seed definitions into stdlib-only item boundary; preserve tuning/IDs and remove aliases/old inventory catalog implementation.
  - Add defensive immutable lookup/list/effect tests, invalid-content tests, consumer-owned inventory port and copied/validated separate starter lines; preserve inventory errors, persistence compatibility, receipts and healing behavior.
  - Done when focused item/inventory/architecture compatibility checks pass with independent review.
  - _Requirements: 1.1–1.4, 2.1–2.5, 4.1_

- [x] 1.2 Integrate and verify owner-only application item probe
  - Implement /dev_item, enabled menu descriptions, strict positive owner configuration and owner-first authorization across all inventory development commands; retain self-only mutations and existing runtime/character modes.
  - Document and test owner success, non-owner/administrator rejection, ordinary/private/wrong authorization, safe unknown/usage output, disabled mode and zero gameplay state changes.
  - Done when fake-Telegram executable/composition evidence passes with independent review and reproduction steps are complete.
  - _Depends: 1.1_
  - _Requirements: 3.1–3.7, 4.2, 4.3_

- [ ] 1.3 Close automated validation and live demonstration gate
  - Run pinned scripts/check and scripts/smoke plus focused compatibility/import verification and final independent review; record actual fresh outputs.
  - Record owner /dev_item, non-owner rejection, zero mutations and inventory continuity in a real test group only when observed; retain MANUAL_VERIFY_REQUIRED and this unchecked task otherwise.
  - _Depends: 1.2_
  - _Requirements: 4.1–4.3, 3.1–3.7_

## Implementation Notes

- 2026-11-04: Tasks 1.1 and 1.2 passed independent review. Final `./scripts/check` and `./scripts/smoke` both exited 0 on the combined workspace. Task 1.3 remains unchecked solely for the unperformed live Telegram demonstration; automated evidence is in validation-report.md.
