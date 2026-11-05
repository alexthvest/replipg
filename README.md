# Replipg

Replipg is a Telegram-native cooperative RPG for an existing community. Players have persistent characters, health, inventories, consumables, and equipment. Personal menus stay inside the group as ephemeral messages, and players can explicitly share a snapshot of their character or inventory with the group.

The bot currently supports character and inventory management, equipment, healing items, and group language settings in English and Russian. Random monster encounters and step-based cooperative battles are planned.

Replipg is written in Go and stores its state in SQLite. It runs as a single bot process using Telegram long polling, so no public webhook server or separate database service is required.

## Installation

### Prerequisites

- Git.
- [Nix](https://nixos.org/download/) and [devenv](https://devenv.sh/getting-started/).
- A Telegram bot token created with [@BotFather](https://t.me/BotFather).
- A writable directory on a persistent local filesystem for the database.

The repository's devenv environment supplies Go and the development tools; a separate Go installation is not required.

### Get the source

```sh
git clone https://github.com/alexthvest/replipg.git
cd replipg
cp .env.example .env
chmod 600 .env
```

### Configure the bot

Create the storage directory, for example:

```sh
mkdir -p "$HOME/.local/share/replipg"
```

Edit `.env` and fill in these values:

```dotenv
REPLIPG_BOT_TOKEN=123456789:replace_with_your_bot_token
REPLIPG_STORAGE_DIR=/absolute/path/to/your/storage/directory
```

Set `REPLIPG_STORAGE_DIR` to the full absolute path of the directory you created. Use a literal path in `.env`, rather than `~` or `$HOME`. Keep the remaining settings from `.env.example` for a standard local setup, including the disabled development probes.

The `.env` file is ignored by Git. Keep the bot token private.

### Build and start

```sh
./scripts/run
```

This command enters the devenv environment, loads `.env`, builds `bin/replipg`, and starts the bot. Stop it with Ctrl+C.

To build without starting the bot:

```sh
./scripts/build
```

To run the resulting binary with the same environment:

```sh
devenv shell -- ./bin/replipg
```

## Using the bot

Add the bot to a Telegram group or supergroup and use:

| Command | Purpose |
| --- | --- |
| `/character` | Create or view your persistent character and health. |
| `/inventory` | Browse your items and equip or remove weapons and armor. Consumables are reserved for battle actions. |
| `/settings` | View the group's language settings. Group administrators can change the language. |

Groups default to English. Personal menus are visible only to the player who opened them. Use the Share button to publish a read-only snapshot in the group. Player interactions take place in groups and supergroups; private-chat gameplay is not supported.

Sharing publishes the displayed menu without an attribution footer or progress and success messages. Errors, cooldowns, and uncertain-delivery warnings remain personal.

## Running reliably

Run exactly one instance per bot token and storage directory. Startup removes any existing webhook so that the bot can receive updates through long polling.

The storage directory holds `replipg.db` and its SQLite WAL and SHM files. Keep these on the same persistent local filesystem; network filesystems are unsupported. The directory must exist before startup and remain available across restarts.

The bot writes structured JSON diagnostics to stderr. Lifecycle events report `starting`, `recovering`, `ready`, `draining`, and `stopped`; `ready` follows recovery and polling initialization. Failures include a safe category and correlation ID for troubleshooting. No inbound HTTP server or health port is required.

Remove any deployment checks targeting the former health endpoints and remove `REPLIPG_HEALTH_ADDRESS` from deployment configuration.

## Development

Run the repository checks, including formatting verification, linting, tests, builds, and race tests:

```sh
./scripts/check
```

For individual Go commands, use the repository environment, for example:

```sh
devenv shell -- go test ./...
```

To inspect item definitions and exercise inventory mutations in a dedicated test deployment, set `REPLIPG_DEV_INVENTORY=true` and `REPLIPG_OWNER_TELEGRAM_USER_ID` to your positive numeric Telegram user ID, then restart. The owner value must contain decimal digits only and fit a signed 64-bit integer; missing, zero, negative, whitespace-containing, or overflowing values fail before storage or Telegram startup. The default is disabled; no owner is required then. Character and runtime development flags remain independent.

Invoke these commands through Telegram's ephemeral command menu in a group or supergroup, using the configured owner's human account. Ordinary typed messages, private chats, expired interactions, bots, and messages sent as a chat are rejected. All results are personal, with no public or private-chat fallback.

| Command | Prerequisite and expected result | State changes |
| --- | --- | --- |
| `/dev_item coin` | No character or inventory required. Shows translated name, `coin`, and inert category. | None, including group language settings. |
| `/dev_item stick` | Shows stable ID, translated weapon category, and +1 attack bonus. | None. |
| `/dev_item cloth_armor` | Shows stable ID, translated armor category, and +1 defense bonus. | None. |
| `/dev_item healing_potion` | Shows stable ID, translated consumable category, and 10 HP healing. | None. |
| `/dev_item iron_sword` | Shows the iron sword and +3 attack bonus. | None. |
| `/dev_item chainmail` | Shows chainmail and +3 defense bonus. | None. |
| `/dev_item greater_healing_potion` | Shows the greater healing potion and 25 HP healing. | None. |
| `/dev_item ruby` | Shows a collectible ruby with no usable mechanics. | None. |
| `/dev_inventory_grant <item-id> <positive whole quantity>` | First open `/inventory` as the owner. Success reports that the inventory reward was granted. | Adds items only to the owner's existing character; replaying the same update cannot grant twice. |
| `/dev_inventory_heal healing_potion` | Owner has opened `/inventory`, owns a potion, is active, and has missing HP. With character probes enabled, `/dev_character_damage 20` prepares missing HP. Success reports healing. | Atomically spends one potion and heals up to 10 HP; full health or incapacitation spends nothing. |

The item probe uses the group's saved English or Russian language, defaulting to English without creating settings. Missing, extra, or malformed item arguments return localized usage; unknown IDs return localized unknown-item guidance without echoing input. No callbacks or sharing controls are created. Restarting retains inventories and receipts; disabling the flag removes all three development routes and menu entries while `/inventory` remains available to every player.

Verify access with a second account, including a group administrator: all three development commands must produce no output and no feature-state changes. Usernames and administrator roles do not grant access. Automated tests exercise the real executable against fake Telegram and temporary SQLite storage, including zero-state item inspection, owner/non-owner behavior and disabled probes. Real Telegram success and access-control observations remain pending until performed in a test group.
