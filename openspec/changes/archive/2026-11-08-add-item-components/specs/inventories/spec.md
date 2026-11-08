## MODIFIED Requirements

### Requirement: Stacks require identical item data
Stackable items SHALL merge only within the same character inventory when their catalog IDs and complete saved snapshots match, including components. A differing snapshot MUST create a separate stack. Component declaration order MUST NOT affect matching. Matching MUST depend on saved data rather than merely a reload generation number.

#### Scenario: Identical grant
- **WHEN** the same character receives more of an item with an identical catalog ID and snapshot
- **THEN** the matching stack quantity increases without changing its snapshot

#### Scenario: Changed definition or translation
- **WHEN** a later grant has a different saved rarity, ownership mode, or English or Russian item text
- **THEN** it does not overwrite or merge into an existing stack with different data

#### Scenario: Unchanged data after reload
- **WHEN** a reload or restored definition yields exactly the same catalog ID and snapshot data as an existing stack
- **THEN** a subsequent stackable grant merges into that matching stack

#### Scenario: Changed components
- **WHEN** a later grant differs only by adding or removing a component
- **THEN** it creates a separate stack and preserves the existing stack's classification

#### Scenario: Reordered components
- **WHEN** a definition changes only the declaration order of its components
- **THEN** an otherwise identical grant merges into the existing matching stack

#### Scenario: Legacy stack receives an unclassified grant
- **WHEN** an otherwise identical unclassified item is granted to a character with a matching pre-component stack
- **THEN** the grant merges into that stack and preserves its durable identity and acquisition position

### Requirement: Inspection uses saved item data
An inventory inspection SHALL show the selected entry's saved name and description in the current group language, its saved rarity, mode, and components, and its quantity or instance identity. Interface labels, rarity labels, and component labels SHALL use current translations. Inspection MUST offer a return to inventory navigation.

#### Scenario: Inspect removed definition
- **WHEN** an opener inspects an item whose asset definition has been edited or deleted
- **THEN** the bot renders the saved item data without requiring the original definition

#### Scenario: Group language changes during browsing
- **WHEN** the group language changes before the opener next interacts with the inventory menu
- **THEN** the next rendered view uses the new group language and the corresponding saved item text

#### Scenario: Display multiple saved components
- **WHEN** an item with saved consumable and material components is inspected
- **THEN** inspection displays both localized labels in a stable order independent of asset declaration order
- **AND** it uses saved component membership even if the active definition has changed

#### Scenario: Empty classification
- **WHEN** an unclassified or pre-component item is inspected
- **THEN** inspection displays a localized empty classification value
- **AND** it does not infer classification from the item's name or live definition

## ADDED Requirements

### Requirement: Marker components do not enable item actions
Marker components SHALL describe items without adding equip, use, or crafting actions. Inventory list rows and numbered navigation controls SHALL retain their existing layout; classification SHALL appear in inspection.

#### Scenario: Inspect a weapon or consumable
- **WHEN** a player browses and inspects an item carrying weapon or consumable components
- **THEN** the item has descriptive classification labels in inspection
- **AND** no equip, use, or crafting action is offered and ownership and quantities remain unchanged
