# Item Grants

## Purpose

Let authorized group administrators grant catalog items to players through reply commands with atomic persistence and protection against duplicate deliveries.

## ADDED Requirements

### Requirement: Grants identify recipients by reply
The `/give <item_id> <quantity>` command SHALL grant an active catalog item to the Telegram user whose message is being replied to in the current group. The actor MUST be the bot owner or a current game admin of that group. Recipient resolution MUST NOT require a username. Owner-only `/items` buttons SHALL provide self-grants of quantity one without a reply, following the same atomicity and replay rules.

#### Scenario: Valid grant
- **WHEN** an authorized actor replies to a player's message with `/give coin 5` and coin is an active stackable item
- **THEN** the system grants five coins to that player's current-group inventory

#### Scenario: Recipient has no username
- **WHEN** an authorized grant replies to a valid user's message and the user has no Telegram username
- **THEN** the grant resolves the recipient by Telegram user identity

#### Scenario: Missing or invalid reply recipient
- **WHEN** the command has no reply or the replied message is from a bot, an anonymous sender, or a sender without a usable Telegram user identity
- **THEN** the system rejects the grant without creating a character or inventory entry

#### Scenario: Actor lacks current-group permissions
- **WHEN** a user without current-group game privileges submits a grant
- **THEN** the grant is denied without changing characters or ownership

### Requirement: Invalid grants have no side effects
The system SHALL require an active item ID and a strictly positive integer quantity within documented numeric and request limits. Malformed arguments, unknown or removed IDs, and quantity overflow MUST be rejected with a localized error and no character or inventory changes.

#### Scenario: Invalid quantity
- **WHEN** quantity is missing, zero, negative, fractional, nonnumeric, or outside supported limits
- **THEN** the grant fails without modifying any character or inventory

#### Scenario: Item definition absent
- **WHEN** a grant names an unknown or deleted catalog ID
- **THEN** the grant is rejected even if existing inventories still contain snapshots with that ID

#### Scenario: Stack addition overflows
- **WHEN** adding the requested quantity would exceed the supported stored quantity range
- **THEN** the complete grant is rejected and the existing quantity is preserved

### Requirement: Grants commit atomically
A successful grant SHALL persist any missing recipient character, all resulting ownership changes, immutable snapshots, and its durable operation result in one atomic transaction. The system MUST report success only after commit.

#### Scenario: New recipient character
- **WHEN** a valid grant targets a player without a character
- **THEN** the recipient character, owned entries, and operation result are committed together

#### Scenario: Partial persistence failure
- **WHEN** persistence fails while creating a recipient or granting multiple individual items
- **THEN** all changes from that grant are rolled back and no success is reported

#### Scenario: Asset reload overlaps grant
- **WHEN** active assets change during grant processing
- **THEN** all items in that grant use one consistent validated definition snapshot

### Requirement: Telegram delivery replay cannot duplicate grants
The system SHALL durably identify grant operations by bot identity and Telegram update ID. Repeated or concurrent deliveries of the same operation MUST apply ownership changes at most once, including after restart or a response-send failure. Distinct update IDs SHALL represent distinct intentional commands.

#### Scenario: Concurrent duplicate delivery
- **WHEN** two handlers receive the same grant update concurrently
- **THEN** only one set of inventory changes commits and both deliveries resolve to the recorded operation outcome

#### Scenario: Commit succeeds but response fails
- **WHEN** a grant commits, Telegram response delivery fails, and the update is retried after restart
- **THEN** the saved operation result is used without creating another character, stack increment, or instance

#### Scenario: Intentional repeated command
- **WHEN** an admin intentionally sends the same grant command in two different updates
- **THEN** each valid update grants the requested items once

### Requirement: Grant outcomes use the group language
The bot SHALL render grant outcomes in the current group's language and identify the recipient, item, and quantity for successful grants. A confirmation MUST reflect the committed item snapshot rather than an unrelated later catalog version.

#### Scenario: Russian success confirmation
- **WHEN** a grant commits in a Russian-language group
- **THEN** the confirmation uses Russian interface text and the Russian item name captured by that grant
