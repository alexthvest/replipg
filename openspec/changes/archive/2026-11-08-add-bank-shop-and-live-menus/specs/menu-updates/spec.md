# Spec Delta

## Purpose

Keep active group menus consistent with committed game state and published assets without requiring another interaction from the player.

## ADDED Requirements

### Requirement: Committed changes refresh dependent active menus
The system SHALL automatically refresh all affected active bound menus after committed inventory, reservation, order, character, group language, or permission changes and successful item, offer, or locale publication. Only menus depending on changed data MUST be refreshed. Refreshes MUST use committed state and one consistent asset version without additional chat messages.

#### Scenario: Inventory opened during a pending purchase
- **WHEN** a player opens inventory while a purchase is pending and that purchase subsequently commits its items
- **THEN** that existing inventory message automatically reflects the owned items without another player interaction

#### Scenario: Grant affects several open inventories
- **WHEN** a grant commits for a player with several active inventory menus in that group
- **THEN** all those menus refresh while other players' inventories and other groups' menus remain unchanged

#### Scenario: Language or permission changes
- **WHEN** the group's language changes or a game admin loses privileges
- **THEN** affected active menus refresh their language or available controls and further actions check current permissions

### Requirement: Refresh preserves the current screen
An automatic update SHALL preserve the player's current screen, page, selected item, and selected order when still valid. Invalid pages MUST clamp to a valid page; a removed selected entry MUST return to an inventory or sale list. Inspection MUST remain based on saved item data. Changed quote terms MUST require renewed confirmation.

#### Scenario: Selected item is sold
- **WHEN** a completed sale removes the individual item currently under inspection
- **THEN** the same menu returns to the nearest valid inventory page with valid controls

#### Scenario: Unrelated inventory change during inspection
- **WHEN** an item is granted while the player inspects another entry
- **THEN** the menu retains that inspection instead of switching to the list

### Requirement: Menu delivery cannot regress the screen
Text and buttons SHALL be updated together. Automatic updates and interaction-driven edits to the same message MUST be ordered so an older render cannot overwrite a newer screen. Rapid changes MUST coalesce toward the newest state, and unchanged renders MUST avoid unnecessary edits. Superseded controls MUST be rejected rather than reinterpreted against newer item positions.

#### Scenario: Navigation races a delayed refresh
- **WHEN** a player navigates while a prior refresh is delayed
- **THEN** the final displayed message represents the newest screen and state

#### Scenario: Stale numbered button
- **WHEN** a callback uses a numbered item button from a superseded render
- **THEN** it is rejected and cannot act on the item now occupying that number

### Requirement: Delivery failures do not repeat domain operations
Temporary edit failures SHALL be retried with bounded rate-aware backoff while a session is active. Uneditable or deleted messages MUST stop receiving refresh attempts, with the player able to reopen a menu. Failed edits MUST NOT undo committed state or repeat a charge, payout, reservation, or grant. Slow edits MUST NOT block gameplay processing.

#### Scenario: Telegram rejects an edit temporarily
- **WHEN** inventory fulfillment commits but its menu edit receives a rate limit
- **THEN** a later retry renders current committed inventory without repeating the purchase

#### Scenario: Message deleted
- **WHEN** Telegram establishes that a subscribed message cannot be edited
- **THEN** its subscription stops and the player can open a new menu showing current state

### Requirement: Menu lifetime remains bounded
Automatic updates SHALL target only bound, active sessions and MUST NOT extend inactivity expiry. Expired or evicted sessions and sessions from a previous process MUST receive no new edits. Order recovery MUST continue independently of menu lifetime, and initial message binding MUST render current state to close changes that occurred during sending.

#### Scenario: Background activity does not keep a menu alive
- **WHEN** repeated state changes occur without player interaction until the inactivity TTL elapses
- **THEN** the session expires and no further automatic edits are sent

#### Scenario: Purchase settles while inventory message is being sent
- **WHEN** inventory state changes between its initial render and successful message binding
- **THEN** the bound menu is rendered from current committed state and shows the completed purchase

#### Scenario: Restart with pending purchase
- **WHEN** recovery fulfills a purchase after restart
- **THEN** prior process menus remain invalid and a newly opened inventory shows the committed items
