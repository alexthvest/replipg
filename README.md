# RepliPG

A group-only Telegram RPG written in Go. Game state belongs to each group;
Telegram administrator status does not grant game permissions.

## Development

Copy `.env.example` to `.env` only when `.env` does not exist. Set
`REPLIPG_BOT_TOKEN` using BotFather and `REPLIPG_BOT_OWNER_ID` to the owner's
positive numeric Telegram user ID. Enter `devenv shell`, then run:

```sh
go run ./cmd/replipg
go test ./...
go vet ./...
go build ./...
golangci-lint run ./...
go test -race ./...
cue vet -c=false ./...
cue vet -c ./assets/items ./assets/locales
```

Keep `go 1.26` in `go.mod` and commit `devenv.lock`. The development shell's
installed Go patch release may be newer. Devenv imports dotenv into the local
Nix store; use it on trusted machines. Production reads environment variables.
Never commit `.env` or tokens.

| Variable | Default |
| --- | --- |
| `REPLIPG_BOT_TOKEN` | Required bot token |
| `REPLIPG_BOT_OWNER_ID` | Required positive numeric user ID |
| `REPLIPG_LOG_LEVEL` | `info` |
| `REPLIPG_DATABASE_PATH` | `./data/replipg.db` |
| `REPLIPG_ASSETS_PATH` | `./assets` |
| `REPLIPG_ASSET_DEBOUNCE` | `300ms` |
| `REPLIPG_MENU_TTL` | `15m` |
| `REPLIPG_SHUTDOWN_TIMEOUT` | `10s` |
| `REPLIPG_BANK_BOT_ID` | Empty: bank settlement disabled |
| `REPLIPG_BANK_TREASURY_ID` | `2` |

Paths resolve from the working directory. Duration values must be positive.
Asset directories must exist; the database's parent directory is created as
needed. Configuration diagnostics name the setting without echoing its value.
The application uses only `REPLIPG_` variable names.

Set `REPLIPG_LOG_LEVEL` to `debug`, `info`, `warn`, or `error` (case-insensitive)
to set the minimum log severity. Unset or empty values default to `info`;
unsupported values reject startup. Restart the application after changing it.

## Commands

The configured bot owner can run `/replipg balance` in a group or supergroup to
check the bot's shared bank treasury balance (account 2 by default). The result is
visible to the group in its selected language. This read-only command requires a
configured, reachable bank; errors or a ten-second timeout report that the balance
is unavailable. It does not affect shop orders or prove their settlement.
Balance requests run in an application-owned worker so sequential Telegram intake
can process the bank reply while the lookup is pending. Shutdown cancels and joins
this worker; failed reads record a diagnostic in the application log.

`/items` lists the active game catalog in ID order, with each ID and its localized
name. Only the configured bot owner can run it or use its buttons; game admins
cannot. Numbered rows have no blank lines between them, like inventory rows.
Press a numbered button to give yourself one of that item in the current group;
the bot confirms with callback feedback. Separate presses each give one item,
while duplicate delivery of an update cannot give twice. Pages contain up to six
items and use the current group language and live assets. The message is visible
to the group. Reopen `/items`
after menu expiry or restart. Listing items does not create a character or grant
items; pressing a numbered button creates your character if needed and grants
the item atomically. Removed item IDs cannot be granted from an old page.

Use commands in a group or supergroup. Commands may include this bot's username,
for example `/ping@your_bot`. Private chats, channels, edited messages, bot users,
and anonymous senders are ignored.

- `/ping`: show the group's localized ping response.
- Reply to a human player's message with `/admin add` or `/admin remove`:
  only the configured bot owner may appoint or revoke a game admin in that group.

The owner bypasses game admin assignments. Appointments in one group grant no
permissions in another group. Recipients do not need Telegram usernames.

## Runtime and module ownership

`cmd/replipg` handles process signals and invokes `app`. `app` explicitly wires
feature services, adapters, typed runtime handlers, and ordered ECS hooks.
`runtime` owns a bounded queue and one Donburi world; it contains no gameplay
switch. Each feature under `internal/modules` owns its rules, values, persistence
ports, and ECS components/projections. Feature modules never import adapters,
application coordinators, app, or runtime. `core` contains only shared identities
and output values. Adapters translate Telegram, SQLite, and CUE into feature
contracts. `content` holds one immutable item/locale bundle. Views and menu
sessions belong under `internal/ui` as those stages are introduced.

Register typed operations with `runtime.Bind`; the closure receives the world
only while running on its owner. Register hydration hooks with explicit earlier
dependencies. Duplicate routes/hooks and missing hook dependencies fail startup;
registries freeze before intake. After SQL commits, feature projection helpers
update ECS before success is returned. Durable IDs are independent of ECS handles.

Dependencies are pinned in `go.mod`: Donburi provides core ECS without rendering;
go-telegram/bot handles polling and API models; CUE evaluates validated assets;
modernc SQLite provides durable transactions without requiring CGO. These serve
required external boundaries; the remaining runtime uses the standard library.

Startup validates configuration, migrates SQLite, loads the full assets, hydrates
ECS, and checks bot identity before polling. Failed startup closes acquired
resources. Shutdown stops intake, drains accepted work with a fresh deadline,
joins transport workers, and closes storage. Telegram send retries never rerun
mutations. Sends have bounded deadlines and respect rate-limit retry delays.

## Two-group smoke test

1. Configure an owner and a test bot, then start it with `go run ./cmd/replipg`.
2. Add the bot to two groups with two human users and permission to send messages.
3. Run `/ping` in each group; verify private-chat `/ping` has no gameplay response.
4. As the owner in group A, reply to user B with `/admin add`. Verify a non-owner's
   appointment is denied and Telegram administrator status alone grants no role.
5. Revoke user B in group A with `/admin remove`. Appoint B in group B, restart,
   and confirm the persisted assignments remain isolated between groups.

Automated tests use fake Telegram and temporary SQLite files. A real Telegram
smoke run requires local credentials and has not been performed by the agent.

## Settings and live assets

Anyone can open `/settings`. The current owner or a game admin in that group can
choose English or Russian. Selection is saved before the same menu is edited;
callbacks never post a separate language-change notification. A revoked admin
loses access immediately. An edit failure preserves the saved language; reopen
`/settings` to see it. Menus expire after `REPLIPG_MENU_TTL` of inactivity and
become invalid after restart; reopen the command when needed.

The bot watches both `assets/items` and `assets/locales`, including schemas and
atomic editor replacements. It captures the full file set, evaluates it in a
fresh CUE context, and publishes one complete validated bundle on the runtime
owner. Invalid edits retain the last valid bundle and log a diagnostic. Prepare
coordinated edits before replacing files together; each stable save can become
its own published version. Watcher failures trigger bounded re-registration;
persistent failure stops gameplay so broken reloads are visible.

English and Russian require matching keys and named placeholders. Interface
templates use `{name}` parameters declared by the localization module, such as
`{user}` for `command.admin.updated`. The renderer inserts values as literal code
spans; assets contain no Telegram markup. Unknown parameters and malformed braces
are rejected. Item text can include literal formatting characters. Validate with
`cue vet -c ./assets/items ./assets/locales` inside devenv before saving changes.

## Characters

`/character` opens your current-group profile and creates a character on the first
valid character interaction. `/rename <name>` changes only your character in that
group. Names are trimmed at the edges and must contain 1–64 Unicode code points;
internal text and formatting characters remain literal. Blank or over-limit names
are rejected. Names and durable IDs survive restart.

An initial name uses the Telegram username without `@`, or the first and last
display names when there is no username. Telegram identity changes never overwrite
a saved name. Ordinary chat, settings, ping, admin commands, and invalid names do
not create characters. Inventory and successful grants use the same automatic
creation rules when those commands run.

## Item definitions and saved snapshots

Each top-level field in `assets/items` is a stable catalog ID. Definitions require
`name` and `description` translation keys, `rarity` (common, uncommon, rare, epic,
legendary), and explicit `mode` (`stackable` or `individual`). Coin is stackable;
stick is individual. Item names are limited to 128 code points, descriptions to
1000, and other translations to 1024. Both languages and every referenced key
must be complete. Keep `package items` and `package locales` in their directories.

Additional catalog items can be granted with `/give <item_id> <quantity>`:

| Item ID | Name | Rarity | Ownership mode |
| --- | --- | --- | --- |
| `bread` | Bread | common | stackable |
| `iron_sword` | Iron Sword | uncommon | individual |
| `silver_ring` | Silver Ring | uncommon | individual |
| `ruby` | Ruby | rare | stackable |

A continuously active ID cannot change ownership mode. Publish its removal first
if restoring that ID in a different mode is intended. An empty catalog is valid.
Each successful grant captures one complete fixed-schema snapshot with ID, mode,
rarity, and resolved English/Russian text. Definition and translation edits affect
future captures. Editing, removing, or restoring definitions never changes saved
items; their saved language variants remain viewable after restart. Identical
saved data can merge into an existing stack even after removal and restoration.

## Inventory and grants

Open `/inventory` to create your character if needed and browse its inventory in
this group. The title is bold **Inventory**, a literal ` • ` separator, and your
saved name in code. A blank line separates the title from the items. Item numbers
and names use normal text; each quantity appears in code as `(100)` or `(1)`,
including individual items. Each full page has six consecutive item rows and one row of
buttons numbered 1–6; the final page has only its remaining rows/buttons. Rarity
and descriptions appear only in inspection. Navigation and inspection are read
only; there are no equip, use, discard, delete, or transfer actions. Only the
opener can operate an inventory menu. Reopen `/inventory` after session expiry,
eviction, or restart.

As the bot owner or a current game admin, reply to a human player's message with
`/give coin 5` or `/give stick 3`. The recipient needs no username. Quantity must
be an integer from 1 through 1000. Coin stacks combine only when all saved item
data matches; stick grants create distinct durable instances. Inventory has no
slot/weight capacity. Aggregate stack quantities use signed 64-bit integers;
overflow rejects the whole grant.

Invalid syntax, recipients, catalog IDs, quantities, or permissions create no
character or inventory entries. A valid grant commits the recipient character,
snapshot, ownership, and operation result together. Response retries rerun only
Telegram delivery. Re-delivery of the same bot/update ID returns the saved outcome
without granting again, even after restart, catalog deletion, or admin revocation.
Sending a new command creates a distinct update and intentionally grants again.
Duplicate confirmation messages remain possible after a network failure; durable
mutation deduplication does not promise exactly-once Telegram delivery or intake.

## Full manual smoke procedure

Use a disposable bot and asset copy, two users, and two groups. The real Telegram
smoke test has **not** been performed by the agent; automated checks use fakes.

1. Start with valid assets and configuration. In group A, have the owner appoint
   user B by replying with `/admin add`. Verify the same role grants no privileges
   in group B and Telegram administrator status alone grants no game privileges.
2. As B, open `/settings`, choose Russian, and verify the same message changes
   language and selected button without posting a notification. Re-select Russian.
   Revoke B, then verify its old menu can no longer change language. As owner,
   choose English; ensure group B keeps its own language.
3. Open `/character`, rename with `/rename alexthvest`, and open `/inventory`.
   Check the owner title and empty state. Reject blank and over-limit renames.
4. Reply to B with `/give coin 5` and `/give coin 2`; confirm one stack of seven.
   Grant `/give stick 7`; confirm seven individual identities, a six-entry page,
   the partial final page, numeric inspection buttons, inspection, and Back.
   Try the buttons as another user and in a foreign context; no view should change.
5. Edit both coin translations and grant it again; confirm the old stack retains
   saved text and a separate new stack uses changed text. Break an asset and
   confirm the bot retains the prior valid set. Restore valid assets, remove the
   coin definition, and verify owned coins remain inspectable but new grants fail.
6. Switch languages while browsing and interact again; verify saved item text in
   that language and current interface/rarity labels. Menus expose no mutation
   actions. Wait for expiry or restart and verify old buttons require reopening.
7. Restart. Verify IDs, names, stacks, instances, admin roles, and language remain.
   In automated/fake transport, replay an already committed update after a failed
   send; verify quantities do not change. An intentionally new command grants once.
8. Stop during a grant. On restart, verify it committed completely or left no
   character/ownership/operation changes. A success must correspond to a commit.

## Backups, upgrades, and rollback

Stop the bot cleanly and confirm it has exited before taking a backup. Keep the
database, compatible asset files, binary/module versions, and configuration names
with the backup, storing credentials separately. For an online backup, use SQLite's
backup API/tooling rather than copying a live main database alone. If copying
files after shutdown, keep the database and any remaining `-wal`/`-shm` files as a
consistent set; never discard a WAL while committed data may still be in it.

Test restoring the backup to a separate path using `REPLIPG_DATABASE_PATH` before
upgrading. Migrations 001 (groups), 002 (characters), and 003 (inventory/grants)
run together in a transaction before intake. Failed migrations roll back. A
binary refuses schemas newer than it supports. To roll back, stop the new binary,
restore the verified pre-upgrade backup and compatible assets, then start the
prior binary. Do not manually lower SQLite's schema version or rewrite snapshots.
Asset rollback means restoring a valid file set; validation retains the last
accepted bundle until that set is ready.

## Adding a future feature

For a future feature such as dungeons:

1. Add its model, service, narrow persistence ports, and adjacent tests under
   `internal/modules/dungeons`. Keep rules independent of adapters, app, runtime,
   and application coordinators. Put any Donburi components and scoped projection
   helpers there. Items and characters stay independent of inventory; inventory
   consumes public item values without character internals.
2. Add SQL queries implementing that feature's port in `adapters/sqlite` and append
   a globally ordered migration. Add an application coordinator only when a
   workflow must share a transaction across features, following the grant unit of
   work's explicit transaction-bound ports. Never call independently committing
   repositories inside that transaction.
3. Add transport parsing/routes, views, and both translations. If new assets are
   needed, extend the captured content bundle and feature-owned validators so the
   whole candidate still publishes atomically.
4. Construct dependencies explicitly in `app`, register a typed `runtime.Bind`
   closure, and declare hydration dependencies/commit projections. Update ECS
   after SQL commits and return `runtime.ProjectionError` on synchronization
   failure. No central runtime gameplay branch or unsafe world access is needed.
5. Add port-fake rule tests, real-adapter contract tests, fake-transport integration,
   and import-boundary checks. `sqlite/contracts.go` checks existing port contracts
   at compile time, and `app/architecture_test.go` checks Go import metadata.

This MVP contains no dungeon or other out-of-scope feature implementation.

## Shop offer assets

`/shop` opens one item catalog for the current group and creates a missing
character. Select an item, choose Buy or Sell, then review its quantity, unit price,
and exact total before confirming. The quantity controls are `1`, `−`, `+`, and
`Max`; Max respects ownership, the 1000-unit limit, and the bank amount limit.
Individual sales continue to consume one exact instance per order. Buttons use
their default appearance, and the catalog page indicator sits in the footer.
Purchases and admin-granted items follow the
same configured sale offers. Orders survive restarts; menu sessions must be
reopened. Active menus refresh after committed changes while retaining the
current page or inspection. Background updates do not extend menu inactivity.
The inventory list retains its existing six-row formatting and owned quantities;
inspection additionally shows reserved and available quantities during a sale.
Custom locale packages must add the shop, bank availability, reservation, and
order keys required by the shipped English/Russian locale schema before upgrade.

The shipped `assets/shop/schema.cue` defines a separate `package shop`. Its empty
catalog is intentional: choose economic prices before adding offers. Custom asset
roots without `shop/` also load with no offers. Keep one `<item_id>.cue` file per
configured item and use its catalog ID as a flat key. For example, an owner-authored
`assets/shop/bread.cue` can contain the following illustrative prices:

```cue
package shop

bread: {
	mode:      "stackable"
	buyPrice:  "10"
	sellPrice: "5"
}
```

These numbers are examples, not deployed prices. Either direction can be omitted;
at least one is required. Prices are positive decimal strings of at most 100 digits,
and a sell price must be below the buy price when both exist. Buy offers require an
active item with matching ownership mode. Sell-only offers can accept saved items
whose catalog definition has been removed, including admin-granted items.

Items, offers, and English/Russian translations load and publish as one validated
bundle. An invalid edit preserves the entire previous bundle. Creating, removing,
or recreating the shop directory is watched automatically. Quotes use one bundle,
and confirmed orders retain their frozen terms and item text across later edits.
Shop assets do not change item definitions or the saved snapshot JSON format.

## Bank deployment and order recovery

The game uses the external bank's public JSON-RPC contract for invoice creation,
payment status, and treasury transfers. Configure
`REPLIPG_BANK_BOT_ID` with the independently verified positive numeric Telegram ID
of the bank bot; usernames are insufficient for authenticating replies. Set
`REPLIPG_BANK_TREASURY_ID` to the existing SYSTEM account (default `2`, maximum
`2147483647`). Empty bank ID disables settlement and recovery sends while keeping
ordinary gameplay and menus available. Invalid supplied IDs reject startup.
Outstanding orders require their original bank and treasury configuration;
changing either is rejected before any old intent can be transmitted.

Deployment prerequisites belong to the external bank and Telegram configuration:
enable private Bot-to-Bot Communication Mode for both bots, grant the game bot the
bank's `bank.transfer.public` and `bank.payment-request.public` permissions and
account binding, and ensure the bank application can deliver invoices in each
participating group. Permission, group access, and verified identity setup are not
features that replipg can enable for the independent bank. Confirm this setup with
the bank operator. Do not infer account availability or a current balance from
example protocol data. Automated checks use fake bank/Telegram responses only.

Invoices keep the original group `chatId`, payer's numeric Telegram identity,
comment, amount, and financial key. Failed group delivery never silently switches
to private delivery. Invoices have no local cancellation or expiry and can remain
pending indefinitely. A completed operation or Telegram send alone does not prove
payment. Verified transaction evidence commits before local fulfillment, so a
paid order with a database failure or numeric stack overflow remains recoverable
without requesting payment again.

Account 2 is shared across groups. Orders, characters, and ownership remain group
scoped, but groups draw payouts from the same treasury. Admin-granted items are
eligible whenever their catalog ID and saved ownership mode have a sell offer.
Admins can therefore fund sales through grants; the owner accepts this economic
policy when configuring sell prices. Items are reserved before requesting payout,
remain owned until confirmed settlement, and stay unavailable for another sale
while the payout outcome is unknown. Generic `Method unavailable` does not
identify insufficient funds, a missing player account, self-transfer, or revoked
permissions separately.

For an unresolved order, retain the upgraded database and recovery-capable binary.
Use the order ID and status from Orders and correlate them with
`shop_orders`, `financial_requests`, `financial_attempts`, and `financial_proofs`.
Detailed diagnostics are stored in the order and attempt records; recovery logs
identify affected order/request IDs for local fulfillment and send failures.
The stored transport ID, method, exact params, financial key, bank ID, and treasury
identify the original obligation. Back up the database before operator maintenance.
Restore missing external permissions/connectivity and restart with the original
configuration; scheduled unresolved work resumes with its saved identity. Status
reads use fresh transport IDs, while transfers and invoice creation retain their
original financial payload and key.

A conflict, malformed proof, or inconsistent account binding parks automatic
mutation retries for operator investigation. Compare the exact saved request with
verified bank evidence through the bank operator. If explicitly retrying delivery
through the original bot, resend the saved JSON-RPC request byte-for-byte using its
recorded destination; never invent a replacement financial key, adjust its amount,
change its payer/recipient/comment/chat, or send it as a different bot. A valid
late matching reply can still settle a parked order. There is no timeout-based
force-release action. Balance differences, approximate history matches, a later
generic error, and lack of replies cannot authorize releasing sale reservations or
granting items. An unresolved paid order needs local fulfillment repair, not a new
charge or an assumed refund.

Before rollback, stop new orders and reconcile all outstanding obligations. Do not
restore a pre-transfer database or serve gameplay from an older binary that ignores
orders and reservations. The schema version guard rejects unsupported rollback.
Retain financial keys and proofs permanently; menu expiry or process restart does
not erase them. Old menu sessions must be reopened after restart.
