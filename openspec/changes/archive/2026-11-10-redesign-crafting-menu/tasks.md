# Tasks

## 1. Composition and draft state

- [x] 1.1 Add crafting-specific composition snapshots, independent page positions, editor origin, and draft quantity state under `internal/ui/menus`; verify focused tests cover adding, saving, abandoning edits, removing, clearing, and more than six selected ingredients.
- [x] 1.2 Preserve requested snapshots and quantities after inventory disappearance, invalidate unsubmitted confirmations on committed composition changes, and retain exact-instance semantics; verify shortage, overflow boundary, and confirmation invalidation tests.

## 2. Composition, browser, editor, and review

- [x] 2.1 Render the composition with numbered pages and exact Add / Clear then Confirm / Close rows; add required English/Russian keys and CUE schema entries together and verify empty, six-entry, partial-page, and bilingual view tests.
- [x] 2.2 Wire Add to the six-entry material browser and both numbered routes to the shared draft editor; verify route tests cover saved versus cancelled edits, return pages, repeated material selection, and individual materials.
- [x] 2.3 Add final review, empty/invalid Confirm feedback, and visible required/available shortages; verify no browsing or editing consumes ownership and duplicate final confirmation still resolves once.
- [x] 2.4 Document the composition, Add, Save, Back, Clear, and confirmation flow in README; verify examples match rendered labels and row order.

## 3. Pending, success, and failure actions

- [x] 3.1 Render immutable consumed ingredients and submitted pending ingredients with bounded pagination, output details, and state-specific keyboards; verify long-selection, pending-language-change, disabled discovery, technical-error, and shortage scenarios.
- [x] 3.2 Implement repeat preparation using complete saved snapshots and exact quantities, allocating current matching ownership without reservations or double counting; verify exhausted originals, identical replacement instances, differing snapshot versions, split stacks, and unresolved shortages.
- [x] 3.3 Implement New craft and Retry with fresh review tokens and no implicit submission; verify old accepted callbacks cannot submit a new composition and explicit retry preserves idempotency.
- [x] 3.4 Add Use in crafting for material results and seed one available matching output; verify nonmaterial omission, sold/reserved output feedback, and no additional grants or consumption.
- [x] 3.5 Route Inventory to current-group ordinary inventory navigation; verify opener checks, page controls, and isolation from stale crafting state.
- [x] 3.6 Document result actions, renewed confirmation, and shortage handling in README; verify documented journeys with deterministic route tests.

## 4. Closing and live menu consistency

- [x] 4.1 Close with a terminal view without controls and retire the bound session after delivery, rejecting callbacks immediately; verify delivery failure/retry, delayed refresh ordering, and no edits after terminal delivery.
- [x] 4.2 Preserve screens, pages, and drafts across inventory and language refresh while reporting reduced availability; verify stale/foreign callbacks and existing projection-failure regressions remain protected.
- [x] 4.3 Verify closing a pending menu leaves durable generation and inventory fulfillment intact, using the existing controlled generator test fixture; document that closing does not cancel crafting.

## 5. Integration verification

- [x] 5.1 Copy `.env.example` to `.env` only if absent, enter the repository devenv shell, format changed Go with `gofmt -w` and CUE with `cue fmt`, and verify the resulting diff follows repository conventions without unrelated changes.
- [x] 5.2 Inside devenv run `go test ./...`, `go vet ./...`, `go build ./...`, and `golangci-lint run ./...`; record results and any environment-blocked checks accurately.
- [x] 5.3 Inside devenv run `go test -race ./...` for session delivery and worker interaction coverage when supported, plus `cue vet -c=false ./...` and `cue vet -c ./assets/items ./assets/locales`; record results.
- [x] 5.4 Run `openspec validate redesign-crafting-menu --strict` and review the final change against all menu scenarios; verify task checkboxes reflect actual implementation and verification, without archiving or syncing changes.

## Validation results

- Formatting: changed Go files formatted with gofmt and locale files with cue fmt inside devenv. go mod tidy completed without changing go.mod or go.sum; the declared Go version is preserved.
- Passed: go vet ./..., go build ./..., golangci-lint run ./... (0 issues), cue vet -c=false ./..., and cue vet -c ./assets/items ./assets/locales.
- Full go test ./... and go test -race ./... were run. Both fail only in TestShopReferenceLayoutAcrossBuySellAndOrders (English and Russian): its fixture expects iron_sword, which is absent from the existing catalog. This unrelated test and production item definitions were left unchanged.
- Passed: go test ./... -skip '^TestShopReferenceLayoutAcrossBuySellAndOrders$' and the same command with -race. Subsequent focused race tests cover final available-output matching, reserved outputs, intentional repeated crafting, draft refresh, and all new menu journeys.
- Passed: openspec validate redesign-crafting-menu --strict and git diff --check.
- Menu behavior is verified with deterministic fake Telegram transport and controlled generators; no live Telegram messages or model calls were sent for these checks.
- No change was archived or synced.

## Archive verification (2026-11-10)

- Synced all requirements and scenarios to
  `openspec/specs/crafting-menus/spec.md`, verified the copied content, and
  archived this completed change with its AI crafting dependency.
- Tested the isolated staged crafting snapshot in devenv. Build, vet, lint, and
  both CUE checks passed. Full ordinary and race tests failed only in the
  existing English/Russian shop-layout cases missing `iron_sword`; all other
  tests passed, with no reported races. Live Telegram and inference were not run.
- Strict change validation passed before archive and main-spec validation passed
  after sync. All 19 tracked tasks were complete at archive.
