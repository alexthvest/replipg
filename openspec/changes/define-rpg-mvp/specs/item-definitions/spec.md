# Item Definitions

## Purpose

Maintain a shared reloadable item catalog while preserving complete and immutable grant-time definitions for items already owned by players.

## ADDED Requirements

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
