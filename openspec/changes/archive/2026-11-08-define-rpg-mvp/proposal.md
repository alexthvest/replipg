# Proposal

## Why

The project needs a testable contract for its first playable Telegram RPG features. Define character, item, inventory, and localization behavior before implementation begins.

## What Changes

- Define group-only gameplay with independent characters, inventories, language settings, and game admin permissions per group.
- Define automatic character creation, editable character names, and persistent profiles.
- Define a shared item catalog, immutable grant-time snapshots, and persistent inventory browsing and inspection.
- Add owner-only `/items` browsing of the live catalog with numbered names, IDs, and buttons granting one selected item to the owner.
- Define reply-based admin grants with atomic persistence and duplicate-delivery protection.
- Define English/Russian localization and `/settings` language buttons that edit the existing menu without a separate notification message.
- Define automatic, validated, atomic reloads of item and locale assets.

### Confirmed scope and non-goals

The MVP covers characters, item definitions, item grants, inventories, group administration, and localization. It excludes private-chat gameplay, mini apps, combat, dungeons, mobs, bosses, events, guilds, classes, levels, equipment actions, item use, discarding, transfers, and monetization.

Owned items preserve the definition and translated item text captured when granted. Editing, deleting, or restoring an asset definition never changes existing items. Inventories have no gameplay capacity limit.

### Planning details

`/give <item_id> <quantity>`, `/inventory`, and `/settings` are specified interfaces. The accompanying design resolves character/admin commands, name and quantity limits, menu expiry, and asset debounce defaults; tasks organize implementation into the five stages below. These details preserve the approved behavior contracts.

## Capabilities

### New Capabilities

- `telegram-runtime`: Group-only interactions, group isolation, reliable startup and shutdown, and durable state recovery.
- `group-administration`: Configured bot owner and separately managed game admin permissions per group.
- `localization`: English/Russian text, persistent group language settings, button-driven settings menus, and automatic asset reloads.
- `characters`: One automatically created character per user per group with persistent editable names and profiles.
- `item-definitions`: Shared validated item catalog, rarity and stacking modes, immutable snapshots, and automatic asset reloads.
- `inventories`: Persistent unlimited ownership, snapshot-based stacking, individual item instances, browsing, and protected inspection menus.
- `item-grants`: Authorized reply-based grants with automatic recipient creation, atomicity, and durable duplicate protection.

### Modified Capabilities

None; this repository has no existing capability specs.

## Impact

This change captures planning artifacts only. Future implementation uses Go at the version declared in `go.mod`, Donburi ECS from https://github.com/yohamta0/donburi-ecs, https://github.com/go-telegram/bot for Telegram integration, SQLite for durable state, and CUE files in `assets/items/` and `assets/locales/`. Use a modular monolith in one Go module with feature-owned models, services and ports; keep Telegram, SQLite and CUE loading in adapters. Explicit composition registers feature handlers and ECS hooks with a small runtime. A dedicated grant application service coordinates the atomic cross-feature transaction. Keep one shared ECS world with explicit group ownership and persistent identities across reconstruction. Development uses the repository's devenv environment.

Preserve the five implementation stages: `telegram-runtime` (including group administration), `localization`, `character-system`, `item-system`, then `inventory-system` (including grants). These stages describe intended sequencing, not CLI-enforced dependencies. The design and tasks cover the SQLite driver, file-watching library, concurrency boundaries, package layout, and verification. Resolve and pin compatible dependency versions during implementation. This change does not archive specs or authorize implementation by itself.
