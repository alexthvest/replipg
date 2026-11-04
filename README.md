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
| `/inventory` | Browse your items, manage equipment, and use consumables. |
| `/settings` | View the group's language settings. Group administrators can change the language. |

Groups default to English. Personal menus are visible only to the player who opened them. Use the Share button to publish a read-only snapshot in the group. Player interactions take place in groups and supergroups; private-chat gameplay is not supported.

## Running reliably

Run exactly one instance per bot token and storage directory. Startup removes any existing webhook so that the bot can receive updates through long polling.

The storage directory holds `replipg.db` and its SQLite WAL and SHM files. Keep these on the same persistent local filesystem; network filesystems are unsupported. The directory must exist before startup and remain available across restarts.

The health server listens on `127.0.0.1:8080` by default. Check readiness from the same machine:

```sh
curl --fail http://127.0.0.1:8080/readyz
```

A ready instance returns HTTP 200 with a JSON lifecycle status. `/livez` reports liveness, and `/statusz` provides runtime status. These endpoints have no authentication; keep them private.

## Development

Run the repository checks, including formatting verification, linting, tests, builds, and race tests:

```sh
./scripts/check
```

For individual Go commands, use the repository environment, for example:

```sh
devenv shell -- go test ./...
```
