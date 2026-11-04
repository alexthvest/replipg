# Replipg

A single-instance Telegram group runtime in Go. Gameplay features register their
own behavior; the runtime provides update routing, durable work, and operations.

Install [devenv](https://devenv.sh/getting-started/), then use the repository's
pinned Go and lint tools. Every helper below enters `devenv shell` automatically.
No host Go toolchain or HTTP client dependency is required.

```sh
cp .env.example .env
chmod 600 .env
```

Edit the ignored `.env` locally: set `REPLIPG_BOT_TOKEN` to your test bot's
credential and `REPLIPG_STORAGE_DIR` to an absolute, clean directory path on a
persistent local filesystem. Create that directory before startup:

```sh
devenv shell
mkdir -p -- "$REPLIPG_STORAGE_DIR"
chmod 700 -- "$REPLIPG_STORAGE_DIR"
exit
./scripts/build
./scripts/run
```

The build writes `bin/replipg` (ignored). `scripts/run` compiles and runs the
command with the current configuration. The example deliberately leaves the
credential and storage path empty: running it unchanged exits unsuccessfully
and names the missing setting before database or network activity. Never put a
token in command arguments, shared files, logs, or screenshots. Keep local
environment files and machine paths out of shared devenv configuration.

Run exactly **one active replica** per deployment. Coordination between
instances and horizontal scaling are unsupported; `REPLIPG_DEPLOYMENT_MODE`
must remain `single`. Stop the old process before starting a replacement.
Update intake uses outbound long polling and needs no public inbound endpoint.
Startup removes conflicting webhook delivery while preserving pending updates.

`REPLIPG_STORAGE_FILESYSTEM=local` is an operator assertion, not automatic mount
detection. The directory must support local SQLite WAL semantics; do not use
NFS, SMB, or other network filesystems. Keep `replipg.db`, `replipg.db-wal`, and
`replipg.db-shm` together on that volume. Do not remove or copy individual
sidecars while the process is active. For a simple consistent backup, stop the
runtime and snapshot the entire storage directory before restarting it.
Compatible migrations run during startup before readiness. Migration failure
blocks startup; an older binary refuses a database with a newer schema.
Rollback requires a compatible binary/database snapshot pair or a forward fix.

Health endpoints are unauthenticated. Keep the example loopback bind, or use a
private interface protected by infrastructure access control. In another terminal:

```sh
./scripts/probe livez
./scripts/probe readyz
./scripts/probe statusz
```

The probe defaults to `readyz` and prints only the endpoint and HTTP status,
with a nonzero exit on failure. It uses `REPLIPG_HEALTH_ADDRESS` from `.env`.
`/livez` indicates process liveness; `/readyz` returns 200 only after storage,
migrations, recovery, and polling initialization succeed, and 503 during unsafe
states. `/statusz` exposes safe aggregate durable-work counts and deployment
limitations. Probes and diagnostics exclude tokens, message bodies, private
response content, and raw Telegram request/response bodies. Send SIGINT
(Ctrl-C) or SIGTERM to stop: readiness drops, new work stops, running work drains
up to `REPLIPG_SHUTDOWN_DEADLINE`, then remaining work is cancelled and preserved
for recovery on restart.

To receive ordinary messages in a Telegram test group, disable the bot's Privacy
Mode through BotFather (and re-add the bot if Telegram requires it), or grant
equivalent group access. Confirm ordinary messages reach the bot; receiving
commands alone does not establish this. Public and user-specific ephemeral
responses use their registered transport capability. Unsupported or failed
ephemeral delivery has no private-chat or public-message fallback. This setup
does not itself register gameplay or demonstration commands.

Run the automated local quality gates without Telegram credentials:

```sh
./scripts/check
```

This checks formatting and runs `go test ./...`, `go vet ./...`,
`golangci-lint run`, and `go test -race ./...` inside devenv. Tests use fake
Telegram boundaries and must not contact real Telegram. All required runtime
environment names and non-secret timeout examples are listed in `.env.example`;
the claim lease must be at least the handler timeout, and maximum retry delay
must be at least the initial delay.

For an opt-in development transport smoke check, use a dedicated test bot and
an isolated test group. Set `REPLIPG_DEV_SMOKE=true` in the ignored `.env`, then
run `./scripts/run`. The flag is optional and defaults to false; normal startup
registers no demonstration behavior. Enabling it installs these probes:

- Send `/dev_smoke` in the test group: the bot sends a fixed public response
  with a **Restricted reply** button.
- Press the button: Telegram receives an invisible callback acknowledgement,
  followed by a response restricted to the user who pressed it in that group.
- Send `/dev_smoke_ephemeral` as an incoming ephemeral group message when the
  client supports it: the bot replies using that ephemeral message's identity.
  An ordinary command with this name lacks the required authorization and
  produces no message.
- Send an ordinary group message: the bot sends a fixed receipt without echoing
  its content or assigning gameplay outcomes. Privacy Mode must allow intake.

Unsupported or rejected ephemeral delivery records a safe failure and sends no
public or private-chat fallback. The callback and incoming ephemeral probes use
15-second authorization windows. Diagnostics never print the probe content,
incoming message bodies, or raw API responses. Stop the bot and unset the flag
(or set it to `false`) after testing. The deterministic fake-Telegram transport
check requires no bot credentials and sends no real Telegram messages:

```sh
devenv shell -- go test ./internal/devsmoke ./internal/config ./cmd/replipg
```

Run the completed runtime smoke gate without Telegram credentials:

```sh
./scripts/smoke
```

This builds and boots the production executable with an isolated temporary
SQLite directory and the development probes enabled. A test-owned HTTPS proxy
routes only `api.telegram.org:443` to a local TLS fake; no real Telegram
connection is made. Proxy and certificate trust settings apply only to the
child process. The gate checks live/not-ready startup, ready/status probes with
both durable workers active, public and restricted replies, invisible callback
acknowledgement, ordinary-message intake, rejected restricted delivery without
fallback, SIGTERM shutdown, closed health listener, and the durable cursor after
storage reopens. It prints only test results and safe failure descriptions.

## Characters

Normal startup installs the ephemeral `/character` command. Select it from the
Telegram group command menu to create your character and inspect health, base
attack/defense, and recovery state. Typed ordinary commands have no incoming
restricted-reply authorization and produce no response. Only the originating
human actor can inspect their character; bots and anonymous chat senders are
ignored. One character follows the same user across groups and process restarts.
Authorization lasts 15 seconds from the original message timestamp, so an old
queued command may expire without a reply. A failed reply does not reset committed
character state; select the command again.

Starting tuning is version-controlled in `internal/character/defaults.json`:
100 health, 10 attack, 5 defense, five-minute incapacitation, and 25% recovery.
Settings are strictly validated before runtime construction. Existing characters
keep stored base attributes, and each defeat retains its original recovery
amount and deadline across configuration changes. Recovery occurs on the next
inspection or gameplay operation at/after that deadline; there is no passive
regeneration or background recovery notification.

The executable replaces the complete default-language `all_group_chats` command
menu at startup. Managed groups also receive translated chat-scoped menus as
described under Group language. Other scopes are not deleted automatically.
Startup remains not-ready if global publication fails and preserves queued updates.

Character schema migration 0002 is additive. Stop the runtime before backing up
the entire local SQLite storage directory. A version-1 binary refuses a version-2
database: roll forward, or restore a compatible stopped whole-directory backup
before running the older binary. Restore external command-menu settings deliberately
when rolling back to a binary that does not manage them.

For a dedicated test deployment, set `REPLIPG_DEV_CHARACTER=true` in the ignored
`.env` and restart. This flag accepts exactly `true` or `false`, defaults to false
when absent, and is independent of `REPLIPG_DEV_SMOKE`. It exposes ephemeral
`/dev_character_damage <whole HP>` and `/dev_character_defeat` commands for the
requesting actor only, with five-second recovery. Select the menu command and
supply the damage amount in clients supporting ephemeral command arguments.
After defeat, inspect before and after five seconds: health recovers to 25/100
on observation. Stop/restart during incapacitation to verify the same deadline.
Set the flag back to `false` and restart to remove these routes and replace the
managed menu; already-recorded defeats retain their original timing and amount.

Reproduce automated verification without real Telegram traffic:

```sh
./scripts/check
./scripts/smoke
devenv shell -- go test -count=1 -run 'TestCharacter' ./cmd/replipg
```

See `.kiro/specs/character-system/manual-verification.md` for live milestone steps
and the separate evidence record.

## Inventory and equipment

Select the ephemeral `/inventory` command from the Telegram group menu to open
personal numbered inventory navigation. First access creates a missing character
and grants its starter supplies once across all groups and restarts. Ordinary
typed commands lack incoming ephemeral authorization and do not initialize an
inventory. Only the requesting actor can navigate their own group-bound controls;
controls expire after five minutes. Reopen `/inventory` to refresh an old view.
Item selection, pagination, Back, Equip and Unequip edit the original personal
inventory message and its buttons. Expired or stale controls replace that view
with guidance to reopen `/inventory` and remove its buttons.
Personal delivery has no public-message or private-chat fallback.

Version-controlled tuning in `internal/inventory/items.json` grants ten ordinary
Coins, one Stick (+1 attack), one Cloth armor (+1 defense), and three Healing
potions (10 HP each). Equipment starts unequipped. Equipped copies remain part
of owned quantities; each slot contributes its additive bonus without changing
character base attributes. Coin is an inert item with no currency or spending
behavior. Healing-item details are read-only and state that use requires battle
authorization; production inventory has no standalone Use button.

Startup reconciles catalog mechanics before Telegram setup and update admission.
Saved identities retain their category and numeric mechanics: keep old definitions
and introduce a new identity for new mechanics. Display text may change; starter
changes affect only characters who have not yet received their kit. Preparation
never grants starter supplies.

Inventory migration 0003 is additive. Stop the single active replica before
backing up the whole SQLite storage directory. An older version-2 binary rejects
the version-3 schema. Roll forward with compatible catalog content, or restore
a compatible stopped whole-directory backup and its matching binary. An
incompatible catalog blocks startup even if schema migration has already applied.
Restore external Telegram command menus deliberately when reverting binaries.

Reproduce automated checks with `./scripts/check`, `./scripts/smoke`, and
`devenv shell -- go test -count=1 ./internal/inventory/...`. Milestone test-group
steps and actual live evidence status are kept in
`.kiro/specs/inventory-equipment/manual-verification.md`.

For a dedicated test deployment, set `REPLIPG_DEV_INVENTORY=true` in the ignored
local `.env` and restart. The flag accepts exactly `true` or `false`, defaults to
false when absent, and is independent of character probes and devsmoke. Open
`/inventory` first, then select ephemeral `/dev_inventory_grant <item-id> <quantity>`
or `/dev_inventory_heal <item-id>` for the requesting character. Healing keeps
normal ownership, full-health and incapacitation checks. Enable the separate
character damage/defeat probes to prepare health tests. Set the inventory flag
back to false and restart to remove its routes and replace the complete managed
menu; stale development commands then have no registered handler.

## Group language

Select ephemeral `/settings` in a group to inspect its persistent language.
English is the default; a currently verified group administrator or owner can
select English or Russian for the entire group. Ordinary members see a read-only
view. Give the bot administrator status for reliable membership verification;
without sufficient privileges, settings explains that verification is unavailable
and rejects changes. Ordinary translated gameplay remains available. Settings
inspection and changes do not create characters or grant starter supplies.
Typed ordinary `/settings` lacks restricted authorization and produces no reply.
Language controls are personal, group bound, and expire after five minutes.

The saved language applies to subsequent character, inventory, item and enabled
development interactions. Already sent messages retain their text until an
accepted interaction would normally edit them. Player names, item identities,
health, quantities, equipment and starter entitlements keep their existing rules.
The historical `cloth_armor` identity is displayed as leather armor in both
languages. Translations are embedded flat English/Russian catalogs; incomplete
resources or invalid saved selections block admission instead of falling back.

Language changes commit independently of command-menu publication and personal
confirmation delivery. Settings distinguishes saved selection from pending menu
synchronization. A supervised worker retries pending publication across restarts,
refreshes menus periodically, and reconciles enabled development commands.

The executable owns default-language `all_group_chats` commands and the default,
`en` and `ru` chat-scoped menus of managed groups. Other language variants,
administrator scopes and per-member scopes remain operator owned. Remove
competing unmanaged overrides deliberately if clients display other descriptions.
Command names remain stable in both languages.

Localization migration 0004 adds group settings and controls without changing
existing gameplay tables. Stop the one active replica and back up the entire
local SQLite directory before upgrading. Schema-3 binaries refuse schema 4;
rollback requires a matching stopped whole-directory backup and older binary,
plus deliberate restoration of external Telegram menus.

Run `./scripts/check`, `./scripts/smoke`, and
`devenv shell -- go test -count=1 ./internal/localization ./internal/settings`.
See `.kiro/specs/localization/manual-verification.md` for Telegram test-group
steps and the separate live evidence status.

## Sharing personal views

Character summaries, inventory pages and item details have a **Share** button.
Opening, navigating or managing equipment stays personal. Settings and development
responses have no Share button. Failed personal delivery has no public fallback.

Press Share to post an ordinary, read-only copy in the same group, attributed to
your numeric user identity even without a username. The copy preserves the body
and language you were viewing; it has no buttons. Later gameplay, browsing or
language changes do not update it. Each displayed view has a 30-second cooldown
from its last confirmed successful publication. Pressing again during pending
work or cooldown gives personal guidance. After the cooldown, deliberately press
Share again to create another copy; all earlier public copies remain.

Share follows the source menu's existing usability rules. Inventory controls
retain their five-minute deadline; reopen `/inventory` when needed. Sharing adds
no separate expiry to character views. A definite publication failure permits
another deliberate attempt. An uncertain outcome means the copy may already be
visible: check the group before trying again when guidance permits it. Uncertain
sends are not automatically reposted; exactly-once delivery is not guaranteed.
A failed personal confirmation does not trigger another public post.

Accepted delivery content is retained temporarily for delivery and recovery.
Resolved public and personal-feedback delivery content is cleaned independently
after 24 hours from its recorded outcome; unresolved work remains until safely
resolved. This cleanup does not delete public Telegram messages. Usable views
retain authorization metadata, and accepted actions retain replay protection;
there is no permanent archive of private view bodies.

Sharing migration 0005 is additive. Stop the single active replica and back up
the entire local SQLite storage directory before upgrading. Schema-4 binaries
refuse schema 5; restore a compatible stopped whole-directory backup with its
matching binary, or roll forward. Sharing does not change command-menu scopes.

Reproduce the automated character journey without Telegram credentials:

```sh
devenv shell -- go test ./cmd/replipg -run TestSharingCharacterMilestone -count=1
```

See [sharing verification](.kiro/specs/menu-sharing/manual-verification.md) for
isolated test-group steps, expected observations and separate automated/live
evidence. Live sharing checkpoints remain unperformed until recorded there.
