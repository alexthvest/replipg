# Requirements Document

## Introduction

The Inventory System gives each player's persistent character a personal collection of consumables, weapons, armor, and inert stackable items shared across Telegram groups. Players open and manage their inventory through user-specific ephemeral group interactions. Opening inventory creates a missing character and grants a configurable starter kit exactly once. Players equip one weapon that adds attack and one armor that adds defense. The initial consumable effect is fixed-HP self-healing, used through battle-authorized actions without spending an item at full health or during incapacitation.

## Boundary Context

- **In scope**: A starter kit, persistent ownership and quantities, inventory summaries and item details, ephemeral equip/unequip interactions, equipment-derived attack and defense, atomic item grants, and atomic consumption coordinated with character healing and battle actions.
- **Out of scope**: Trading, gifting, crafting, shops, currency balances or spending, durability, randomized affixes, player storage facilities, auctions, administrator item editing, direct-damage consumables, temporary buffs, revival items, out-of-battle item use, and loot eligibility or selection policy.
- **Adjacent expectations**: Telegram Runtime supplies authenticated actors, group context, ephemeral delivery, and durable operations. Character System remains responsible for character identity, base attributes, health, incapacitation, and recovery. Battle System authorizes item use, applies combat restrictions and cooldowns, calculates damage, selects loot, and coordinates battle outcomes with inventory and character changes. This feature supplies inventory operations before battle gameplay is implemented; it does not introduce a separate battle or loot simulator into production.
- **Confirmed product decisions**: Create the character on first inventory access if needed; grant a starter kit once per character; equip one weapon for additive attack and one armor for additive defense; replace the current item when equipping another in the same slot; support fixed-HP self-healing only; reject healing consumption at full HP or while incapacitated.

## Requirements

### Requirement 1: Catalog Consumption and Starter Configuration

**Objective:** As an operator, I want version-controlled item content and starting supplies, so that players receive consistent items without an administrator editing interface.

#### Acceptance Criteria

1. The Inventory System shall consume validated stable item definitions and mechanics from Item System and resolve display text from localization resources.
2. The Inventory System shall use Item System healing amounts and equipment bonuses without redefining category/effect rules or changing character base attributes.
3. The Inventory System shall obtain starter-kit quantities from separate version-controlled inventory configuration.
4. If starter entries contain duplicate or undefined identities, nonpositive quantities or invalid numeric values, the Inventory System shall reject configuration before accepting operations.
5. The Inventory System shall preserve saved item identities through its persisted inventory_catalog compatibility projection, rejecting removal or changed registered categories/mechanics before admission.
6. The Inventory System shall aggregate equipped bonuses separately from character base attributes using checked arithmetic.
7. The Inventory System shall persist inert items, including Coin, as ordinary quantities without equipment, usable effects, currency balances or spending.

### Requirement 2: Character Ownership and One-Time Starter Kit

**Objective:** As a player, I want inventory access to prepare my character and starting supplies automatically, so that I can begin without a separate onboarding flow.

#### Acceptance Criteria

1. When an identified human player first opens inventory through an authorized group interaction and has no character, the Inventory System shall create the character through the Character System before establishing its inventory.
2. When a character opens inventory without having received its starter kit, the Inventory System shall grant the configured starter-kit quantities and record that grant as one indivisible outcome.
3. When a character that already exists first opens inventory, the Inventory System shall grant its starter kit without resetting its character attributes, health, or recovery state.
4. The Inventory System shall associate inventory, equipment, and starter-kit eligibility with the same persistent character across supported groups.
5. When inventory access is repeated, concurrent, retried after failure, or performed in another group, the Inventory System shall grant the starter kit at most once per character.
6. When starter-kit configuration changes after a character has received its kit, the Inventory System shall retain the character's received items without granting the kit again.
7. If inventory initialization cannot complete, the Inventory System shall leave neither a partial starter grant nor a completed starter entitlement that prevents a subsequent successful grant.

### Requirement 3: Ephemeral Inventory Inspection

**Objective:** As a player, I want to inspect and manage my items within the group without exposing my personal interface to other members.

#### Acceptance Criteria

1. When a player invokes the inventory command through an authorized ephemeral group interaction, the Inventory System shall provide an inventory response visible only to that player in the originating group.
2. When inventory is displayed, the Inventory System shall show owned item names and quantities, the equipped weapon and armor or their empty slots, and the resulting equipment attack and defense bonuses.
3. When a player requests details for an owned item, the Inventory System shall show its description, category, owned quantity, healing amount or equipment bonus, and applicable equip or unequip actions.
4. When a player's inventory contains no owned items, the Inventory System shall show an explicit empty-inventory state while retaining access to equipment inspection.
5. When inventory contents exceed one response's presentation limit, the Inventory System shall provide navigation that lets the player inspect every owned item within the supported response limits.
6. If a personal response lacks valid ephemeral authorization or delivery fails, the Inventory System shall not send its content as a public group message or private-chat message.
7. When an incapacitated player inspects inventory, the Inventory System shall allow inspection without clearing or bypassing incapacitation.
8. When inventory is displayed, the Inventory System shall use a compact `Inventory • <owner>` header, show numbered `Item name (quantity)` entries with equipped markers, and provide matching numbered item-selection controls, using the requesting player's available username as the owner label or `you` when unavailable.
9. When an inert item is inspected, the Inventory System shall show its name, description, and owned quantity with a Back control and no equip, unequip, or use action.

### Requirement 4: Equipment Assignment and Derived Attributes

**Objective:** As a player, I want to equip and remove owned weapons and armor, so that my loadout changes my combat attributes without altering my character's base stats.

#### Acceptance Criteria

1. The Inventory System shall allow at most one equipped weapon and one equipped armor item per character.
2. When a player equips an owned weapon or armor item through an authorized interaction, the Inventory System shall assign it to its matching slot and replace any previous assignment in that slot as one indivisible outcome.
3. When equipment is equipped, replaced, or unequipped, the Inventory System shall preserve total owned quantities and make the previously equipped item available for subsequent equipment selection.
4. If a request attempts to equip an unowned item, a consumable, an inert item, or an item incompatible with the requested slot, the Inventory System shall reject it without changing ownership or equipment.
5. When a player unequips an occupied slot, the Inventory System shall clear that assignment and remove its derived bonus.
6. When an already equipped item is equipped again or an empty slot is unequipped, the Inventory System shall leave ownership and bonuses unchanged.
7. When equipment modifiers are requested, the Inventory System shall provide the equipped weapon's attack bonus and equipped armor's defense bonus, using zero for an empty slot.
8. The Inventory System shall define effective attack as base attack plus the equipped weapon bonus and effective defense as base defense plus the equipped armor bonus, without changing base attributes, maximum health, current health, or recovery state.
9. When an equipment change commits, the Inventory System shall make the resulting assignments and bonuses available to subsequent gameplay reads and personal inventory responses.

### Requirement 5: Atomic Item Grants and Quantity Integrity

**Objective:** As a reward-producing gameplay feature, I want to grant items reliably, so that each accepted reward reaches its intended character exactly once.

#### Acceptance Criteria

1. When an authorized gameplay feature grants defined items in positive whole-number quantities to an existing character, the Inventory System shall increase that character's ownership by those quantities.
2. When additional copies of an already owned item are granted, the Inventory System shall combine their quantities without changing equipment assignments.
3. When a single reward grants multiple items, the Inventory System shall apply all quantities together or leave all quantities unchanged.
4. If a grant references an undefined item or missing character, requests a nonpositive or fractional quantity, or would exceed a supported numeric range, the Inventory System shall reject the grant without applying any part of it or creating a character.
5. The Inventory System shall maintain whole-number, nonnegative owned quantities and shall not permit equipped assignments to reference an item with no owned copy.
6. The Inventory System shall apply the item identities and quantities selected by the caller without deciding loot eligibility, probability, quality, or contribution weighting.

### Requirement 6: Battle-Authorized Healing Consumption

**Objective:** As a player, I want an accepted healing-item battle action to heal me and spend one item together, so that failures or repeated actions cannot waste supplies or duplicate healing.

#### Acceptance Criteria

1. When Battle System authorizes use of an owned healing consumable for an active character below maximum health, the Inventory System shall consume exactly one copy and apply that item's configured fixed-HP healing to the same character as one indivisible outcome.
2. When an accepted consumable's healing amount exceeds missing health, the Inventory System shall restore health up to maximum health and consume exactly one copy.
3. If the character is at full health or remains incapacitated after accounting for its recovery deadline, the Inventory System shall reject consumption without spending the item or applying healing.
4. If the requested item is undefined, not a consumable, or not owned in sufficient quantity, the Inventory System shall reject consumption without changing inventory or health.
5. When consumption removes the last owned copy of a consumable, the Inventory System shall cease listing it as an owned item and reject further consumption until another copy is granted.
6. The Inventory System shall provide the consumable's effect identity and healing amount to Battle System and leave battle eligibility, cooldowns, action acceptance, and monster responses to Battle System.
7. The Inventory System shall not expose consumable-use actions through the standalone inventory management interface or permit its healing effect to bypass character incapacitation.

### Requirement 7: Authorized Management and Stale Interactions

**Objective:** As a player, I want inventory controls to act only on my current items, so that another user or an outdated interface cannot change my inventory incorrectly.

#### Acceptance Criteria

1. When a personal inventory interaction is processed, the Inventory System shall validate the requesting individual player and supported group context and restrict inspection and mutation to that player's character.
2. If a request lacks a valid individual human actor or supported group context, the Inventory System shall reject it without creating a character, granting a starter kit, or changing inventory.
3. If interaction data is malformed, unknown, expired, associated with another player or group, or inconsistent with the expected inventory state, the Inventory System shall reject the requested mutation without applying it.
4. When a player uses a stale inventory control and a personal reply remains authorized, the Inventory System shall indicate that the control is no longer applicable and allow the player to reopen current inventory.
5. The Inventory System shall use compact opaque references in mutating controls and shall not treat player-supplied item quantities, effects, or ownership assertions as authoritative.
6. When an equip or unequip operation succeeds and a personal reply remains authorized, the Inventory System shall show the resulting equipment state.
7. When an authorized inventory button is pressed, the Inventory System shall edit the originating personal inventory message and its buttons for item details, pagination, Back, Equip, Unequip, and stale or expired control guidance instead of sending another message. Opening `/inventory` shall send a new personal view. Failed edits shall have no message fallback and shall preserve committed operations.

### Requirement 8: Atomic Coordination, Replay, and Persistence

**Objective:** As a player, I want accepted item operations to survive failures and restarts without item loss, duplication, or inconsistent battle results.

#### Acceptance Criteria

1. When an inventory operation commits, the Inventory System shall preserve ownership, quantities, equipment assignments, starter-kit receipt, and mutation replay protection across restarts.
2. When an accepted grant, equipment change, or consumption is repeated with the same operation identity and inputs, the Inventory System shall not apply that operation's changes a second time.
3. If an accepted operation identity is reused with different inputs, the Inventory System shall reject the conflicting request without changing inventory or replaying an effect.
4. When different operations concurrently affect the same character's inventory, the Inventory System shall produce a result consistent with an ordering of the accepted operations while preserving ownership and equipment invariants.
5. When an item action combines inventory, character health, and battle changes, the Inventory System shall allow those changes and their replay protection to succeed or fail together.
6. If an inventory operation cannot commit, the Inventory System shall preserve the preceding committed inventory state and shall not report the attempted change as successfully applied.
7. If personal response delivery fails after an operation commits, the Inventory System shall retain the committed result and allow a later inventory inspection to show the current state without repeating the grant, equipment change, or consumption.

### Requirement 9: Privacy and Reproducible Delivery

**Objective:** As an operator, I want to verify inventory behavior in runnable increments without exposing personal data or waiting for battle loot to be implemented.

#### Acceptance Criteria

1. The Inventory System shall operate using player and character identities and gameplay data without requiring or persisting names, usernames, profile photos, or personal message content.
2. The Inventory System shall exclude credentials, personal command content, and ephemeral inventory response content from logs and operational diagnostics.
3. Where development verification is enabled, the Inventory System shall provide development-only controls to demonstrate repeat-safe grants, equipment changes, and coordinated healing consumption before production battle actions exist.
4. While development verification is disabled, the Inventory System shall not expose development-only grant or consumption controls to players.
5. The Inventory System shall provide documented commands and expected observations for demonstrating starter-kit uniqueness, ephemeral inspection, equip/unequip, healing acceptance and rejection, and restart persistence in a Telegram test group.
6. The Inventory System shall support automated verification of duplicate, stale, concurrent, and failing item operations and character-effect coordination using isolated state and controlled time without contacting real Telegram.
7. When an implementation milestone is delivered, the Inventory System shall provide a runnable group interaction, automated verification, and documented reproduction steps for that milestone's behavior.

### Requirement 10: Owner-Authorized Development Integration

1. When REPLIPG_DEV_INVENTORY is enabled, composition shall require a strictly positive REPLIPG_OWNER_TELEGRAM_USER_ID before storage or network side effects.
2. When /dev_inventory_grant or /dev_inventory_heal is requested, the Inventory System shall require the authenticated actor to match the configured owner before development behavior or output; administrators and usernames shall not confer access.
3. The Inventory System shall restrict development mutations to the requesting owner's character and retain human, group and ephemeral authorization checks.
4. While inventory development verification is disabled, composition shall omit inventory and item development routes/menu entries and preserve independent runtime/character development behavior.
5. The Inventory System shall document successful owner use, non-owner rejection, invalid owner configuration, disabled probes, restart compatibility and unchanged state on rejection, retaining unobserved live evidence as pending.

## Approved Boundary Change

The user approved this split on 2026-11-04. Item System owns IDs, categories, effect kinds/definitions, definition validation and catalog content. Inventory owns quantities, starter policy, slots/bonus aggregation, grants, receipts and atomic healing coordination. Requirements 2–9 retain numbering and behavior for historical traceability. Dependencies: item-system, telegram-runtime and character-system; localization supplies presentation.
