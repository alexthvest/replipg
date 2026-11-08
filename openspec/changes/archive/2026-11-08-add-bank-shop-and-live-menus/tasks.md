# Tasks

## 1. Offers, amounts, and content compatibility

- [x] 1.1 Add pure offer/quote/order types and exact integer money arithmetic under `internal/modules/shop`; verify adjacent tests cover optional directions, duplicate offer targets, sell/buy ordering, quantities 1..1000, and 100-digit amount/total limits.
- [x] 1.2 Add `assets/shop/schema.cue` and separate `package shop` decoding/indexing, using one `assets/shop/<item_id>.cue` file per configured item with a flat catalog-ID key; verify existing item schemas/assets and saved snapshot JSON remain unchanged, missing/schema-only shop packages mean no offers, buy offers require active matching definitions, and sell-only offers accept old snapshots.
- [x] 1.3 Capture and watch shop, items, and locales as one coherent candidate, including asset-root events for shop directory creation/removal/recreation; verify reload tests retain the previous complete bundle for invalid offers/translations and never mix quote/item versions. Document the shared schema and one-file-per-item examples in README without enabling arbitrary live prices.

## 2. Durable orders and inventory reservations

- [x] 2.1 Add the next SQLite migration for orders, financial requests/attempts, proof records, and reservations with group/character ownership constraints and confirmation/transaction uniqueness; verify upgrade tests preserve existing groups, snapshots, ownership, and grant replay results and reject unsupported schema rollback.
- [x] 2.2 Implement transaction-bound order creation, frozen request intent, duplicate-confirmation lookup, due work, and conditional state/proof transitions; verify close/reopen tests and injected failures cannot lose intent, regress terminal outcomes, or reuse one financial proof for multiple orders.
- [x] 2.3 Implement transaction-bound reservation, available-quantity reads, release, decrement, and entry deletion; verify competing stack/instance sales cannot over-reserve and failed transactions leave ownership and reservations intact.
- [x] 2.4 Extend inventory read models/views for reserved and available quantities and add projection removal/reconciliation; verify owned quantities remain unchanged before payout, old snapshots/identities persist, and a completed last-unit sale removes the entry from both SQL and runtime state.

## 3. External bank protocol and trusted transport

- [x] 3.1 Add the narrow application bank port and JSON-RPC codec in `internal/adapters/bank`; verify contract fixtures for all documented financial states, generic errors, ID types, exact method/params spelling, optional transaction fields, and byte/UTF-16/envelope limits without live network calls.
- [x] 3.2 Add private bank response dispatch and unformatted numeric-destination sending with the existing Telegram client; verify fake API/router tests reject foreign senders/chats, forwards, edited messages, unsolicited IDs, mismatched amounts/accounts, and malformed replies while preserving unsupported private gameplay behavior.
- [x] 3.3 Add bank configuration and disabled/unavailable behavior, with treasury default 2 and persisted identity consistency checks; verify parser/startup tests reject invalid supplied values, retain existing gameplay when bank configuration is absent/offline, and prevent old intent from being replayed to a changed bank/account.
- [x] 3.4 Document verified numeric bank identity, private bot-to-bot mode, bank permissions, group invoice delivery access, and placeholders in README/.env.example; verify documentation distinguishes external prerequisites from features implemented in replipg and contains no secrets or assumed live balance.

## 4. Shop transactions and recovery

- [x] 4.1 Implement quote confirmation with frozen snapshots/prices/player/chat parameters and durable intent before send; verify changed quotes require another review and repeated confirmation updates return one order without duplicating requests or reservations.
- [x] 4.2 Implement verified purchase payment recording and atomic local fulfillment using inventory grant rules; verify pending/operation-only responses grant nothing, immediate paid creation fulfills once, duplicate settlement is harmless, and offer/item removal cannot change pending obligations.
- [x] 4.3 Implement sale reservation-before-send and atomic payout settlement or proven-failure release; verify admin-granted items are accepted by offers, self/invalid/missing-bank-account failures do not consume items, generic errors do not invent reasons, and unknown prior attempts preserve reservations.
- [x] 4.4 Implement one bounded recovery worker using durable due work, original financial keys/payloads, fresh status read IDs, backoff, and typed runtime completion operations; verify fake-clock tests recover lost creation/transfer replies, late/contradictory replies, permission revocation after unknown sends, bank outages, and request conflicts without new charges/payouts.
- [x] 4.5 Retain verified paid/payout-confirmed states across fulfillment failure and restart; verify failure injection at intent commit, send observation, proof commit, and ownership commit converges on one completed outcome, including numeric stack overflow remaining recoverable without another charge.
- [x] 4.6 Add actionable order/reconciliation diagnostics and document original-key operator recovery, indefinite pending invoices, shared treasury, admin-grant payout policy, and safe rollback restrictions; verify no recovery path guesses settlement from balance/history or force-releases an unknown payout.

## 5. Menu state, subscriptions, and ordered delivery

- [x] 5.1 Extend sessions with current screen/selection, dependencies, dirty state, desired/delivered revisions, and semantically stable action references; verify adjacent tests preserve page/inspection, clamp removed pages, return from removed item inspection, and reject obsolete/foreign controls.
- [x] 5.2 Add dependency indexes and committed-state invalidation for inventory, reservations, orders, character, language/permissions, and content; verify fake-clock tests refresh only affected bound sessions, render as each opener, close the initial send/bind gap, and do not extend TTL on background work.
- [x] 5.3 Add a bounded coalescing menu delivery coordinator and route callback and automatic edits through it; verify delayed transport tests enforce one in-flight edit per message, deliver the newest screen after navigation races, suppress unchanged views, and avoid blocking the runtime or unrelated ready messages during backoff.
- [x] 5.4 Preserve typed Telegram edit outcomes and implement retry-after/transient retry, not-modified success, and permanent-message unsubscribe; verify failures never repeat domain operations and expired/evicted sessions cancel queued work without posting replacement messages.
- [x] 5.5 Wire commit invalidation into grants, self-grants, rename, settings/admin actions, and successful asset publication; verify integration tests update multiple active inventory/settings/catalog menus and preserve group/player isolation and saved owned-item text.

## 6. Shop menus and application lifecycle

- [x] 6.1 Add `/shop` registration and automatic current-group character creation for valid opening; verify route tests cover malformed input, first-time players, ownership, opener-only actions, and isolation from private/channel gameplay.
- [x] 6.2 Implement paginated Buy, Sell, and Orders views, quantity selection, review confirmation, and pending/paid/completed/failure/reconciliation screens; verify view/route tests cover empty offers/inventory, old sellable snapshots, exact unit/total display, stale quotes, repeated clicks, and current available quantities.
- [x] 6.3 Add complete flat English/Russian keys and matching schema groups for shop, bank availability, reservation labels, and order outcomes; verify locale parity and safe formatting and document that existing six-row inventory list formatting is retained.
- [x] 6.4 Wire bank and menu workers in `internal/app`, with app-owned contexts, typed owner operations, bounded queues, and joined shutdown; verify startup/restart/shutdown tests preserve pending financial intent and reservations, discard old menu sessions, and close workers before database/transport cleanup.

## 7. Integrated acceptance and required verification

- [x] 7.1 Add an end-to-end fake-bank/fake-Telegram acceptance test: confirm purchase, open inventory while pending, report paid, and observe the same inventory message update without a new command; verify one grant and one financial request across duplicate replies and a delayed edit/navigation race.
- [x] 7.2 Add an integrated sale/restart scenario with admin-granted stack and individual items, competing sales, a lost payout reply, and original-key recovery; verify exactly one payout, preserved reservation until proof, correct final ownership, and automatic refresh of inventory, sale, and order screens.
- [x] 7.3 Format changed Go/CUE files with `gofmt -w` and `cue fmt` in the repository devenv shell; run `go mod tidy` only if imports/dependencies change and verify go.mod retains its declared Go version and no unjustified dependency is introduced.
- [x] 7.4 Inside `devenv shell` (copy `.env.example` only if `.env` is absent), run `go test ./...`, `go test -race ./...` where supported, `go vet ./...`, `go build ./...`, `go build ./cmd/replipg`, and `golangci-lint run ./...`; record actual results and any environmental blockers, without making live financial calls.
- [x] 7.5 Inside the devenv shell run `cue vet -c=false ./...` and `cue vet -c ./assets/items ./assets/locales ./assets/shop`; run `openspec validate add-bank-shop-and-live-menus --strict`, review implementation against each acceptance scenario, and report completion without archiving the change.

## 8. Reference shop presentation follow-up

- [x] 8.1 Adapt Buy, Sell, review, My Orders, and order details from `~/projects/replipg-go`, including compact styled headings, six-item pagination, grouped and styled controls, literal descriptions and totals, localized settlement notices, and Unicode-safe line wrapping. Preserve existing financial behavior and verify both languages, navigation, callback identity, and automatic updates.

- [x] 8.2 Replace global Buy/Sell modes with a unified six-item catalog and inventory-style page footer. Offer Buy/Sell after item selection, use plain buttons and `1 − + Max` quantity controls, remove redundant purchase/sale notices, and preserve saved snapshot eligibility, confirmation/recovery behavior, live updates, and personal Orders. Verify both locales and financial acceptance scenarios.

- [x] 8.3 Hide the quantity field and quantity controls for non-stackable shop items, use quantity one for new individual purchases and sales, and retain historical order quantities. Verify individual Buy/Sell reviews in both locales.

## Verification results

- [x] Inventory follow-up: hide quantities for individual list entries and list entries in acquisition order, oldest first, preserving stack positions and restart order. Update inventory specifications and verify view formatting, database ordering, tests, vet, build, lint, and strict specification validation.

All checks passed on 2026-11-08. Go and CUE commands ran inside `devenv shell`:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- `go build ./cmd/replipg`
- `golangci-lint run ./...` (0 issues)
- `cue vet -c=false ./...`
- `cue vet -c ./assets/items ./assets/locales ./assets/shop`
- `openspec validate add-bank-shop-and-live-menus --strict`

Changed Go/CUE files were formatted and `go mod tidy` left `go.mod` and `go.sum`
unchanged, including the declared Go 1.26 version. Acceptance scenarios use fake
bank and Telegram transports with temporary SQLite databases. No live financial
calls were made. There are no remaining verification blockers.

The reference-presentation follow-up passed `go test ./...`, affected-package race
tests, `go vet ./...`, `go build ./...`, lint (0 issues), both CUE checks, and strict
OpenSpec validation. Dedicated English/Russian layout tests verify the unified
catalog, six selectors, page footer, plain item-level Buy/Sell controls, the four
quantity controls, and order navigation. Max is tested against ownership and the
bank amount limit. Purchase, competing sale, restart recovery, and automatic menu
update acceptance scenarios also pass. Live Telegram appearance was not exercised.
