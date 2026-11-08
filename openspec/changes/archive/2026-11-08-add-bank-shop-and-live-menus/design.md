# Design

## Context

See `proposal.md` for motivation and scope. The current application uses one serialized runtime owner for an ECS world, SQLite as durable authority with one connection, immutable bilingual item snapshots, and bounded ephemeral menu sessions. Grant orchestration already commits character creation, ownership, and its replay result in one transaction. Inventory projection currently supports additions/updates but no removal.

The Telegram router currently ignores private messages and bot senders, holds a mutex across routing and response delivery, and edits only the menu that produced a callback. Sessions retain page and character but not whether the player is inspecting an entry. CUE capture/watch handles only items and locales. These are concrete extension points, not reasons to replace the runtime.

`replibank-integration.md` is the external contract. Creation may return paid immediately, `operationId` does not prove payment, and no push notification, invoice cancellation, expiry, or general operation-status API is guaranteed. Account 2 is the only available system account, with no issuance rights. Generic Method unavailable conflates several causes. A lost Telegram response can coexist with a successful financial effect.

## Goals / Non-Goals

**Goals:** Keep local transitions atomic, persist every external financial intent before sending, and make every retry converge on the original operation. Keep bank latency and menu delivery outside the world owner. Reuse existing snapshots, localization, menu authorization, SQLite migrations, and client transport.

**Non-Goals:** A distributed transaction with the bank, a mirror of player balances, a plugin framework/event broker, durable menu delivery across restart, and speculative services for future earnings. Future money sources can reuse the bank port and recovery records, but no generic rewards subsystem is needed now.

## Decisions

### 1. Shop orchestration with a small bank port

Use `internal/application/shop` for cross-module transactions and recovery scheduling; `internal/modules/shop` owns offers, money validation, quotes, and order states. The application declares a narrow bank port for invoice creation/status and transfers. The bank adapter owns JSON-RPC envelopes, correlation, limits, and typed result validation. SQLite implements transaction ports; views never call the bank or mutate storage.

Use `encoding/json`, `math/big`, existing IDs, and the existing Telegram client. Currency is a canonical positive decimal string, with arbitrary precision multiplication and a 100-digit total limit; quantities remain int64 with the existing 1..1000 bound. Transaction and request identifiers stay strings. Avoid float64 decoding for identifiers or amounts. No new module dependency is planned. The interface exists because the external transport is asynchronous and must be substituted deterministically in tests; it is not a general provider registry.

### 2. Explicit order state and durable request records

Persist an order with UUID, group, user, character, direction, immutable item snapshot/reference, quantity, unit/total amounts, quote identity, treasury identity, bank identity, and original financial payload. A unique confirmation identity prevents repeated clicks with different Telegram updates from creating another order; also retain bot/update replay identity. Associate every accepted quote confirmation with its order until the session expires, independently of replaced callback refs.

Persist request records before transmission: financial key (for example `replipg:order:<uuid>:invoice` or `:payout`), exact JSON-RPC params, transport ID, request kind, attempt history, next attempt time, response/transaction proof, and outcome classification. Enforce unique provider transaction IDs for successful financial effects and atomic conditional state transitions. Retain finalized keys and order results; no time-based financial deduplication eviction is safe under this contract.

Purchase states: created -> requesting -> awaiting-payment -> paid -> completed, with rejected/failed terminal nonpayment outcomes where proven, and reconciliation-required for ambiguous or contradictory evidence. Sale states: reserved -> payout-requesting -> payout-confirmed -> completed; proven nonpayment releases the reservation and fails the order. Paid/payout-confirmed remain recoverable if the local inventory commit fails. An order awaiting reconciliation must still accept valid late success evidence.

Record a verified bank outcome durably before attempting fulfillment, then perform ownership mutation plus final result in a second atomic transaction. A crash between them leaves a paid order to finish. If response persistence itself fails, original-key replay/status recovers it. Never perform a network call while a SQL transaction is open. Unique fulfillment state and transaction proof prevent duplicate results from granting or consuming items twice. Delayed pending/error replies cannot regress settled state.

### 3. Freeze quotes and preserve pending obligations

Keep offers in a separate `assets/shop/` CUE package (`package shop`), with `schema.cue` and one file per item, such as `bread.cue`. Each file declares its catalog ID as a flat key, like item definitions, and contains ownership mode and optional buy/sell decimal prices. The map key supplies catalog identity; no enclosing `shopOffers` field or duplicated catalog ID is needed. Keep item definitions and saved snapshot JSON/version unchanged.

Capture, evaluate, and watch `shop` alongside `items` and `locales`, then validate and publish one coherent content bundle. Missing `assets/shop/` means no offers for compatibility with existing asset directories; schema-only shop assets also define a valid empty catalog. Watch the asset root as well as existing package directories so adding, removing, or recreating `assets/shop/` triggers a rescan and subscription update. Invalid shop edits retain the entire previous valid bundle. Shop offers remain independently editable without changing the item schema.

Illustrative owner-authored `assets/shop/bread.cue` (prices are examples, not deployable defaults):

```cue
package shop

bread: {
    mode:      "stackable"
    buyPrice:  "10"
    sellPrice: "5"
}
```

Index offers by unique `(catalogID, mode)` and reject duplicate targets. Buy offers require the active definition and matching mode; sell-only offers accept saved snapshots by catalog ID/mode even when their text/rarity differs or the active item has been removed. Price is current offer policy at quote time, not an old snapshot property. Admin grants follow the same rules; no origin fields are introduced.

Quotes store screen selection, quantity, terms, and a consistent capture. On confirmation, validate ownership, availability, permissions where applicable, active offer, and quote terms; changed terms produce a new review. A confirmed order never changes price, delivery chat, or snapshot. Unlimited purchase stock avoids merchant stock allocation; inventory remains unlimited in gameplay capacity. Numeric stack overflow still requires a paid-awaiting-fulfillment state and an actionable diagnostic rather than losing payment evidence or charging again.

Invoice creation uses payer `telegram_id`, account 2, originating group `chatId`, and a bounded frozen comment identifying the order. The bank application must be able to post there. No silent fallback to a different chat is allowed because that changes financial parameters. Do not add local expiry to invoices: they may be paid indefinitely, and the frozen purchase snapshot must remain fulfillable after offer removal.

### 4. Reserve sale quantities without deleting ownership early

Add `inventory_reservations` keyed by order and exact entry, referencing its group/character and quantity. Sum reservations under the same SQLite transaction used to validate and insert a sale order. Individual entries reserve one; stacks reserve part or all. Owned quantities remain unchanged until verified payout; available quantity is ownership minus active reservations. Inspection displays owned/reserved/available when relevant; list formatting remains the existing six-row presentation of owned quantities.

On payout confirmation, transactionally decrement/delete the selected entry, release its reservation, and mark the order completed. Preserve item snapshots for historical orders. Add projection removal/reconciliation rather than leaving deleted ECS entries behind. On established nonpayment release the reservation without modifying ownership. A later generic error after an earlier unknown attempt does not authorize release: permissions may have been revoked after the payout succeeded. A first-attempt definitive rejection with no unresolved earlier send can fail safely, but message text must not invent a specific reason for -32601.

Do not use balance as a reservation or settlement oracle: external activity may change it. The bank transfer is authoritative for treasury availability. Do not correlate history by amount/time alone, as it contains no documented financial key. Conflicts or mismatched proofs park the order for reconciliation and surface an owner diagnostic with order/request IDs. Resolution uses original-key recovery or verified exact bank evidence; never automatically replace a financial key or offer a force-release control based on a timeout. Document operator recovery steps without introducing a new privileged money command.

### 5. Authenticated asynchronous transport and lifecycle

Add bank message dispatch before normal group routing. Require private chat, exact configured numeric sender/chat identity, bot sender, original unedited/unforwarded text, valid envelope, and a known persisted request. Bank messages never become player commands and must not be answered as JSON-RPC requests. Validate transaction amount, treasury account/direction, request ID, and counterparty fields available in the method response. The invoice request binds payer identity; its senderAccountId must be positive and consistent with any previously learned account binding, without inventing a public account lookup.

Use existing `sendMessage` with numeric destination and no formatting; no user session/MTProto dependency. Telegram documents private bot-to-bot messages when both bots enable Bot-to-Bot Communication Mode (`https://core.telegram.org/api/bots/bot-to-bot`). Deployment must verify this and the bank's numeric ID; do not trust username matching for replies. Existing bot client version should be exercised with fake API requests; do not upgrade it speculatively.

One bounded bank worker fetches persisted due work through registered owner operations, sends outside the owner, and submits send observations/replies back through typed operations using the order's stored scope. The private reply path must remain serviceable while a request is outstanding: never synchronously wait for a bank response inside the Telegram handler, its mutex, or the runtime. Persist replies/correlation before financial transitions; in-memory pending maps are only optional accelerators. Bound work buffers and rely on durable due rows when memory queues fill.

Use fresh transport IDs for each payment-status read to avoid replayed pending reads. Financial recovery preserves the original exact payload/key; a new transport ID may represent a retry only of that unchanged financial request. If reusing a transport ID, reuse its exact method and params. Exponential backoff with jitter, per-peer send limits, and request timeouts bound delivery attempts; unresolved obligations have no automatic terminal retry-count cutoff. Conflict and invalid proof require operator attention, while valid late responses can still settle. Diagnostic balance/history calls are optional and never authorize item changes.

Config adds optional bank numeric ID and treasury account (default 2). Without bank ID, shop settlement is disabled with a localized unavailable state while existing gameplay and menu updates work. Validate malformed supplied settings at startup, freeze selected bank/account into orders, and refuse mismatched configuration for outstanding orders rather than replaying old intent to another destination. Online bank readiness failure does not prevent unrelated gameplay startup.

Shutdown stops new intake/financial claims, cancels network waits, drains accepted local transitions within the existing shutdown deadline, stops menu delivery, and joins both workers before closing transport/storage. Persisted sends remain unknown if interrupted. Each worker has application-owned context, done channel, and cancellation; no goroutine owns or accesses ECS/session pointers.

### 6. Menu subscriptions and commit invalidation

Extend sessions with screen kind, selected entry/order, quantity/quote selection, monotonic render revision, dependency keys, last delivered representation, and dirty state. Maintain indexes by group, character/inventory, order, permissions, and content/locale generation. Dependency indexes and session navigation remain owned solely by the runtime. Use explicit typed change notifications from committed feature operations and successful asset publication, not a global event bus.

Grants, purchase fulfillment, sale reservation/release/consumption, rename, language/admin changes, and valid content publication mark dependent sessions dirty. Background notifications do not call the user-activity path. Re-render from current durable state under the owner, preserve list page/inspection/order when valid, clamp empty pages, and return from missing entry inspection to its list. Quote updates invalidate old financial confirmations. New message binding re-renders from current state to close the initial send/bind race. Sweeping/eviction removes subscriptions and queued work.

Automatic rendering stages text and keyboard together. Preserve action refs when semantic screen/actions have not changed, so no-op detection is meaningful. When changed, advance revision and reject old refs immediately; if delivery fails, a click on the still-visible old keyboard cannot be reinterpreted. Record desired versus delivered revision independently. Rendering for a subscription uses its opener identity, never the actor whose action triggered invalidation; shared settings sessions keep their existing shared access policy and check current permissions on each action.

### 7. One ordered edit path for callbacks and automatic updates

Route all edits of a bound menu through a single delivery coordinator in `internal/ui/menus` using the Telegram adapter as a transport port. The router must no longer independently edit bound menu messages. A bounded application-owned delivery worker operates on immutable `(session, group, message, revision, view)` jobs and returns outcomes to the owner. Retain only the latest desired job per message, allow one in-flight edit per message, and send the newest revision after any older in-flight edit completes. An obsolete completion cannot mark a newer revision delivered. This prevents a slow automatic edit from overwriting later user navigation.

Coalesce bursts over a short bounded interval (proposed 100ms) and prioritize callback-driven renders; dispatch normally within one second when delivery is healthy and not rate limited. Compare actual text/styles/keyboard before sending. Retry temporary failures and Telegram retry-after outside the world owner, regenerating latest state when needed. Treat a confirmed not-modified response as success; classify permanent inaccessible/deleted-message failures by adapter errors, deactivate the subscription, and log bounded diagnostics. Keep callback acknowledgement separate and prompt.

Do not hold the router's global mutex across slow edit retries. Serialize screen transitions on the runtime, and message edits on the coordinator. Registration maps can remain immutable after startup. Only the delivery worker touches its queues; only the owner mutates sessions. Expiry cancels queued work and checks activity immediately before sends; an already submitted network edit cannot be recalled. No replacement message is posted automatically on edit failure. Restart discards subscriptions and delivery queues, while orders resume from storage.

### 8. Project structure and responsibilities

Planned new paths (`*_test.go` adjacent to implementation files):

```text
internal/modules/shop/
  offer.go, money.go, order.go          # Pure offer/quote/order rules
internal/application/shop/
  service.go, recovery.go, ports.go     # Atomic shop use cases and bank worker
internal/adapters/bank/
  protocol.go, client.go                # Contract encoding, parsing, proof checks
internal/adapters/sqlite/
  shop.go, reservations.go              # Order/request UoW and reservation queries
  migrations/002_shop.sql               # Additive durable schema (next free version)
internal/app/
  shop.go, menu_updates.go              # Routes, commit invalidation, worker wiring
internal/ui/menus/
  updates.go, delivery.go               # Subscription/revision model and delivery
internal/ui/views/
  shop.go                              # Buy, Sell, Orders, review and result screens
```

New asset paths: `assets/shop/schema.cue` and owner-configured `assets/shop/<item_id>.cue`, all in `package shop`; keep one item offer per file and use flat catalog-ID keys as in `assets/items/`. Changed paths: `internal/content/bundle.go` and `internal/adapters/cueassets/{load,watch}.go` for separate offer evaluation and coherent three-package publication, and `assets/locales/{schema,en,ru}.cue` for mirrored flat key groups. Keep item assets, `package items`/`package locales`, and saved snapshot JSON unchanged.

Also change `internal/modules/inventory/{service,ecs}.go`, SQLite inventory access, `internal/ui/menus/sessions.go`, inventory views, `internal/app/{app,modules,inventory,items,characters}.go`, `internal/adapters/telegram/{bot,router}.go`, and nearby integration tests. `internal/core/response.go` can carry an immutable menu update descriptor if necessary; it must not expose ECS or adapter types. Add configuration in `internal/config/`, placeholders in `.env.example`, and operator instructions in `README.md`. `cmd/replipg/main.go` stays thin; workers are composed in app, never in main. Adapt `internal/runtime/` only for typed internal completion operations/lifecycle needs, preserving one owner.

Dependency direction: UI/adapters depend on application/domain ports; shop orchestration depends on inventory/characters/shop rules and transaction interfaces, never a concrete Telegram or SQLite client. Composition injects concrete adapters. Existing grant use cases keep their own transaction behavior but emit inventory invalidation after commit. No concurrency inside domain modules.

## Risks / Trade-offs

- [Game admins can drain treasury using grants] -> Accepted user requirement; keep explicit offer eligibility and prices, and let the bank enforce actual funds. No hidden provenance restriction.
- [No cancellation or expiry] -> Retain pending purchase obligations indefinitely and never promise local cancellation. Order lists are paginated; no lifetime cap silently discards debt.
- [No cross-system atomic transaction] -> Durable intent, original-key recovery, and exactly-once local settlement; unresolved sales remain reserved rather than risking item duplication.
- [Generic bank errors after unknown attempts] -> Expose reconciliation required and original request IDs; never guess a payout failed or manufacture a replacement key.
- [Paid order cannot fit a numeric stack or local persistence fails] -> Preserve payment proof and retry local fulfillment; alert owner without charging again or silently refunding. This is not a gameplay inventory limit.
- [Menu edit failures or Telegram limits] -> Coalesce and retry latest state; preserve authoritative game state and permit reopening. Automatic updates are eventually consistent, not instantaneous guarantees.
- [New locale keys invalidate custom asset sets] -> Update shipped English/Russian/schema together and document migration of custom assets before deployment.
- [Single delivery worker head-of-line delay] -> Never sleep inside the world owner; scheduler tracks per-message retry deadlines and can service other ready messages. A bounded worker is adequate initially; add more only with measured need while preserving per-message order.

## Migration Plan

1. Back up SQLite and retain existing user changes. Introduce additive orders, requests/attempts, and reservations tables with ownership constraints, state indexes, confirmation uniqueness, and transaction-proof uniqueness. Use the next migration filename available at implementation time. Existing grant records and snapshots are untouched.
2. Deploy `assets/shop/schema.cue` with an initially empty offer set; update shipped locales atomically. Custom asset roots without `assets/shop/` continue to work with no offers. Document one-file-per-item offer configuration without committing arbitrary live prices.
3. Deploy generic menu updates independently of bank availability. Old sessions naturally expire on restart; users reopen them.
4. Configure verified bank ID, account 2, bot-to-bot mode, and group access. Use fake bank/Telegram adapters for automated acceptance; do not spend real funds as a test. Any later live monetary verification requires a separately authorized concrete transaction.
5. On startup hydrate ownership/reservations and resume outstanding requests before accepting new financial claims. Inventory browsing must still work while bank recovery is offline.
6. Rollback must stop new orders and reconcile outstanding obligations first. Do not restore an old pre-transfer database or run an older binary that ignores active reservations/orders while serving sales. The migration version guard already rejects an unsupported newer schema; keep that protection. Retain the upgraded database and recovery-capable binary until obligations resolve.

## Open Questions

- Owner-selected initial item offers and prices; an empty catalog is valid, so these do not block implementation.
- Deployment values for the bank numeric Telegram ID and participating group permissions; expose documented placeholders rather than guessing identities or making bank calls.
