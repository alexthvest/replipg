# Tasks

## 1. Component values and snapshot compatibility

- [x] 1.1 Add comparable marker composition and membership/enumeration helpers under `internal/modules/items`, with strict object codecs. Verify adjacent tests cover all marker combinations, empty sets, unknown types, nonempty marker properties, null/scalar/list inputs, decoder reset, and detached value copies.
- [x] 1.2 Extend Definition and Snapshot with components and integrate snapshot serialization and validation. Verify literal pre-change version-1 JSON/digest fixtures remain identical for empty components, explicit empty and omitted components canonicalize identically, nonempty components round-trip, declaration order does not affect equality/bytes, and membership changes do affect equality/bytes.

## 2. Validated assets and coherent capture

- [x] 2.1 Extend `assets/items/schema.cue` with closed component and marker schemas and an omitted-field empty default. Add CUE loader fixtures for multiple markers and invalid shapes; verify real legacy assets without components remain valid and no catalog item is classified by inference.
- [x] 2.2 Copy components through `internal/content` capture and retain ownership-mode compatibility rules. Verify content tests show definition access/copies cannot mutate published data or saved captures and snapshots remain tied to one bundle after component edits or definition removal.
- [x] 2.3 Add watcher regression coverage proving valid component edits publish for future captures and invalid components preserve the complete previous bundle with diagnostics. Verify invalid required startup component assets fail loading.

## 3. Localized inspection

- [x] 3.1 Add required component labels and an empty classification value to locale schemas, matching English/Russian locale groups, and Go locale validation. Verify missing labels reject a candidate and complete locale assets validate; preserve flat dotted keys, matching order, and one language block per file.
- [x] 3.2 Render saved components in item inspection using current localized labels in stable component-key order. Verify view tests cover both languages, multiple/empty/legacy component sets, changed or deleted live definitions, and no new item actions or inventory-row layout changes.

## 4. Durable integration and replay

- [x] 4.1 Add SQLite tests using literal pre-component persisted snapshots: reopen the database, grant identical unclassified items, and verify reuse of the original snapshot ID, stack ID, and acquisition position. Verify different membership creates separate stacks and reordered membership merges into the matching stack, including after restart; retain collision-safe byte comparison tests.
- [x] 4.2 Verify legacy and classified grant-operation replay and saved shop-order recovery preserve complete snapshots, component membership, replay idempotency, and sale reservations. Add adjacent regression tests using old JSON fixtures and nonempty components without rewriting historical data or introducing a database migration.

## 5. Integration verification

- [x] 5.1 Use the repository devenv environment, copying `.env.example` only if `.env` does not exist; format changed Go and CUE files with `gofmt -w` and `cue fmt`. Run `go test ./...`, `go vet ./...`, `go build ./...`, `golangci-lint run ./...`, `cue vet -c=false ./...`, and `cue vet -c ./assets/items ./assets/locales`; record actual outcomes and any blocked checks. Run `go test -race ./...` if implementation changes concurrent behavior, where supported.
- [x] 5.2 Run `openspec validate add-item-components --strict` and review the final implementation against both delta specifications, canonical-byte compatibility fixtures, and the documented future encounter snapshot boundary. Update task checkboxes only for implemented and verified work; leave archiving for an explicit request.

## Verification results

- Used the repository devenv shell (Go 1.26.10); formatted changed Go and CUE files and ran `go mod tidy` without dependency changes.
- Passed: `go vet ./...`, `go build ./...`, `golangci-lint run ./...` (zero issues), `cue vet -c=false ./...`, and `cue vet -c ./assets/items ./assets/locales`.
- `go test ./...` ran: all packages passed except three existing `internal/app` tests: `TestItemsOwnerOnlyNavigationAndLanguage`, `TestItemsButtonsGrantToOwnerAndProtectReplays`, and `TestShopReferenceLayoutAcrossBuySellAndOrders` (both languages). They expect more than one catalog page and missing `iron_sword`/related catalog fixtures. The same failures were reproduced from `HEAD` plus the pre-existing staged asset edits in an isolated temporary baseline copy. Those unrelated assets and tests were preserved.
- Passed focused race tests: `go test -race ./internal/adapters/cueassets ./internal/adapters/sqlite ./internal/content ./internal/modules/items ./internal/modules/localization ./internal/ui/views`. No concurrent implementation behavior was added or changed, so the conditional full race run was not required.
- Passed: `openspec validate add-item-components --strict` and `git diff --check`.
- Reviewed both delta specifications, literal legacy JSON/digest and database fixtures, classified replay/recovery, and the future encounter reward boundary. `items.Snapshot` remains a detached comparable version-1 value; future encounter capture and rewards can pass the complete extended snapshot through existing inventory transaction ports. No encounter implementation or SQL migration was added.
- Applied explicit user-selected catalog classifications and reran complete CUE asset validation plus the affected loader, content, view, and SQLite tests successfully. Legacy loader fixtures remain independent of current catalog classifications.
- At the user's separate request, backed up the ignored local database and updated 11 existing inventory entries to component-bearing snapshots. Preserved original snapshot rows, item text, entry identities and acquisition order, quantities, historical grant results, and shop quotes. This one-time local operation and its backup are not part of the committed implementation.
