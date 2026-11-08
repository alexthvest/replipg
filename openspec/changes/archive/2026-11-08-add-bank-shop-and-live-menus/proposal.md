# Proposal

## Why

Players need to spend bank money on game items and sell inventory items back to the game. Bank settlement is asynchronous, so open inventories and other affected menus must reflect committed changes without requiring another command or button press.

## What Changes

- Add a group shop with Buy, Sell, and Orders screens, quantity selection, explicit total confirmation, and persistent order status.
- Integrate RaylabBot (replipg) with the independently operated bank application described in `replibank-integration.md`, using account 2 for receipts and payouts.
- Persist financial intent before sending messages, reconcile missing replies with unchanged financial keys, grant paid purchases once, and reserve sale items until payout is resolved.
- Allow sales of admin-granted items whenever a configured shop offer accepts them. No acquisition-origin restriction or provenance tracking is required.
- Refresh affected active menus after committed mutations and valid asset publication; preserve navigation, reject superseded actions, and deliver ordered edits outside the game owner.
- Retain group-only gameplay while allowing authenticated private bank replies to settle existing group orders.

### Confirmed requirements

- The external bank is not controlled by the game owner; use its existing public contract only.
- Initial economic actions are item purchases and sales, including admin-granted items.
- Completed transactions automatically update affected open menus, including an inventory opened while a purchase was pending.
- Shop offers and prices live in `assets/shop/`, with one CUE file per item and a shared schema, following the item asset layout.

### Proposed defaults and assumptions

- One shared treasury (account 2) and bank user identity across groups; characters, items, and orders remain group scoped. No local player wallet is introduced.
- Fixed CUE-configured offers, unlimited purchase stock, optional buy/sell directions, and positive integer currency amounts. When both directions exist, sell price must be below buy price.
- Match sale offers by catalog ID and saved ownership mode, including old snapshots; absent offers mean an item cannot be sold. Opening the shop creates a character like opening inventory.
- Invoice prompts are delivered to the originating group. The bank must be able to post there; no automatic private-delivery fallback changes an existing invoice payload.
- Menus remain ephemeral, bounded, and subject to the current inactivity TTL; background edits do not extend it. Durable orders recover after restart, but menus must be reopened.
- Initial deployable offer prices are intentionally left to the owner; a valid empty offer set is supported and avoids inventing economy values.

### Non-goals

Money issuance, quests or other earning systems, player trading, auctions, finite merchant stock, local bank emulation, automated refunds, invoice cancellation, changes to the bank bot, private gameplay, and persistent menu sessions.

### External prerequisites and unresolved inputs

Deployment needs the bank's verified numeric Telegram identity, working private bot-to-bot communication, existing account-2 permissions, and bank access to participating groups. These are configuration/prerequisites, not requests to modify the external service. Unknown outcomes must remain recoverable rather than being guessed from balance/history or generic errors.

## Capabilities

### New Capabilities

- `shop`: Group item offers, confirmed purchases and sales, treasury-funded payouts, and durable player orders.
- `bank-integration`: Authenticated JSON-RPC transport, idempotent financial requests, settlement validation, and restart reconciliation.
- `menu-updates`: Dependency-based refresh of active menus with navigation preservation, ordered delivery, and failure isolation.

### Modified Capabilities

- `inventories`: Reserve sale quantities, consume them on confirmed payout, and expose owned versus available amounts without changing saved item data.
- `characters`: Include valid shop opening in automatic character creation.
- `telegram-runtime`: Permit trusted private integration messages and persist financial recovery state while retaining group gameplay boundaries.
- `localization`: Refresh active menus on successful locale publication rather than waiting for their next interaction.

## Impact

Add shop orchestration under `internal/application/`, bank protocol adaptation under `internal/adapters/`, and shop rules under `internal/modules/`. Extend SQLite migrations, inventory projections, menu sessions/rendering/delivery, composition, configuration, CUE loading/watching, and both locales. Preserve current item snapshot and grant compatibility; existing items, including admin grants, remain eligible under configured offers. Use the existing Telegram client and Go standard library where possible; no new runtime dependency is planned. Do not contact the bank or make live financial calls during automated validation.
