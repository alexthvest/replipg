# Character Health

## Purpose

Maintain independent character health in each group, with passive recovery between encounters and durable knockout rules during combat.

## ADDED Requirements

### Requirement: Character health persists within its group
Each character SHALL have persistent current and maximum health, with current health bounded from zero through maximum. New characters and characters predating health support SHALL start at full health. Damage MUST carry into later encounters and MUST NOT affect the same user's characters in other groups. Character profiles SHALL display current and maximum health.

#### Scenario: Damaged character enters another encounter
- **WHEN** a damaged character enters a later encounter
- **THEN** health reflects prior damage plus applicable passive recovery rather than resetting to full

#### Scenario: Existing database migration
- **WHEN** health support is introduced for existing characters
- **THEN** each existing character starts at full health without changing its identity, name, or inventory

#### Scenario: Same user in another group
- **WHEN** one character takes damage
- **THEN** another group's character belonging to that user keeps its own health

### Requirement: Health regenerates passively over elapsed time
Characters SHALL recover health automatically at a documented fixed rate based on elapsed time, including outside combat, without exceeding maximum health. Recovery MUST survive restart, preserve fractional elapsed intervals, and never grant extra health from repeated reads or a backward clock movement. Healing consumables SHALL NOT be required in this version.

#### Scenario: Offline recovery
- **WHEN** a damaged character is next observed after the bot was stopped for several regeneration intervals
- **THEN** elapsed recovery is applied once and capped at maximum health

#### Scenario: Repeated profile reads
- **WHEN** a player opens their profile several times without another full regeneration interval elapsing
- **THEN** those reads do not produce additional recovery

### Requirement: Knockout excludes a player for the encounter lifetime
When encounter damage reduces a participant to zero health, that player SHALL become knocked out for the rest of that encounter. Knocked-out players MUST NOT attack or be targeted again in that encounter even if passive recovery restores health. A character with zero current health MUST NOT enter a new encounter until health recovers above zero.

#### Scenario: Recovery during the same encounter
- **WHEN** a knocked-out participant regenerates positive health while their encounter continues
- **THEN** they remain excluded from attacks and target selection for that encounter
- **AND** their earlier accepted attack still qualifies for victory loot

#### Scenario: Later encounter after recovery
- **WHEN** the old encounter has ended and the character has recovered positive health
- **THEN** the character can participate in a new encounter
