# Characters Delta

## MODIFIED Requirements

### Requirement: Characters are created automatically
The system SHALL create a missing character on the user's first valid character, inventory, or shop interaction, as part of a successful item grant to that user, or with their first accepted encounter attack. Ordinary group messages, settings access, malformed shop commands, rejected grants, and rejected encounter attacks MUST NOT create characters. Encounter participation and any required character creation MUST commit together.

#### Scenario: First profile or inventory request
- **WHEN** a user without a character opens their profile or inventory
- **THEN** a character is persisted automatically without a registration step or name prompt

#### Scenario: Grant as first interaction
- **WHEN** a valid grant targets a user without a character
- **THEN** the recipient's character and granted ownership are persisted together

#### Scenario: Unrelated or rejected interaction
- **WHEN** a user sends ordinary chat text, opens settings, sends a malformed shop command, or is targeted by an invalid grant
- **THEN** that action creates no character

#### Scenario: First shop request
- **WHEN** a user without a character opens `/shop` in a supported group
- **THEN** a persistent current-group character is created automatically without requiring a purchase

#### Scenario: Attack as first interaction
- **WHEN** a user without a character submits a valid attack to an open encounter round in their group
- **THEN** their character, initial full health, encounter participation, and round submission are persisted together without a join step

#### Scenario: Rejected encounter attack
- **WHEN** a user without a character presses an invalid, expired, foreign, or terminal encounter control
- **THEN** no character, health record, participation, or round submission is created
