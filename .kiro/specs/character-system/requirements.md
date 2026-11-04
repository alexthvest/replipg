# Requirements Document

## Introduction

The Character System gives each Telegram user one persistent, generic character shared across the groups where the game is available. A player's first character-related command creates the character automatically and presents its state through a user-specific ephemeral group response. Characters start at full health with configuration-backed maximum health, attack, and defense. Reaching zero health causes temporary incapacitation; after the configured delay, the character recovers a configured percentage of maximum health. Health does not regenerate passively.

## Boundary Context

- **In scope**: Automatic and idempotent character creation, personal character summaries, persistent base attributes and health, temporary incapacitation, deadline-based partial recovery, character eligibility, and atomic health/status changes for other gameplay features.
- **Out of scope**: Classes, races, skills, XP, levels, stat allocation, customization, permanent death, currency, achievements, multiple characters per user, equipment modifiers, item management, encounter spawning, damage calculation, battle participation rules, and private-chat flows.
- **Adjacent expectations**: The Telegram Runtime supplies authenticated actor/group identities, ephemeral response authorization and delivery, durable state, and atomic operations. Inventory supplies equipment and item effects. Battles decide damage and permitted item use, request character changes, and coordinate their own outcomes with character changes. Character eligibility means that a character exists and is not incapacitated; downstream features remain responsible for additional encounter or battle restrictions.
- **Confirmed product decisions**: Automatically create on the first character-related command; start at full health; use maximum health, attack, and defense; incapacitate immediately at zero health; restore a configurable percentage of maximum health, rounding upward to at least 1 HP; do not regenerate health passively.

## Requirements

### Requirement 1: Automatic Creation and Stable Ownership

**Objective:** As a community member, I want a character to be created when I first use a character command, so that I can enter the game without a separate registration flow.

#### Acceptance Criteria

1. When an identified player issues a supported character-related command in a group and has no character, the Character System shall create one generic character for that player as part of handling the command.
2. When a player already has a character and issues a character-related command, the Character System shall use the existing character without resetting its health, attributes, incapacitation state, or recovery deadline, except for the deadline-based recovery required by Requirement 5.
3. The Character System shall associate at most one character with each Telegram user across all supported groups and maintain a stable character identity for other gameplay features.
4. When duplicate or concurrent requests attempt to create a character for the same user, the Character System shall produce a single character with one initial state.
5. The Character System shall allow character creation without class selection, attribute allocation, profile customization, or a private-chat conversation.
6. If a request lacks a valid individual player identity or supported group context, the Character System shall reject it without creating or changing a character.

### Requirement 2: Personal Group Interaction

**Objective:** As a player, I want to inspect my character within the group without exposing my personal interface to other members.

#### Acceptance Criteria

1. When a character-related command completes successfully, the Character System shall request an ephemeral response in the originating group visible only to the requesting player.
2. When a player inspects their character, the Character System shall show current health, maximum health, base attack, base defense, and whether the character is active or incapacitated.
3. While a character is incapacitated, when its summary is requested, the Character System shall include its recovery deadline or remaining recovery duration.
4. When a player's first character-related command creates a character, the Character System shall indicate that creation occurred and show the resulting character summary.
5. If a personal response lacks valid ephemeral authorization or ephemeral delivery fails, the Character System shall not send that response as a public group message or private-chat message.
6. If a player supplies another player's identity in command arguments or interaction data, the Character System shall not use it to inspect or mutate that other player's character through the personal interface.

### Requirement 3: Starting Attributes and Configuration

**Objective:** As an operator, I want consistent, configurable starting attributes, so that the generic character can be tuned without adding character customization.

#### Acceptance Criteria

1. When a character is created, the Character System shall assign the configured starting maximum health, attack, and defense and set current health equal to maximum health.
2. The Character System shall use version-controlled configuration for starting attributes, incapacitation duration, and the recovery-health percentage.
3. If character configuration specifies nonpositive or fractional maximum health, negative or fractional attack or defense, a nonpositive incapacitation duration, or a recovery percentage outside the exclusive range (0, 100), the Character System shall reject that configuration before accepting character operations.
4. When starting-attribute configuration changes, the Character System shall retain the stored base attributes and current health of existing characters.
5. The Character System shall keep base attributes distinct from equipment-derived modifiers and shall not apply equipment effects as permanent changes to base attributes.

### Requirement 4: Health Changes and Immediate Incapacitation

**Objective:** As a gameplay feature, I want consistent character health changes, so that combat and item effects cannot leave a character in an invalid state.

#### Acceptance Criteria

1. The Character System shall maintain current health between zero and the character's maximum health, inclusive.
2. When a permitted damage operation is accepted, the Character System shall reduce current health by the requested amount without reducing it below zero.
3. When a permitted healing operation is accepted, the Character System shall increase current health by the requested amount without exceeding maximum health.
4. When an accepted health change reduces an active character's health to zero, the Character System shall make the character incapacitated and establish its recovery deadline as one indivisible outcome.
5. If a health operation requests a negative or non-whole-number HP amount or targets a nonexistent character, the Character System shall reject the operation without creating a character or changing character state.
6. While an incapacitation deadline has not expired, the Character System shall keep the character ineligible for battle and shall not allow a health mutation to clear or bypass incapacitation.
7. The Character System shall preserve current health as time passes except for accepted gameplay health changes and the recovery transition specified in Requirement 5.

### Requirement 5: Deadline-Based Partial Recovery

**Objective:** As an incapacitated player, I want to become playable again after a predictable delay, including when the bot restarts.

#### Acceptance Criteria

1. When a character becomes incapacitated, the Character System shall establish its recovery deadline using the incapacitation time and the configured duration.
2. While the current time is earlier than the recovery deadline, the Character System shall report the character as incapacitated and ineligible for battle.
3. When the current time is equal to or later than the recovery deadline, the Character System shall treat the character as active for character summaries, eligibility checks, and subsequent permitted gameplay operations without requiring a player recovery command.
4. When an incapacitated character recovers, the Character System shall set current health to the configured recovery percentage of its maximum health rounded upward to a whole HP, with a minimum of 1 HP and a maximum of maximum health, and clear the expired incapacitation state.
5. When repeated or concurrent operations observe the same expired incapacitation, the Character System shall apply recovery once without subsequently restoring health lost after that recovery.
6. When the runtime restarts before or after a recovery deadline, the Character System shall preserve the original deadline and determine recovery from elapsed time rather than restart time.

### Requirement 6: Eligibility and Gameplay Coordination

**Objective:** As an encounter or battle feature, I want reliable character state and eligibility, so that gameplay uses consistent character rules.

#### Acceptance Criteria

1. When another gameplay feature requests character state, the Character System shall provide the stable character identity, owning player identity, base attributes, current health, and current incapacitation or recovery state.
2. When character eligibility is requested, the Character System shall distinguish a missing character, an incapacitated character, and an active character after accounting for any expired recovery deadline.
3. When an eligibility lookup finds no character, the Character System shall report that absence without creating a character as a side effect of the lookup.
4. When a gameplay action combines a character health/status change with battle or inventory changes, the Character System shall allow all of those changes to succeed or fail together.
5. The Character System shall leave damage calculation, equipment modifiers, item-use permission, battle cooldowns, and encounter participation restrictions to their respective gameplay features.

### Requirement 7: Persistence, Replay, and Failure Consistency

**Objective:** As a player, I want my character to survive restarts and repeated requests without losing progress or receiving unintended health changes.

#### Acceptance Criteria

1. When a character operation is successfully committed, the Character System shall preserve its identity, ownership, base attributes, health, and any incapacitation/recovery data across runtime restarts.
2. When an already applied character mutation is submitted again with the same operation identity, the Character System shall not apply its health or status effects a second time.
3. When different valid operations affect the same character concurrently, the Character System shall produce an outcome consistent with applying the accepted operations in sequence while preserving character invariants.
4. If an operation cannot commit, the Character System shall preserve the preceding committed character state and shall not report the attempted mutation as successfully applied.
5. If response delivery fails after a character change commits, the Character System shall retain the committed state and shall allow a later character command to inspect it without recreating or resetting the character.
6. When a repeated request concerns a still-current incapacitation, the Character System shall not extend its deadline or grant additional recovery health merely because the request was repeated.

### Requirement 8: Privacy and Reproducible Delivery

**Objective:** As an operator, I want to verify character behavior in a test group without collecting unnecessary player data or exposing personal responses.

#### Acceptance Criteria

1. The Character System shall not require or persist player names, usernames, profile photos, or personal message content to create or operate a character.
2. The Character System shall exclude personal command content, ephemeral response content, and credentials from logs and operational diagnostics.
3. Where development verification is enabled, the Character System shall provide a development-only way to demonstrate health loss, incapacitation, and recovery without waiting through production recovery durations.
4. While development verification is disabled, the Character System shall not expose its development-only health or time controls to players.
5. The Character System shall provide documented commands and expected observations for reproducing automatic creation, character inspection, incapacitation, partial recovery, and restart persistence in a Telegram test group.
6. The Character System shall support automated verification of recovery boundaries and concurrent or repeated operations using controlled time and isolated test state without contacting real Telegram.
