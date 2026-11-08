# inventories Specification

## Purpose

Let players browse and inspect their persistent group inventories while preserving item versions and restricting menu controls to their opener.

## Requirements

### Requirement: Inventory ownership is persistent and unlimited
Each character SHALL own an independent durable inventory without a gameplay capacity limit. Every entry MUST belong to one character in one group and retain its grant-time snapshot. Quantities and instance identities MUST survive restart.

#### Scenario: Inventory grows beyond one page
- **WHEN** a grant adds entries to an inventory already containing many entries
- **THEN** the grant is not rejected because of an inventory slot or weight capacity
- **AND** all committed entries remain accessible after restart

### Requirement: Stacks require identical item data
Stackable items SHALL merge only within the same character inventory when their catalog IDs and complete saved snapshots match. A differing snapshot MUST create a separate stack. Matching MUST depend on saved data rather than merely a reload generation number.

#### Scenario: Identical grant
- **WHEN** the same character receives more of an item with an identical catalog ID and snapshot
- **THEN** the matching stack quantity increases without changing its snapshot

#### Scenario: Changed definition or translation
- **WHEN** a later grant has a different saved rarity, ownership mode, or English or Russian item text
- **THEN** it does not overwrite or merge into an existing stack with different data

#### Scenario: Unchanged data after reload
- **WHEN** a reload or restored definition yields exactly the same catalog ID and snapshot data as an existing stack
- **THEN** a subsequent stackable grant merges into that matching stack

### Requirement: Individual items have independent identities
Each granted individual item SHALL have a distinct durable instance identity and its own saved snapshot. Granting multiple individual items MUST create that many separate instances rather than a quantity stack.

#### Scenario: Multiple individual items
- **WHEN** a player receives three items with individual ownership mode
- **THEN** the inventory contains three separately identifiable instances with preserved identities after restart

### Requirement: Players can browse their inventory
The `/inventory` command SHALL show the invoking user's current-group inventory using localized text, item names, and stack quantities. The inventory list MUST NOT show rarity. Empty inventories MUST have a clear empty state. Pagination MUST make every entry reachable while respecting Telegram message and button limits.

#### Scenario: Empty inventory
- **WHEN** a user with no items invokes `/inventory`
- **THEN** the bot shows a localized empty inventory message

#### Scenario: Browse multiple pages
- **WHEN** the inventory contains more entries than fit on one page
- **THEN** the user can navigate through bounded pages and reach each entry for inspection

### Requirement: Inventory entries follow acquisition order
Inventory entries SHALL appear in the order they were first received, oldest first, across all pages and after restart. Adding quantity to an existing stack MUST preserve its position. Multiple instances received together SHALL retain their creation order.

#### Scenario: Grants with unordered identities
- **WHEN** items are received in an order different from their identity sort order
- **THEN** inventory pages list them in acquisition order after reopening the database
- **AND** increasing an earlier stack does not move it

### Requirement: Inventory title identifies its owner
The inventory title SHALL use the localized heading followed by a literal bullet separator and the saved character name: bold heading, ` • `, inline-code name. The English form MUST be `Inventory` in bold followed by ` • ` and the name in inline code.

#### Scenario: Named inventory owner
- **WHEN** the saved character name is alexthvest and the group uses English
- **THEN** the title displays bold Inventory followed by ` • ` and inline-code alexthvest

### Requirement: Inventory pages use six compact item rows
Each full inventory page SHALL contain exactly six entries on consecutive lines without blank lines between entries, separated from the title by one blank line. The last page SHALL show only the remaining entries. Entry numbers and item names SHALL use normal text. Only stackable entries SHALL display their quantity in parentheses using inline code. Individual entries SHALL omit the quantity. Page-number values SHALL use inline code. List rows MUST omit rarity and item descriptions.

#### Scenario: Full and partial pages
- **WHEN** the inventory has thirteen entries
- **THEN** its three pages contain six, six, and one entry respectively with no blank lines between item rows

#### Scenario: Item row formatting
- **WHEN** an inventory contains an individual stick and a stack of 100 coins
- **THEN** their rows display page-local numbers and saved item names as normal text, with no quantity for the stick and inline-code `(100)` for the coins
- **AND** a blank line separates the title from the first entry

### Requirement: Inspection buttons use one numbered row
Each inventory page SHALL have one row of inspection buttons labeled only with page-local numbers matching its visible item rows, starting at 1. A full page MUST have six inspection buttons in that row. A partial page MUST offer buttons only for its visible entries. Pagination controls SHALL use a separate row.

#### Scenario: Full-page inspection keyboard
- **WHEN** a page contains six entries
- **THEN** its inspection row has exactly the captions 1, 2, 3, 4, 5, and 6 without item names
- **AND** selecting a number inspects the corresponding visible entry

#### Scenario: Partial-page inspection keyboard
- **WHEN** the final page contains one entry
- **THEN** its inspection row contains only button 1 and no placeholder buttons

### Requirement: Only the opener operates inventory menus
Inventory menus SHALL be visible in the group but controllable only by the opener. The system MUST validate the acting user, originating group and message, active session, and entry ownership for every callback. Invalid callbacks MUST NOT expose other inventory data or mutate state.

#### Scenario: Another player presses a button
- **WHEN** another player presses an inventory menu's navigation or inspection button
- **THEN** the callback is acknowledged and rejected without editing the opener's menu or showing the other player's inventory

#### Scenario: Forged entry reference
- **WHEN** the opener submits an inspection callback for another player's entry or a foreign group's entry
- **THEN** the system rejects it without exposing the referenced snapshot

#### Scenario: Expired or restarted session
- **WHEN** a callback uses an expired menu session or one invalidated by restart
- **THEN** it cannot perform navigation or inspection and the user can reopen `/inventory`

### Requirement: Inspection uses saved item data
An inventory inspection SHALL show the selected entry's saved name and description in the current group language, its saved rarity and mode, and its quantity or instance identity. Interface labels and rarity labels SHALL use current translations. Inspection MUST offer a return to inventory navigation.

#### Scenario: Inspect removed definition
- **WHEN** an opener inspects an item whose asset definition has been edited or deleted
- **THEN** the bot renders the saved item data without requiring the original definition

#### Scenario: Group language changes during browsing
- **WHEN** the group language changes before the opener next interacts with the inventory menu
- **THEN** the next rendered view uses the new group language and the corresponding saved item text

### Requirement: Inventory menus only view and inspect
Inventory navigation and inspection SHALL leave ownership, snapshots, and quantities unchanged. The MVP MUST NOT expose equip, use, discard, delete, or transfer actions.

#### Scenario: Repeated inspection
- **WHEN** a player repeatedly navigates and inspects an item
- **THEN** ownership and quantities remain unchanged and no item mutation controls are offered

### Requirement: Sale reservations preserve ownership until settlement
Inventory SHALL distinguish owned, reserved, and available quantities. Reservations MUST be durable, group and character scoped, and tied to one sale order; total reserved quantity MUST NOT exceed ownership. A reserved individual instance MUST be unavailable to another sale. Reserving items MUST NOT modify their snapshots, identities, or owned quantities.

#### Scenario: Reserve part of a stack
- **WHEN** a player reserves 3 units of an owned stack of 10
- **THEN** ownership remains 10, reserved quantity is 3, and available quantity is 7
- **AND** inventory inspection and sale selection expose the reservation without showing it as completed removal

#### Scenario: Restart with an unresolved payout
- **WHEN** the process restarts while a sale payout is unresolved
- **THEN** its reserved quantities remain unavailable and retain their original item snapshots and identities

### Requirement: Sale settlement changes ownership atomically
Confirmed sale payout SHALL reduce the exact reserved stack quantity or remove the reserved individual instance, release its reservation, and complete the order in one transaction. Established nonpayment MUST release the reservation without changing ownership. Duplicate settlements MUST have no further effect. Inventory views and runtime ownership MUST reflect removal as well as addition.

#### Scenario: Complete partial stack sale
- **WHEN** a verified payout settles the reserved 3 units of a stack of 10
- **THEN** owned quantity becomes 7 and the reservation disappears together with the completed order result

#### Scenario: Settlement commit fails
- **WHEN** persistence fails during ownership removal and order completion
- **THEN** neither partial removal nor completed status commits and the recorded payout remains recoverable

#### Scenario: Entire entry sold
- **WHEN** the last units or an individual instance are sold successfully
- **THEN** the entry disappears from persistent ownership and runtime views while its snapshot remains available to historical orders
