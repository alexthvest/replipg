# Roadmap

## Overview

Replipg is a Telegram-native cooperative RPG for an existing community. The MVP gives every Telegram user a generic persistent character, an inventory with consumables and equipment, and step-based group battles against randomly spawned monsters. Personal interfaces stay inside the group by using Telegram ephemeral commands and messages; encounters remain public and use inline buttons.

The implementation will be a Go modular monolith running as one long-polling container. Domain packages remain separate, while SQLite provides durable state on a local persistent volume. Encounter scheduling, callback processing, and Telegram delivery must survive restarts through database timestamps, idempotent state transitions, and an outbox/reconciliation mechanism.

## Approach Decision

- **Chosen**: Modular monolith with SQLite and Telegram long polling. Use `github.com/go-telegram/bot`, a pinned no-CGO `modernc.org/sqlite` stack, and the repository's devenv environment.
- **Why**: This is the smallest operational model for a single-container MVP while preserving domain boundaries that can later support guilds, progression, more event types, and world events.
- **Rejected alternatives**: PostgreSQL was deferred because it adds a separate service before the MVP needs horizontal write scaling. Event-driven services were rejected because their deployment and consistency overhead would delay validation of the game loop.

## Scope

- **In**: Telegram group onboarding; generic character creation; persistent health and temporary incapacitation; inventory; consumables; weapons and armor; configuration-seeded content; common and special monster spawning; activity-weighted common spawn probability; step-based multiplayer battles; attack, item use, and escape actions; player action cooldowns; single- and multi-target monster attacks; contribution-weighted random loot; restart recovery.
- **Out**: World events, guilds, PvP, trading, crafting, currencies and shops, classes, skills, character XP and levels, admin content editing, private-chat flows, multiple bot replicas, and event types other than monsters.

## Constraints

- Use Go through the repository's devenv configuration; pin the environment with `devenv.lock` and use Go 1.26.10 or newer before implementation.
- Use `github.com/go-telegram/bot` with a release supporting Telegram Bot API 10.3 ephemeral messages.
- Use a patched `modernc.org/sqlite` release and pin its matching `modernc.org/libc` version.
- Run exactly one long-polling bot replica and remove any configured webhook on startup.
- Store the SQLite database, WAL, and SHM files together on one local persistent volume; network filesystems are unsupported for WAL.
- Treat SQLite as a single-writer store: keep transactions short, configure bounded busy handling, and make callback-driven mutations atomic and idempotent.
- Persist scheduled work and Telegram delivery intent; do not rely on in-process timers as the source of truth.
- The bot must receive ordinary group messages for activity scoring, requiring appropriate administrator access or disabled Bot Privacy Mode.
- Activity scoring prioritizes unique active users and adds capped per-user message volume over a rolling window to resist spam.
- Content and tuning values are version-controlled configuration or seed data in the MVP.
- Do not log the Telegram bot token, personal message content, or other sensitive values. Activity tracking stores only the minimum metadata needed for scoring.

## Boundary Strategy

- **Why this split**: Runtime and persistence establish stable adapters first. Character and inventory state can then develop independently. Encounter orchestration owns when and what appears, while battle resolution owns deterministic state transitions and outcomes. This keeps Telegram delivery, spawning policy, and combat rules from collapsing into one package.
- **Shared seams to watch**: Telegram user/chat identifiers; transaction boundaries between combat, character health, and inventory grants; content identifiers shared by monster and item seed data; event lifecycle versus battle lifecycle; callback versioning; and restart reconciliation after Telegram API failures.

## Specs (dependency order)

- [ ] telegram-runtime -- Establish configuration, long polling, ephemeral interaction support, SQLite persistence, migrations, durable scheduling, and Telegram delivery reconciliation. Dependencies: none
- [ ] character-system -- Create and persist one generic character per Telegram user, including health and temporary incapacitation. Dependencies: telegram-runtime
- [ ] inventory-equipment -- Manage consumables, weapons, armor, equipped slots, item effects, and atomic item grants. Dependencies: telegram-runtime, character-system
- [ ] monster-encounters -- Track privacy-preserving chat activity and schedule configurable common and special monster encounters in group chats. Dependencies: telegram-runtime, character-system
- [ ] battle-system -- Run step-based cooperative monster battles and resolve actions, cooldowns, monster responses, escape, defeat, recovery, and contribution-weighted loot. Dependencies: character-system, inventory-equipment, monster-encounters
