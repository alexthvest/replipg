# Proposal

## Why

Characters and inventories exist, but players have no shared gameplay objective. Random monster encounters will give each Telegram group a cooperative combat loop with lasting health consequences and individual victory rewards.

## What Changes

### Confirmed requirements

- Spawn common and rare unique encounters at random times in recently active groups, with at most one active encounter per group and the next delay beginning after the previous encounter ends.
- Use timed rounds with Attack as the only player action and no join step. The first accepted attack establishes participation; participants remain targetable even when skipping rounds.
- Accept one attack per player per round. Resolve all player damage at the deadline, then execute the surviving monster's previously announced attack. Target selection depends on attack type and excludes nonparticipants and knocked-out players.
- Persist character health across encounters and regenerate it passively over time, including outside combat. Zero health knocks a participant out for the rest of that encounter, even after regeneration.
- End encounters on victory, immediately when all participants are knocked out, on the round limit, or after an initial round with no participants. Do not reopen a defeated party to new participants.
- Give individual victory loot to everyone who attacked at least once, including knocked-out participants. Keep this initial reward policy replaceable.
- Use fixed player damage in this version, with a boundary for future equipment calculations. Unique monsters rely on health, attacks, and round limits rather than a minimum player count.
- Define mobs and events in CUE under `assets/mobs/` and `assets/events/`, one definition per file, with schemas and configurable settings.

### Proposed defaults and unresolved decisions

- Proposed activity signal: successful player gameplay interactions, excluding ordinary chat and background work. The exact qualifying interactions and activity window need a documented choice before implementation.
- Balance values remain to be selected: player maximum health and fixed damage, regeneration rate, spawn ranges and category weights, round durations and limits, mob attacks, and loot quantities.
- The design proposes single-target, bounded multi-target, and all-participant monster attacks, with an ordered attack cycle. Exact shipped mob content is not yet agreed.
- Recovery after downtime and attack target announcement precision are specified as proposed defaults in the design and must be reviewed before implementation.

### Non-goals

World events and cross-group participation; equipment, character progression, skills, Defend, healing items, PvP, minimum party sizes, contribution-weighted rewards, and external bank currency rewards. No automatic archiving or implementation is part of this planning capture.

## Capabilities

### New Capabilities

- `group-encounters`: Activity-gated spawning, timed combat, public encounter controls, terminal outcomes, and individual inventory rewards.
- `encounter-definitions`: CUE mob and event definitions, settings, validation, and consistent reload behavior.
- `character-health`: Persistent group-scoped health, passive recovery, and encounter knockout behavior.

### Modified Capabilities

- `characters`: Automatically create a character on the first valid encounter attack while rejecting invalid actions without creation.
- `telegram-runtime`: Preserve encounters, round submissions, health, schedules, and reward outcomes across restart.

## Impact

Add encounter and health rules under `internal/modules/`, transactional encounter coordination under `internal/application/`, SQLite persistence/migrations, group-visible Telegram encounter views and callbacks, and an owned scheduler/delivery worker composed in `internal/app/`. Extend the existing content bundle and CUE loader/watcher to validate mobs, events, items, and locales together. Reuse inventory snapshot and ownership rules without calling privileged `/give` workflows or the bank.

The existing bank/shop/live-menu change is separate and currently marked complete; its working-tree changes must be preserved. No new third-party dependencies are proposed. Existing characters need a full-health migration/default; existing inventory snapshots and shop contracts remain compatible.
