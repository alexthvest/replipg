# Spec Delta

## MODIFIED Requirements

### Requirement: Characters are created automatically
The system SHALL create a missing character on the user's first valid character, inventory, or shop interaction, or as part of a successful item grant to that user. Ordinary group messages, settings access, malformed shop commands, and rejected grants MUST NOT create characters.

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
