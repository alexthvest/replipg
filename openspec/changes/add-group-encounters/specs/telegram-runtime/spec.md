# Telegram Runtime Delta

## MODIFIED Requirements

### Requirement: Committed game state survives restart
The system SHALL retain committed characters, inventory snapshots and quantities, group settings, game admin assignments, processed grant results, shop orders, sale reservations, bank recovery records, character health and recovery timestamps, encounter snapshots and participants, accepted round actions, round deadlines and outcomes, spawn schedules and activity times, reward results, and pending encounter delivery intents across restart. Runtime entity reconstruction MUST preserve durable identities. Menus SHALL remain ephemeral and MUST be reopened after restart.

#### Scenario: Restart with existing state
- **WHEN** the bot restarts after successful state-changing interactions
- **THEN** subsequent interactions observe the committed state and original durable identities

#### Scenario: Restart with outstanding financial work
- **WHEN** the bot restarts after sending an invoice or sale payout without observing its final result
- **THEN** it resumes reconciliation using the saved terms and financial key without duplicating money or ownership changes

#### Scenario: Restart during a round
- **WHEN** the bot restarts with an unfinished encounter
- **THEN** it restores accepted attacks, participant knockout flags, captured definitions, and the stored round state
- **AND** it neither repeats completed damage nor grants completed loot again

#### Scenario: Restart with a pending spawn
- **WHEN** the bot restarts before a persisted spawn due time
- **THEN** it retains the due time without resampling an earlier spawn

## ADDED Requirements

### Requirement: Encounter recovery does not simulate unannounced rounds
If an open round is overdue after restart, the system SHALL resolve that round once using its previously accepted submissions. A subsequent round SHALL receive a fresh announcement and a full submission interval before it can resolve. Recovery MUST NOT advance through multiple unannounced rounds or create a burst of missed encounters.

#### Scenario: Long downtime during combat
- **WHEN** the bot resumes after several round durations elapsed
- **THEN** it resolves only the stored overdue round before announcing the next round
- **AND** players receive the full configured interval for that next round

### Requirement: Encounter delivery preserves committed round ordering
The system SHALL retry pending encounter announcements and updates independently of committed combat mutations. A round's submission deadline SHALL begin only after its announcement is successfully delivered and bound to the encounter. Delivery failure MUST NOT silently consume the player response interval. Ambiguous sends can duplicate messages but MUST NOT create extra encounters or valid alternate controls.

#### Scenario: Failed round announcement
- **WHEN** Telegram delivery of a new round announcement fails
- **THEN** the round remains unavailable for submissions and its timer does not run until delivery is confirmed

#### Scenario: Ambiguous send
- **WHEN** Telegram accepted a message but the bot did not record its delivery result
- **THEN** a retry can produce another announcement
- **AND** only the message successfully bound to the current encounter revision can accept actions
