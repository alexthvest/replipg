# telegram-runtime Specification

## Purpose

Provide a reliable Telegram interface for gameplay inside independent group worlds, with durable state and controlled lifecycle behavior.

## Requirements

### Requirement: Gameplay stays inside groups
The bot SHALL provide gameplay only in Telegram groups and supergroups through messages, commands, and inline buttons. It MUST NOT provide private-chat gameplay or require mini apps.

#### Scenario: Supported group interaction
- **WHEN** a user invokes a supported command in a group or supergroup
- **THEN** the bot handles the interaction and replies in that group

#### Scenario: Private or channel update
- **WHEN** an update originates from a private chat or broadcast channel
- **THEN** the bot creates no game state and sends no gameplay response

### Requirement: Group boundaries are enforced
The system SHALL identify every gameplay action by its originating group and acting Telegram user. Characters, ownership, permissions, settings, and menu actions MUST remain isolated to that group.

#### Scenario: Same player in two groups
- **WHEN** a user interacts in two different groups
- **THEN** each interaction accesses only the state belonging to its originating group

#### Scenario: Foreign group reference
- **WHEN** an interaction contains a reference to another group's character, inventory entry, or menu
- **THEN** the system rejects the action without exposing or changing the referenced state

### Requirement: Menus use consistent text formatting
Menu headings SHALL be bold and displayed values SHALL use inline code, except that inventory and catalog list entry numbers and item names SHALL use normal text. Inventory list quantities and catalog list IDs SHALL include their parentheses within inline code. Inline button captions SHALL remain plain text. User and asset text MUST NOT introduce unintended formatting.

#### Scenario: Formatted settings values
- **WHEN** the settings menu is rendered
- **THEN** its heading is bold and the selected language value uses inline code

#### Scenario: Formatting characters in a value
- **WHEN** a displayed value contains backticks, brackets, or other formatting characters
- **THEN** it appears literally within the intended code formatting without altering surrounding content

### Requirement: Committed game state survives restart
The system SHALL retain committed characters, inventory snapshots and quantities, group settings, game admin assignments, and processed grant results across restart. Runtime entity reconstruction MUST preserve durable identities.

#### Scenario: Restart with existing state
- **WHEN** the bot restarts after successful state-changing interactions
- **THEN** subsequent interactions observe the committed state and original durable identities

### Requirement: Startup validates required resources
The bot SHALL validate required configuration, durable storage, and initial assets before accepting gameplay. Startup failure MUST produce an actionable diagnostic without exposing bot credentials.

#### Scenario: Invalid startup resources
- **WHEN** required configuration is missing, storage cannot be opened, or initial assets fail validation
- **THEN** the bot refuses gameplay startup and reports the failing resource without logging secrets

### Requirement: Shutdown preserves mutation integrity
The bot SHALL stop accepting new gameplay work on shutdown and finish or roll back accepted mutations before releasing its resources. It MUST NOT acknowledge an uncommitted mutation as successful.

#### Scenario: Shutdown during a grant
- **WHEN** shutdown begins while an item grant is being processed
- **THEN** the grant either commits completely or leaves no partial changes
- **AND** any successful acknowledgement corresponds to a committed grant
