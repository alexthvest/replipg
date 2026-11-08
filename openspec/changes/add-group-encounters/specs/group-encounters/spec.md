# Group Encounters

## Purpose

Give players in each Telegram group cooperative monster encounters with timed rounds, lasting health consequences, and individual victory loot.

## ADDED Requirements

### Requirement: Spawns require recent gameplay activity
The system SHALL spawn encounters only in groups with a qualifying gameplay interaction within a configured activity window. Ordinary chat, invalid interactions, and background processing MUST NOT refresh that window. Inactivity MUST prevent new spawns without interrupting an active encounter.

#### Scenario: Inactive group
- **WHEN** a group's scheduled spawn becomes due after its activity window has elapsed
- **THEN** no encounter starts until qualifying player activity resumes
- **AND** missed spawn opportunities do not accumulate into a burst

#### Scenario: Activity signals
- **WHEN** a human successfully opens a character or inventory view, renames a character, performs a shop interaction, or submits an encounter attack
- **THEN** the current group's gameplay activity time is refreshed
- **AND** unrelated text, invalid requests, menu delivery, and scheduler ticks do not refresh it

### Requirement: Random scheduling permits one encounter per group
The system SHALL choose and persist a random spawn delay within configured bounds and select eligible common or unique events using configured weights. A group MUST have at most one active encounter. After an encounter ends, a new delay SHALL begin from its terminal time. Unique events MUST NOT enforce a minimum participant count.

#### Scenario: Encounter already active
- **WHEN** another spawn opportunity is processed for a group with an active encounter
- **THEN** a second encounter is not created

#### Scenario: Completed encounter
- **WHEN** an encounter reaches a terminal outcome
- **THEN** the next due time is its terminal time plus a newly sampled delay
- **AND** no new encounter starts while the group is inactive

#### Scenario: Unique encounter in a small group
- **WHEN** a unique event is selected and only one player participates
- **THEN** that player can attack without a party-size rejection
- **AND** the configured monster statistics and round limit determine whether victory is possible

### Requirement: Encounters publish announced timed rounds
Each encounter SHALL announce the monster, its remaining health, the round deadline, and the upcoming monster attack and targeting type when a round opens. Attack SHALL be the only player combat action. The announcement MUST appear before submissions for that round are enabled.

#### Scenario: Round announcement
- **WHEN** a monster opens a round with an all-participant attack
- **THEN** the group sees the attack announcement, its targeting type, the deadline, and a shared Attack control
- **AND** no Join or Defend control is offered

### Requirement: Attacking establishes lasting participation
A living player SHALL enter an active encounter by submitting their first valid attack without a separate join action. Participants SHALL remain eligible monster targets in skipped rounds until knockout or encounter termination. Nonparticipants MUST NOT take encounter damage. Players MAY enter later rounds while the encounter remains active.

#### Scenario: First attack and skipped round
- **WHEN** a player attacks in round one and submits no action in round two
- **THEN** the player remains a participant and an eligible living target in round two

#### Scenario: Late participant
- **WHEN** a living player first attacks during a later open round
- **THEN** the player joins the participant set for that round's resolution and subsequent rounds

#### Scenario: Ordinary group member
- **WHEN** a member has never submitted an accepted encounter attack
- **THEN** no monster attack in that encounter can damage that member

### Requirement: Each player submits at most one attack per round
The system SHALL accept at most one attack per player per encounter round before its deadline. Duplicate deliveries and distinct repeated clicks MUST NOT add damage or extra rewards. Stale rounds, expired deadlines, foreign groups or messages, knocked-out players, and terminal encounters MUST reject submissions without changing combat state.

#### Scenario: Repeated attacks
- **WHEN** a player presses Attack repeatedly in one round, including concurrent requests
- **THEN** exactly one accepted attack contributes damage

#### Scenario: Deadline passed before worker runs
- **WHEN** a player submits after the stored deadline but before the scheduler resolves that round
- **THEN** the attack is rejected for that round

#### Scenario: Foreign or terminal control
- **WHEN** a callback references another group's encounter, an unbound message, an old round, or an ended encounter
- **THEN** no participation, character creation, damage, or loot is applied

### Requirement: Player attacks resolve before retaliation
At the round deadline, the system SHALL resolve all accepted player attacks together using the same fixed per-player damage for this version. If the monster reaches zero health, victory SHALL prevent retaliation. Otherwise the monster SHALL execute the attack announced for that round against eligible living participants. Attack types SHALL support one target, a bounded set of distinct targets, or all eligible targets.

#### Scenario: Killing damage
- **WHEN** combined player damage reduces monster health to zero
- **THEN** the encounter ends in victory without executing the announced attack

#### Scenario: Target selection
- **WHEN** a surviving monster executes a multi-target attack with fewer eligible players than its target count
- **THEN** it hits each eligible player at most once and never substitutes nonparticipants

#### Scenario: Group-wide attack
- **WHEN** a surviving monster executes an all-participant attack
- **THEN** every living participant takes the configured damage and knocked-out players take none

### Requirement: Defeat and escape terminate encounters
The system SHALL end an encounter immediately in defeat when monster retaliation knocks out all participants. If nobody participates in the first round, the monster SHALL escape at that round's deadline. A surviving monster SHALL escape after the configured final round unless party defeat has already occurred. Terminal encounters MUST NOT admit new players or reopen after health recovery.

#### Scenario: Party defeated
- **WHEN** retaliation knocks out the final living participant
- **THEN** defeat is recorded immediately without opening another round
- **AND** a previously uninvolved player cannot enter that encounter

#### Scenario: Ignored spawn
- **WHEN** the initial round deadline passes without any accepted attacks
- **THEN** the monster escapes and the next spawn delay begins

#### Scenario: Round limit
- **WHEN** the final round ends with both the monster and at least one participant alive
- **THEN** the monster escapes and no victory loot is granted

### Requirement: Victory grants individual participation loot
For this version, every player with at least one accepted attack SHALL receive their own configured victory loot, including knocked-out participants. Nonparticipants SHALL receive none. Defeat and escape SHALL award no loot. Rewards MUST follow saved item snapshot and ownership rules and remain isolated to the encounter's group.

#### Scenario: Knocked-out contributor
- **WHEN** an encounter is won after an earlier attacker was knocked out
- **THEN** that attacker and each other contributor receive individual loot
- **AND** a member who never attacked receives nothing

### Requirement: Resolution and rewards are retry safe
A round's damage, health changes, terminal outcome, inventory rewards, and durable delivery intent SHALL commit atomically. Repeated round processing and restart retries MUST NOT apply damage or loot twice. Failed persistence MUST NOT report victory or leave partial rewards. Delivery retries MUST NOT rerun combat mutations.

#### Scenario: Persistence failure during victory
- **WHEN** saving any recipient's loot fails
- **THEN** the round and all its health and inventory changes roll back for safe retry

#### Scenario: Delivery failure after commit
- **WHEN** victory commits but the Telegram message update fails
- **THEN** retrying delivery or restarting does not grant another reward or apply damage again
