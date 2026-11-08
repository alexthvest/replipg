# characters Specification

## Purpose

Give each player a persistent character identity within each group without requiring registration or an initial name entry.

## Requirements

### Requirement: One character per user per group
The system SHALL maintain at most one character for each pair of Telegram group and user identities. Characters in different groups MUST have independent names and inventories.

#### Scenario: Repeated or concurrent creation
- **WHEN** repeated or concurrent interactions request a character for the same group and user
- **THEN** all successful interactions refer to the same single character

#### Scenario: Same user in another group
- **WHEN** an existing player first interacts with gameplay in a different group
- **THEN** the system creates a distinct character for that group without copying the first group's name changes or inventory

### Requirement: Characters are created automatically
The system SHALL create a missing character on the user's first valid character or inventory interaction, or as part of a successful item grant to that user. Ordinary group messages, settings access, and rejected grants MUST NOT create characters.

#### Scenario: First profile or inventory request
- **WHEN** a user without a character opens their profile or inventory
- **THEN** a character is persisted automatically without a registration step or name prompt

#### Scenario: Grant as first interaction
- **WHEN** a valid grant targets a user without a character
- **THEN** the recipient's character and granted ownership are persisted together

#### Scenario: Unrelated or rejected interaction
- **WHEN** a user sends ordinary chat text, opens settings, or is targeted by an invalid grant
- **THEN** that action creates no character

### Requirement: Initial names use Telegram identity
A new character SHALL use the user's Telegram username when available and otherwise the user's Telegram display name. Initial naming MUST NOT require user input, and later Telegram identity changes MUST NOT overwrite the saved character name.

#### Scenario: Username available
- **WHEN** a character is created for a user with a Telegram username
- **THEN** that username supplies the initial character name

#### Scenario: No username
- **WHEN** a character is created for a user without a Telegram username
- **THEN** the user's Telegram display name supplies the initial character name

#### Scenario: Telegram identity changes
- **WHEN** an existing player's Telegram username or display name changes
- **THEN** the stored character name remains unchanged

### Requirement: Players can view and rename their character
The system SHALL let a user view their current-group character profile and rename only that character. Names MUST be nonblank, within a documented length limit, persisted as user-authored text, and displayed safely.

#### Scenario: Successful rename
- **WHEN** a user submits a valid name for their current-group character
- **THEN** subsequent profile views show the saved name, including after restart
- **AND** the user's characters in other groups are unchanged

#### Scenario: Invalid name
- **WHEN** a rename submits a blank or over-limit name
- **THEN** the bot returns a localized validation error and preserves the previous name

#### Scenario: Attempt to rename another player's character
- **WHEN** a user submits a reference to another user's character
- **THEN** the system rejects the rename without changing that character
