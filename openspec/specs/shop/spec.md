# shop Specification

## Purpose

Let players buy catalog items and sell their group inventory items using the external bank, with confirmed prices and recoverable order outcomes.

## Requirements

### Requirement: Shop menus belong to the player and group
The `/shop` command SHALL open one localized item catalog in the current group, with Buy and Sell actions available after selecting an item and a separate My Orders view. Navigation, selection, and order actions MUST validate the opener, group, message, active session, and current ownership. Orders MUST be visible only through their owner's current-group menu.

#### Scenario: Another player operates a shop menu
- **WHEN** another player presses a shop button
- **THEN** the callback is rejected without revealing orders, changing the menu, or creating a financial request

#### Scenario: Player has characters in several groups
- **WHEN** a player opens Orders in one group
- **THEN** only that player's orders from that group are available

#### Scenario: Browse a unified shop catalog with inventory-style pagination
- **WHEN** a player opens the shop
- **THEN** the bold Shop title precedes a blank line and up to six numbered item selectors in one catalog without global Buy or Sell mode buttons
- **AND** a blank line separates the list from the localized `Page <current>/<total>` footer, like inventory
- **AND** numbered item buttons share one row, with up to six buttons, and page navigation occupies a separate row
- **AND** My Orders remains available separately from item selection

#### Scenario: Choose an action after selecting an item
- **WHEN** a player selects an item in the catalog
- **THEN** its saved description, applicable buy and sell prices, and relevant owned, reserved, and available quantities are displayed
- **AND** Buy and eligible Sell actions appear together using default button styling, with Back and My Orders together below
- **AND** selecting a direction opens its explicit quantity and price confirmation without sending a financial request
- **AND** older owned snapshots remain independently sellable when accepted by an offer, without allowing purchases of a retired snapshot

#### Scenario: Review a purchase or sale using the reference layout
- **WHEN** a player selects an item
- **THEN** the review shows the bold saved item name, code-styled direction, italic description, stackable quantity and exact unit price, and bold total with the currency symbol
- **AND** stackable items expose plain `1`, `−`, `+`, and localized Max quantity controls in one row, while Back and My Orders share a row
- **AND** Max chooses the largest supported quantity within the 1000-unit, exact-entry ownership, and bank amount limits; individual sales remain limited to one exact instance per order
- **AND** sale reviews expose owned, reserved, and available quantities without redundant purchase or sale notice paragraphs
- **AND** the stackable quantity range of 1 through 1000, saved snapshots, sale eligibility, and financial confirmation rules remain unchanged

#### Scenario: Review an individual item
- **WHEN** a player buys or sells a non-stackable item
- **THEN** the review omits the quantity field and quantity controls and confirms exactly one item
- **AND** existing frozen orders retain their original quantities and fulfillment obligations

#### Scenario: Browse and inspect personal orders using the reference layout
- **WHEN** a player opens My Orders
- **THEN** a code-styled My Orders context follows the bold Shop title, and up to six numbered orders show saved item names, code-styled quantities, and italic status lines
- **AND** inspecting an order shows its direction, durable identity, frozen quantity and prices, bold total, and localized status with Refresh and Back controls together
- **AND** Refresh reads committed state without issuing a replacement financial request

#### Scenario: Long localized shop content
- **WHEN** a shop menu contains long item text, status, or identifiers
- **THEN** text wraps to the reference 38-character line width while preserving literal content, Unicode text styles, callback identities, and Telegram's message limit

### Requirement: Offers define explicit buy and sell prices
The shop SHALL use validated fixed-price offers keyed by catalog ID and ownership mode. Buy and sell directions SHALL be independently optional. Prices MUST be positive integer amounts of at most 100 decimal digits; sell price MUST be below buy price when both exist. Purchase offers MUST reference an active definition of matching mode. An empty offer set MUST be valid.

#### Scenario: Unsupported sale
- **WHEN** a player owns an item with no matching sell offer
- **THEN** the shop does not offer to buy that entry

#### Scenario: Invalid candidate offers
- **WHEN** an asset candidate contains an invalid price or a purchase offer with no matching active definition
- **THEN** the complete candidate is rejected and previous offers remain active

### Requirement: Shop offers use separate per-item asset files
Shop prices SHALL be defined in `assets/shop/` using `package shop`, a shared `schema.cue`, and one `<item_id>.cue` file per configured item with its catalog ID as a flat key. Offers MUST be validated and published together with items and locales. A missing or schema-only shop package MUST mean no offers. Valid edits and directory creation or removal MUST reload automatically.

#### Scenario: Edit one item's prices
- **WHEN** the owner edits `assets/shop/bread.cue` and the complete asset candidate is valid
- **THEN** new bread quotes and affected active shop menus use the updated prices without changing item definitions or confirmed order prices

#### Scenario: Add shop assets to an existing custom asset root
- **WHEN** a valid `assets/shop/` package is added to a running asset root that previously had none
- **THEN** its offers become available automatically after complete candidate validation

#### Scenario: Invalid per-item offer edit
- **WHEN** a shop file edit makes the complete asset candidate invalid
- **THEN** the previously published items, offers, and translations remain active

### Requirement: Players confirm immutable order terms
Before creating a financial request, the shop SHALL show direction, selected item, quantity, unit price, and total and require explicit confirmation. Quantities MUST be integers from 1 to 1000, and totals MUST fit the bank amount limit. Confirmation MUST freeze those terms, the item snapshot, the player identity, and invoice delivery parameters. A stale quote MUST require a new review if its terms change.

#### Scenario: Price changes before confirmation
- **WHEN** prices change after a quote is shown and before the player confirms it
- **THEN** the shop requires confirmation of the updated quote before requesting money

#### Scenario: Repeated confirmation
- **WHEN** the same confirmation button is pressed repeatedly in distinct updates or a delivery is replayed
- **THEN** it resolves to the same order without another invoice, payout, reservation, or grant

### Requirement: Purchases grant only after verified payment
A confirmed purchase SHALL request payment to treasury account 2 from the player's Telegram identity. Purchase stock SHALL be unlimited. Items MUST be granted from the frozen snapshot exactly once after verified payment, with fulfillment and the durable result committed atomically. Request acceptance, operation completion, and pending status MUST NOT grant items.

#### Scenario: Immediate payment authorization
- **WHEN** invoice creation returns a verified paid result because the player previously authorized automatic payment
- **THEN** the order fulfills once without waiting for another bank approval

#### Scenario: Rejected or financially failed purchase
- **WHEN** the bank reports rejected or failed with insufficient funds
- **THEN** the order reports that outcome and grants no items

#### Scenario: Catalog removal during a purchase
- **WHEN** an outstanding invoice is paid after its item or offer is removed
- **THEN** the order grants its frozen item snapshot for the agreed quantity and price

### Requirement: Sales reserve items before treasury payout
A confirmed sale SHALL reserve only the player's available owned quantity before requesting a transfer from account 2 to that player's Telegram identity. Admin-granted and purchased items MUST be equally eligible under matching offers. Confirmed payout MUST consume the reservation and complete the order atomically; established nonpayment MUST release it. An unknown outcome MUST retain the reservation for reconciliation.

#### Scenario: Sell admin-granted items
- **WHEN** a player confirms a sale of admin-granted items accepted by a sell offer
- **THEN** the same reservation and payout rules apply as for purchased items

#### Scenario: Concurrent sales of one instance
- **WHEN** two orders attempt to sell the same individual item
- **THEN** at most one reserves that instance and requests its payout

#### Scenario: Treasury cannot pay
- **WHEN** a first transfer attempt returns a definitive rejection with no prior unresolved attempt
- **THEN** the sale fails without consuming its items and releases its reservation
- **AND** the message does not claim insufficient treasury funds solely from the bank's generic unavailable error

### Requirement: Players can recover outstanding orders
Orders SHALL expose pending, paid awaiting fulfillment, completed, rejected, failed, or reconciliation-required outcomes as applicable. Restart, menu expiry, and message delivery failures MUST NOT discard orders or change frozen terms. Pending invoices MUST NOT be declared canceled or expired locally without bank evidence that they cannot pay.

#### Scenario: Paid purchase cannot commit items yet
- **WHEN** verified payment is received but local fulfillment persistence fails
- **THEN** the order remains recoverable for fulfillment without requesting another payment

#### Scenario: Menu expires before settlement
- **WHEN** an order settles after its menu expires or the process restarts
- **THEN** it still completes once and the player can reopen Orders and inventory to see its outcome
