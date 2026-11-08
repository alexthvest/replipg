# item-definitions Specification

## Purpose

Maintain a shared reloadable item catalog while preserving complete and immutable grant-time definitions for items already owned by players.

## Requirements

### Requirement: Only the owner can browse the active catalog
The `/items` command SHALL allow only the configured bot owner to browse all active item IDs and localized names in catalog ID order, six entries per page with previous/next buttons. Navigation SHALL require the owner and the originating group, message, and active session. Browsing MUST NOT create characters or change ownership.

#### Scenario: Owner browses all definitions
- **WHEN** the owner invokes `/items` and navigates its pages
- **THEN** every active item is reachable with its ID and localized name
- **AND** no character or inventory entry is created
- **AND** the resulting group message remains visible to group participants

#### Scenario: Catalog row styling
- **WHEN** an owner opens a catalog page
- **THEN** it displays up to six consecutive rows with page-local numbers and names in plain text, followed by parenthesized IDs in inline code
- **AND** there are no blank lines between item rows, matching inventory spacing

### Requirement: Catalog buttons grant one item to the owner
Each visible catalog row SHALL have a matching numbered button in one row. Only the owner SHALL be able to press it to grant one item to their current-group inventory. The grant SHALL use current active item data, commit atomically with any missing owner character, and return localized callback feedback. Replayed updates MUST NOT duplicate grants, while distinct presses SHALL grant separately.

#### Scenario: Owner presses a numbered button
- **WHEN** the owner presses an active page's numbered item button
- **THEN** exactly one corresponding item is granted to the owner in that group and success is acknowledged after commit
- **AND** the catalog message and its buttons remain available for further presses

#### Scenario: Repeated update versus distinct presses
- **WHEN** a button's Telegram update is delivered twice and the owner then presses it again with a distinct update ID
- **THEN** only two items are granted, following their stackable or individual ownership mode

#### Scenario: Invalid or unauthorized grant button
- **WHEN** a non-owner, foreign group/message, expired session, or button from a previous page attempts a grant
- **THEN** no character or ownership changes occur

#### Scenario: Definition removed or grant fails
- **WHEN** a fresh button press references a removed definition or its transaction fails
- **THEN** localized failure feedback is acknowledged and no partial grant or character is saved

#### Scenario: Other users attempt access
- **WHEN** a regular user or game admin invokes `/items` or presses its buttons
- **THEN** the action is denied without rendering another catalog page

#### Scenario: Catalog or language changes
- **WHEN** accepted asset changes or a group language change precede navigation
- **THEN** the next render uses the current catalog and group language, clamping the page if necessary

#### Scenario: Empty catalog
- **WHEN** the owner invokes `/items` with no active definitions
- **THEN** a localized empty state is displayed without navigation buttons

#### Scenario: Unexpected command arguments
- **WHEN** the owner invokes `/items` with arguments
- **THEN** localized usage text is displayed instead of a catalog menu

### Requirement: Groups share one item catalog
The system SHALL expose one shared project-file catalog for all groups. Every active definition MUST have a stable catalog ID, localized name and description references, one supported rarity, and an explicit stackable or individual ownership mode.

#### Scenario: Definition used in two groups
- **WHEN** admins in different groups grant the same active catalog item
- **THEN** both grants use the shared definition while recording ownership independently in each group

#### Scenario: Incomplete definition
- **WHEN** a candidate definition omits an ID, required text reference, rarity, or ownership mode
- **THEN** asset validation rejects the candidate

### Requirement: Item rarity uses a fixed vocabulary
The catalog SHALL support exactly common, uncommon, rare, epic, and legendary rarity identities. The interface SHALL display their localized labels in the current group's language.

#### Scenario: Supported rarity
- **WHEN** an epic item is inspected in a Russian-language group
- **THEN** its rarity is shown using the Russian label for epic

#### Scenario: Unknown rarity
- **WHEN** a candidate item declares an unsupported rarity
- **THEN** validation rejects the candidate asset set

### Requirement: Item files reload automatically
The system SHALL watch item asset files and automatically validate the complete candidate item and locale set before atomically publishing it. Invalid edits MUST retain the last valid assets and log diagnostics. Reload MUST require neither a restart nor a command.

#### Scenario: Valid definition change
- **WHEN** an item's name reference, description reference, or rarity changes in a valid candidate set
- **THEN** subsequent grants use the new definition and existing owned items remain unchanged

#### Scenario: Invalid item edit
- **WHEN** a candidate has invalid syntax, conflicting IDs, unsupported values, or missing translations
- **THEN** no part of the candidate item or locale set becomes active

### Requirement: Active ownership mode remains stable
The system SHALL reject changes to an item's ownership mode while its catalog ID remains continuously active. Removing the definition in a successful reload and later restoring it MUST NOT convert existing ownership.

#### Scenario: Change stackable to individual in place
- **WHEN** a candidate changes a continuously active catalog ID from stackable to individual
- **THEN** validation rejects the candidate and retains the previous asset set

#### Scenario: Restore removed ID with another mode
- **WHEN** a removed catalog ID is later restored with a different ownership mode
- **THEN** future grants follow the restored definition while existing entries preserve their saved modes

### Requirement: Grants capture complete immutable snapshots
Each grant SHALL capture one consistent active definition containing its catalog ID, ownership mode, rarity, and resolved name and description text in both supported languages. The snapshot MUST be durable and immutable once granted.

#### Scenario: Assets reload during a grant
- **WHEN** asset publication overlaps a grant
- **THEN** the grant captures all definition fields and translated item text from a single validated asset version

#### Scenario: Translation edit after a grant
- **WHEN** an item's translation changes after an item is granted
- **THEN** the owned item retains its saved translated text and future grants use the new translation

#### Scenario: Language switch after a grant
- **WHEN** a group changes from English to Russian
- **THEN** owned item names and descriptions use the Russian text saved in their snapshots
- **AND** interface text and rarity labels use current live translations

### Requirement: Definition deletion never deletes ownership
Removing an item definition SHALL prevent new grants of that ID while preserving all existing ownership and snapshots. Restoring the ID SHALL enable new grants without reconnecting or updating existing snapshots.

#### Scenario: Deleted definition and restart
- **WHEN** an owned item's definition is removed and the bot restarts
- **THEN** the item remains viewable with its saved text, rarity, mode, quantity, and durable identity
- **AND** a new grant of that absent catalog ID is rejected

#### Scenario: Restored definition
- **WHEN** a removed definition returns with changed text or rarity
- **THEN** new grants use the restored data and existing items retain their original data
