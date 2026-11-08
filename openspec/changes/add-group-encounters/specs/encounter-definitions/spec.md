# Encounter Definitions

## Purpose

Let maintainers configure monsters and group encounters through validated CUE assets without embedding individual encounters in application code.

## ADDED Requirements

### Requirement: Mobs and events use separate CUE catalogs
The system SHALL load CUE mob definitions from `assets/mobs/` using `package mobs` and event definitions from `assets/events/` using `package events`. Each mob or event SHALL have its own file named after its stable catalog ID. Schema and shared settings files SHALL remain separate from individual definitions.

#### Scenario: Separate definitions
- **WHEN** a maintainer adds `mobs/slime.cue` and `events/slime-encounter.cue`
- **THEN** each file defines one matching catalog ID and the event references the mob by stable ID

### Requirement: Mob settings define combat behavior
Each mob SHALL define a localized name, positive maximum health, and at least one announced attack. Each attack SHALL define a stable ID, localized announcement, positive damage, and a supported targeting type. Bounded multi-target attacks SHALL specify a positive target count. Invalid numeric ranges and unsupported attack types MUST be rejected.

#### Scenario: Invalid mob
- **WHEN** a mob has zero health, negative damage, a missing announcement, or an unsupported target type
- **THEN** the candidate asset bundle is rejected with a diagnostic identifying the definition

### Requirement: Event settings define encounters and spawning
Each event SHALL reference one existing mob and define its category, selection weight, round duration, maximum rounds, and individual loot. Shared event settings SHALL define the random spawn delay bounds and recent-activity window. Only common and unique categories SHALL be executable in this version; world events MUST NOT spawn.

#### Scenario: Broken reference or settings
- **WHEN** an event names a missing mob or reward item, has nonpositive timing or weight, or shared minimum delay exceeds maximum delay
- **THEN** the candidate asset bundle is rejected

#### Scenario: Future world event
- **WHEN** a candidate contains an executable event with category world
- **THEN** validation rejects it as unsupported in this version rather than spawning it in one group

### Requirement: Encounter assets publish as one valid bundle
Mobs, events, items, shop offers, and locales SHALL be validated and published together. Referenced mob names and attack announcements MUST exist in English and Russian. An invalid reload SHALL preserve the entire previous bundle; invalid required startup assets SHALL prevent gameplay startup. An intentionally empty event catalog SHALL disable new spawning.

#### Scenario: Incomplete translation
- **WHEN** a mob announcement is missing its Russian translation during reload
- **THEN** no part of the candidate bundle is published and current gameplay continues with the previous bundle

#### Scenario: Disabled catalog
- **WHEN** a valid empty event catalog is published
- **THEN** no new encounter spawns and already active encounters continue

### Requirement: Active encounters retain captured definitions
At spawn, an encounter SHALL capture its event and mob combat settings, announcements in both languages, and reward item snapshots from one validated bundle. Subsequent edits or removals MUST NOT change that encounter's timing, health, attacks, or reward terms. New encounters SHALL use the newly published settings.

#### Scenario: Mob and loot edited mid-fight
- **WHEN** a maintainer changes or removes a spawned monster, event, or reward item
- **THEN** the active encounter and its eventual loot use their captured definitions
- **AND** the next spawn uses only currently eligible definitions
