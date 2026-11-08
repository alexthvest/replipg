# group-administration Specification

## Purpose

Let the configured bot owner delegate game administration independently in each group without relying on Telegram administrator status.

## Requirements

### Requirement: Bot owner manages game admins
The configured bot owner SHALL be able to appoint and revoke game admins in the current group through group commands. Other users MUST NOT change game admin assignments.

#### Scenario: Owner appoints an admin
- **WHEN** the configured owner appoints a Telegram user as a game admin in a group
- **THEN** the assignment is persisted for that user in that group

#### Scenario: Unauthorized appointment
- **WHEN** a user other than the configured owner attempts to appoint or revoke a game admin
- **THEN** the system denies the action and leaves assignments unchanged

#### Scenario: Revoked admin
- **WHEN** the owner revokes a user's game admin role
- **THEN** subsequent privileged actions by that user in that group are denied unless the user is the configured owner

### Requirement: Privileges are independent of Telegram roles
The configured owner and current group's game admins SHALL be authorized to grant items and change the group language. Telegram group administrator status alone MUST NOT confer game privileges, and a game admin role MUST NOT confer privileges in another group.

#### Scenario: Admin in another group
- **WHEN** a game admin in group A attempts a privileged action in group B without a role there
- **THEN** the system denies the action without changing either group's state

#### Scenario: Telegram administrator without a game role
- **WHEN** a Telegram group administrator without a game role attempts an item grant or language change
- **THEN** the system denies the action

#### Scenario: Configured owner without an assignment
- **WHEN** the configured owner grants items or changes language in a supported group
- **THEN** the system authorizes the action without requiring a separate game admin assignment

### Requirement: Permissions are checked at action time
The system SHALL check current group permissions for every privileged command and button action. A previously opened menu MUST NOT preserve revoked privileges.

#### Scenario: Revocation after opening settings
- **WHEN** an admin opens settings, loses the role, and then presses a language button
- **THEN** the language remains unchanged and the action is denied
