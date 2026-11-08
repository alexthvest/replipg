# Spec Delta

## MODIFIED Requirements

### Requirement: Gameplay stays inside groups
The bot SHALL provide gameplay only in Telegram groups and supergroups through messages, commands, and inline buttons. It MUST NOT provide private-chat gameplay or require mini apps. Authenticated private bank protocol replies SHALL be processed solely to reconcile previously persisted group orders, without accepting private player commands or creating unsolicited game state.

#### Scenario: Supported group interaction
- **WHEN** a user invokes a supported command in a group or supergroup
- **THEN** the bot handles the interaction and replies in that group

#### Scenario: Private or channel update
- **WHEN** an update originates from a private chat or broadcast channel and is not an authenticated correlated private bank reply
- **THEN** the bot creates no game state and sends no gameplay response

#### Scenario: Authenticated private bank reply
- **WHEN** the configured bank bot sends a valid correlated reply for a persisted group order in its private chat
- **THEN** the bot reconciles only that order within its recorded group and player scope
- **AND** no private gameplay response is sent

### Requirement: Committed game state survives restart
The system SHALL retain committed characters, inventory snapshots and quantities, group settings, game admin assignments, processed grant results, shop orders, sale reservations, and bank recovery records across restart. Runtime entity reconstruction MUST preserve durable identities. Menus SHALL remain ephemeral and MUST be reopened after restart.

#### Scenario: Restart with existing state
- **WHEN** the bot restarts after successful state-changing interactions
- **THEN** subsequent interactions observe the committed state and original durable identities

#### Scenario: Restart with outstanding financial work
- **WHEN** the bot restarts after sending an invoice or sale payout without observing its final result
- **THEN** it resumes reconciliation using the saved terms and financial key without duplicating money or ownership changes

### Requirement: Shutdown preserves mutation integrity
The bot SHALL stop accepting new gameplay and financial work on shutdown and finish or roll back accepted local mutations before releasing its resources. It MUST NOT acknowledge an uncommitted mutation as successful. Interrupted external requests MUST retain durable recovery records; bank settlement MUST NOT be assumed to stop when local delivery is canceled.

#### Scenario: Shutdown during a grant
- **WHEN** shutdown begins while an item grant is being processed
- **THEN** the grant either commits completely or leaves no partial changes
- **AND** any successful acknowledgement corresponds to a committed grant

#### Scenario: Shutdown during payout
- **WHEN** shutdown interrupts a sale transfer before its outcome is saved
- **THEN** the sale and reservation remain recoverable and restart retries with the same financial key
