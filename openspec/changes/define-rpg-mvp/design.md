# Design

## Context

See `proposal.md` for motivation and the seven capability specs for behavior. The repository currently contains a `cmd/replipg/main.go` greeting, one Go module declaring Go 1.26.10, a devenv environment, and CUE packages for items and English/Russian locales. There is no application state, database, transport adapter, or test suite yet. Item definitions have name/description translation keys and rarity but lack ownership mode. Locale constraints currently permit arbitrary string keys; runtime validation must enforce required keys and parity.

## Goals / Non-Goals

**Goals:** Build a modular monolith in one deployable process and Go module; give each feature ownership of its rules, state, and contracts; isolate external integrations behind adapters; preserve group isolation and atomic transactions; allow future features to register use cases and systems without expanding a central gameplay package.

**Non-Goals:** Distributed workers, multiple ECS worlds, graphical rendering, a generic plugin framework, and an ORM. Telegram delivery and SQLite commits cannot form one atomic transaction; durable grant deduplication protects ownership rather than promising exactly-once message delivery.

## Decisions

### 1. Project structure and dependency direction

Organize by feature within `internal/modules/`, with application coordination and external adapters outside the feature packages. Planned paths below are additions unless marked existing. Each listed implementation file has adjacent tests where it owns behavior; representative test paths are shown. Create packages when their implementation stage needs them, rather than empty scaffolding for future mechanics.

```text
cmd/replipg/
  main.go                     existing; signals, composition root invocation, exit
internal/
  app/
    app.go                    startup, shutdown, concrete dependency wiring
    modules.go                explicit construction and registration order
    app_test.go               full integration with fake transport/temp SQLite
  config/
    config.go                 environment parsing and documented defaults
    config_test.go
  core/
    identity.go               typed group/user/durable IDs and actor scope
    response.go               transport-neutral text spans, styles and menu values
  runtime/
    engine.go                 bounded queue, single world owner, lifecycle
    registry.go               named command/callback handler registration
    systems.go                ordered hydration and commit projection hooks
    engine_test.go
    registry_test.go
  modules/
    groups/
      model.go                group settings and game admin identities
      service.go              authorization, appoint/revoke, language persistence
      ports.go                narrowly scoped persistence contracts
      ecs.go                  group state and scoped projection operations
      service_test.go
    localization/
      catalog.go              locale values, required keys, templates
      service.go              language resolution and settings use cases
      ports.go                consumer-owned group settings access contract
      service_test.go
    characters/
      model.go                character identity and name validation
      service.go              profile, rename, automatic creation use cases
      ports.go                group-scoped character persistence contract
      ecs.go                  character components, hydration and projections
      service_test.go
    items/
      definition.go           item ID, mode, rarity and definition values
      snapshot.go             resolved immutable snapshot and equality rules
      catalog.go              catalog validation and mode compatibility
      snapshot_test.go
    inventory/
      model.go                entries, stacks, quantities and instances
      service.go              browsing, inspection and ownership rules
      ports.go                inventory reads and transaction-scoped writes
      ecs.go                  inventory components and scoped projections
      service_test.go
  application/
    grants/
      service.go              coordinate permissions, character and ownership
      ports.go                unit of work and operation-result contracts
      service_test.go         cross-feature commit/replay/rollback behavior
  content/
    bundle.go                 immutable items/locales bundle, single publication
    bundle_test.go
  ui/
    menus/
      sessions.go             expiry, binding and opaque action references
      sessions_test.go
    views/
      settings.go             localized settings view and selected state
      character.go            character view
      inventory.go            bounded pages and inspection views
      views_test.go
  adapters/
    telegram/
      bot.go                  polling, send/edit/ack, API error handling
      router.go               command names and callback dispatch wiring
      groups.go               /admin translation to application requests
      settings.go             /settings and language callbacks
      characters.go           /character and /rename
      inventory.go            /give, /inventory and inventory callbacks
      router_test.go
    sqlite/
      database.go             connection lifecycle and configuration
      migrations.go           ordered embedded migration runner
      groups.go               implements group persistence port
      characters.go           implements character persistence port
      inventory.go            implements inventory persistence port
      grants.go               transaction-bound repositories and deduplication
      migrations/
        001_groups.sql
        002_characters.sql
        003_inventory.sql
      database_test.go
      grants_test.go
    cueassets/
      load.go                 captured CUE files -> typed candidate bundle
      watch.go                directory events, debounce and validation errors
      load_test.go
      watch_test.go
assets/
  items/
    schema.cue                existing; add explicit ownership mode
    coin.cue                  existing; declare stackable mode
    stick.cue                 existing; declare individual mode
  locales/
    schema.cue                existing; require supported locales and key parity
    en.cue                    existing; add complete interface messages
    ru.cue                    existing; matching Russian messages
go.mod                        existing; preserve declared Go version
go.sum                        added when dependencies are installed
.env.example                  existing; document placeholders and defaults
README.md                     command, configuration, asset, recovery guidance
```

Feature modules own public value types and narrow application APIs. Keep each feature as a cohesive Go package initially; introduce subpackages only for a proven boundary. Core contains only shared identity/scope and output values, never gameplay rules, repositories, or generic utility collections. Module services receive dependencies explicitly and never resolve them from a global container.

Dependency rules:

```text
cmd --> app --> adapters and application services
                   |                |
                   v                v
              feature ports <-- feature modules --> core

app --> runtime --> core and Donburi
app --> content --> items and localization value types
adapters/cueassets --> content + item/locale validation APIs
adapters/telegram --> runtime submission + ui + application APIs
ui/views --> feature read models + localization
```

Feature modules never import adapters, app, application coordinators, or runtime. They may define components and projection functions using core Donburi, but only the runtime owner invokes world operations. Items and characters are independent of inventory. Inventory can use items' public snapshot/value types, but does not import character internals. Localization declares a small group-settings interface which groups' public service implements; groups does not import localization. Cross-feature workflows live in application services rather than forming cycles between features. SQLite adapters import feature contracts and grant application contracts to implement them; domain/application code never imports database/sql or Telegram/CUE models.

Content is an immutable aggregate of public item and locale values. Modules receive the relevant captured values as use-case input; they never read separate mutable global catalog providers. Runtime publication is wired by app so all feature validators complete before swapping one bundle. UI views receive read models and the captured locale catalog, keeping localized strings out of business-state persistence except where item snapshots require them.

Tests use small fakes for feature-owned ports, real temporary SQLite databases for adapter transaction guarantees, and fake transport for application integration. This permits a future transport or storage adapter without rewriting feature rules. Alternatives considered: keep all rules in one game package (easy initially, but every feature expands it); create full domain/application/infrastructure trees for every small feature (excessive empty packages). Feature packages with explicit ports provide useful isolation now.

### 1a. Registration and adding a feature

Use explicit construction in `app/modules.go`, with dependencies passed through constructors. Register named commands/callback namespaces and ordered hydration/projector hooks through small runtime APIs. Reject duplicate route names, missing required dependencies, and ambiguous ordering at startup; freeze registries before intake. Do not register through init functions, reflection, dependency injection frameworks, or dynamic shared-library plugins. Typed feature inputs are captured by registered handler closures; the runtime queues executable work with actor scope and a result channel rather than maintaining a universal request struct or a growing switch over gameplay commands. No handler closure may access the ECS world before it executes on the owner.

The runtime owns queueing, world access, bundle publication, and lifecycle only. Feature modules implement rules and ECS projection helpers. Application services invoke those helpers after commit on the owner. Hook order is explicit in composition: groups before characters, characters before inventory; content validates and publishes as a complete unit. Future simulation systems can be registered with declared order when gameplay needs them; no idle simulation loop is introduced for the current MVP.

To add a future feature such as dungeons: add its feature package with model/service/ports and any components; add an application coordinator only if it spans other features; implement its SQL adapter and append a globally ordered migration if persistence is needed; add a Telegram route/view and locale keys; wire these in app with explicit dependencies and registered handlers/hooks. Extend the complete content bundle and validators if the feature introduces an asset type. Do not create dungeon packages or change current behavior in this MVP.

All table access stays with the feature's adapter; another feature calls the public API or an application coordinator rather than reading foreign tables directly. The grant unit of work below is the explicit cross-feature transaction boundary. This is a modular monolith with compile-time composition, not independently deployed services.

### 2. Dependencies and runtime configuration

Use the requested [Donburi repository](https://github.com/yohamta0/donburi-ecs), whose [module declaration](https://raw.githubusercontent.com/yohamta0/donburi-ecs/main/go.mod) is `github.com/yohamta/donburi`. Use core world/component/query APIs without Ebitengine or rendering packages. Use [go-telegram/bot](https://github.com/go-telegram/bot) for Telegram models, command handlers, long polling, and API calls.

Add `cuelang.org/go` to evaluate CUE directly using [cue/load](https://pkg.go.dev/cuelang.org/go/cue/load), rather than shelling out to a development CLI. Choose [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) with standard `database/sql` for a driver that does not require CGO. Choose [fsnotify](https://github.com/fsnotify/fsnotify) for directory notifications and atomic-save support rather than continuous full-file polling. These dependencies serve required persistence and live assets; use the standard library for logging, JSON, hashing, IDs, contexts, signals, and migrations. Pin compatible non-retracted module versions during implementation, record them in go.mod/go.sum, and run go mod tidy without changing the declared Go version.

Configuration: required `TELEGRAM_BOT_TOKEN` and positive `BOT_OWNER_ID`; defaults `DATABASE_PATH=./data/replipg.db`, `ASSETS_PATH=./assets`, `ASSET_DEBOUNCE=300ms`, `MENU_TTL=15m`, `SHUTDOWN_TIMEOUT=10s`. Validate paths and positive durations at startup. Resolve relative paths against the working directory. Create the database parent directory only when needed; never print tokens or complete environment values. Devenv loads local dotenv; production reads environment variables directly.

Long polling suits a single local process without public hosting. Webhooks remain a future transport choice. Accept `message` and `callback_query` updates, ignore edited/channel/private updates, reject anonymous or bot actors, and recognize commands addressed to this bot while ignoring commands addressed to other bots. Do not replay edited grant messages as new commands.

### 3. One goroutine owns the world

```text
Telegram handlers --> bounded request queue --> game owner --> result
                                               |               |
Asset watcher --> immutable candidate ----------+               v
                                               |       Telegram send/edit
                                               v
                                         SQLite transaction
```

Use one world shared across groups, mutated only by the runtime owner's goroutine. Each queued envelope carries bot/update IDs, typed group/actor scope, a registered operation closure with feature-specific input, and a result channel. Reply identity and normalized arguments belong to their feature request values. All lookup keys include group ID; never treat an ECS entity handle as a persistent or externally supplied identity. Feature-owned hydration hooks reconstruct durable-ID indexes and entries; groups load before characters and inventory.

A bounded queue of 128 requests applies context-aware backpressure; never launch unbounded goroutines. Each request has a buffered one-result channel so a cancelled caller cannot block the owner. A stopped/cancelled request not yet started has no side effects. Accepted transactions use a bounded operation context; caller cancellation never permits publishing uncommitted changes. Serial execution defines ordering for grant/revoke/settings races. Database transactions remain necessary for durability and duplicate safety.

No simulation tick is needed for this MVP. The owner selects requests, asset publications, and a session cleanup timer. Telegram networking and asset loading run separately so API calls and CUE evaluation do not block the world. Each render captures a consistent group language, immutable item bundle, and saved item snapshots. UI views produce literal text with semantic bold/code spans; the Telegram adapter converts spans into message entities using UTF-16 offsets, rather than parsing user-authored Markdown or HTML.

SQL is authoritative. Compute proposed mutations without altering ECS, commit SQL, then apply committed data to the world before producing success. If post-commit world synchronization cannot complete, stop gameplay and restart/reconstruct from storage; never keep serving a divergent world. A lost Telegram response does not roll back committed state.

Alternative: mutate the world in concurrent Telegram handlers or use a mutex spanning network calls. Both obscure ordering and ownership; the single owner fits the expected small-group scale.

### 4. SQLite schema, transactions, and snapshot identity

Use one database connection (`SetMaxOpenConns(1)`), foreign keys enabled, WAL mode, a finite busy timeout, and durable commit settings. Apply numbered embedded SQL migrations transactionally and refuse an unknown newer schema. The SQLite adapter owns connection management and one global migration sequence; each feature's adapter owns its queries and mappings. Avoid ORM and migration-framework dependencies. Serialize all state-changing requests through the owner, while enforcing invariants in SQL too.

The grants application service consumes a purpose-specific unit-of-work interface declared in `application/grants/ports.go`. Its callback receives transaction-bound character, inventory, and operation-result ports using public feature values. The SQLite implementation binds those ports to the same sql.Tx; callbacks must not open another connection or call an independently committing feature service. The grant coordinator handles replay lookup, validates scope/permissions/catalog input, and invokes character and inventory rules through their public APIs before transaction writes. It returns a committed change set for feature-owned ECS projectors. A standalone character rename or group setting update uses that feature's own atomic persistence method. No generic repository or untyped service-locator transaction API is needed.

Tables:

- `groups(group_id PRIMARY KEY, language)` with English/Russian constraint and default English.
- `game_admins(group_id, user_id, PRIMARY KEY(group_id,user_id))`, referencing groups.
- `characters(id PRIMARY KEY, group_id, user_id, name, UNIQUE(group_id,user_id), UNIQUE(group_id,id))`.
- `item_snapshots(id PRIMARY KEY, catalog_id, canonical_json UNIQUE, digest)` containing a schema-versioned snapshot with mode, rarity, and resolved English/Russian names and descriptions.
- `inventory_entries(id PRIMARY KEY, group_id, character_id, snapshot_id, mode, quantity)` with a composite foreign key `(group_id,character_id)` to characters. Individual entries have quantity one; a partial unique index on `(group_id,character_id,snapshot_id)` applies only to stackable entries.
- `grant_operations(bot_id, update_id, group_id, result_json, PRIMARY KEY(bot_id,update_id))` records successful committed grant results, including recipient and snapshot identity.

Use random durable IDs from crypto/rand, independent of ECS handles. Canonical JSON comes from a fixed-field struct, never an unordered arbitrary map; include snapshot format version. Use a digest for lookup acceleration, then compare full canonical bytes for equality. Hash equality alone is insufficient. Item text references resolve into actual strings in both languages before saving. Exact equivalent data can share a snapshot row across grants, including after deletion/restoration; existing entries are never reattached to live assets.

Before a grant, validate syntax, recipient, actor privileges, active definition, and limits. For a known `(bot_id,update_id)` return the saved result without checking the current catalog again; authorization and active-definition validation concern the original operation, and a replay must not create a new mutation. In the transaction, check the operation key again, ensure the group and missing recipient character, insert/reuse the snapshot, add a stack or individual entries, and record the result. Overflow checks occur before addition. Roll back on any error. Concurrent retries are serialized and also guarded by primary keys. Invalid commands create neither characters nor ownership; they need not be durably recorded.

New item IDs use the CUE top-level field name as the stable catalog ID. Add explicit `mode: "stackable" | "individual"` to definitions; do not infer or default it. Compare candidates against the current catalog to reject in-place mode changes for continuously active IDs. A successfully published removal clears that continuity, allowing restoration with another mode. Ownership is unaffected.

### 5. Asset loading and publication

Watch the two asset directories, including schema files, for write/create/remove/rename events. Watch directories rather than individual files so editor atomic replacements are detected. Debounce a burst for 300ms, then capture the complete `.cue` file sets from both directories. Verify the file set and contents did not change during capture; retry unstable reads. Evaluate the captured bytes using a CUE loader overlay in a fresh CUE context so evaluation never rereads changing disk content. Keep project packages `items` and `locales`; add CUE module metadata only if the chosen supported loader requires it, documenting its purpose.

Validate concrete item and locale values, the five rarity identities, explicit modes, locale key parity, every required message key, item reference resolution in both languages, and platform-safe length limits. Permit all item definitions to be removed while retaining a concrete empty catalog. Permit unused translation keys so removing a definition does not require simultaneous cleanup. Interface templates use a small documented named-placeholder format; validate required placeholder parity between languages and insert values as literal text with renderer-owned formatting spans. Reject unknown format tokens at load time; asset text cannot introduce style markup.

The cueassets adapter produces a typed candidate content bundle containing decoded item data and translations, with no live CUE values shared across goroutines. Item/locale validators belong to their feature modules; the adapter handles file capture and CUE evaluation. On the owner, the content publication handler invokes the item module's active-mode compatibility check and publishes the entire bundle by replacing one reference. No consumer observes partial item/locale publication. Reload errors retain the active bundle and are logged with paths and diagnostics. Watcher errors trigger a full rescan and watch re-registration with bounded retries; persistent failure is logged and surfaces as a failed watcher rather than silent loss of reloads. Stop and join the watcher on shutdown.

Readiness always requires a valid initial complete set, even before item granting is implemented. During the localization stage validate the current item schema and its translation references; extend that same validator with explicit modes in the item stage. Existing owned snapshots do not pass through the reload path. Locale edits update newly rendered interface labels and rarity labels; saved item text stays immutable. Already sent messages are updated on their next interaction rather than proactively edited.

Alternative: independently swap item and locale pointers, or store only live translation keys in owned items. Both violate consistent publication or snapshot immutability.

### 6. Commands and bounded menus

Choose `/character` to view a profile and `/rename <name>` to rename it. Use reply-based `/admin add` and `/admin remove` for owner-managed game roles. Keep `/give <item_id> <quantity>`, `/inventory`, and `/settings`. `/ping` is the first-stage runtime smoke command. Publish usage in README and both locales. Settings/admin/ping and invalid commands do not create characters.

Validate renamed character names as 1-64 Unicode code points after trimming surrounding whitespace; preserve internal text. Initial usernames omit a decorative `@`; display-name fallback combines available first and last names. Accept the actual Telegram identity for automatic initial naming without requiring a rename. Grant quantity is 1-1000 per request for either mode; aggregate stack quantities use positive signed 64-bit integers with checked arithmetic. This request bound limits transaction work without imposing inventory capacity.

Use in-memory menus with 15-minute idle expiry and a bounded session pool (1000 entries, evict expired then least-recently-used). Tokens use crypto/rand; callback data contains only an opaque token and short action reference within Telegram's 64-byte limit. Server-side session state contains menu kind, group ID, message ID, opener for inventory menus, and permitted entry/page references. Bind sessions to the sent message ID before enabling controls. Reopening after expiry or restart creates a new session. TTL and pool eviction affect menus, never owned items.

Settings menus can be opened by any user; any currently authorized group admin or owner can change language through them. Inventory menus require opener identity on every action. Check message/group/session/action binding before permission and ownership checks. Reject unavailable callback messages safely. Always acknowledge validly received group callbacks, including denied or expired ones, without editing an unrelated menu. Error feedback for settings uses callback acknowledgement rather than a new chat message.

Persist a language change before editing that same settings message with localized text and visible selected language. Re-selecting the current language acknowledges without unnecessary edits. If commit fails, retain the old selection and answer with failure. If the edit fails after commit, retain the saved language and log the failure; reopening shows it. Old settings menus reread saved language on every action. Serialize renders/edits per message and discard older pending render revisions so concurrent selections cannot leave a stale selection visible after a newer successful edit. Never send a separate language-change notification.

All menu headings use bold spans; displayed field values, names, quantities, IDs, and page numbers use inline-code spans. Labels and descriptive prose remain ordinary text. Button captions are plain text because inline keyboards do not render message entities. Build message entities separately from literal text so backticks and other characters in names or asset text cannot inject formatting. Bold and code spans must not overlap.

The inventory title is bold `Inventory`, a literal ` • ` separator, and the saved character name in inline code; localize the heading with the group language. For the character alexthvest, the rendered form is **Inventory** • `alexthvest`. A renamed character uses its saved name, consistent with existing inventory ownership views.

Inventory pagination uses stable durable entry ordering and exactly six entries per full page. Only the final page can contain fewer entries; an empty inventory has its empty state. List each entry on one line with no blank lines between entries, showing its page-local number, item name, and quantity where applicable as code spans. Do not show rarity, descriptions, or ownership mode in list rows. Inspection retains rarity, mode, description, and quantity/instance details with bold headings and code-formatted field values.

Use one inspection button row with plain numeric captions `1` through `6`, matching page-local positions. Partial pages have only the buttons for their actual entries; never pad with fake items or disabled placeholders. Put previous/next controls on their own row and show page numbers as code values in the message. Bind page-local actions to durable entry references in the menu session so a stale callback cannot silently inspect a different item. Include inspect/back controls and keep new entries reachable by navigation/reopening.

Budget rendered text and entity ranges using UTF-16 offsets within Telegram limits while preserving six-item pages. Bound asset names to 128 code points and descriptions to 1000 code points, with a 1024-code-point maximum for other translated labels/templates; validate rendered output too. Use bounded label/name previews if needed rather than reducing full-page item counts, and show complete saved item text in inspection. Flatten embedded line breaks only in list previews so each entry remains one line, without altering saved snapshots. Layouts must accommodate initial Telegram names as well as validated renames. No mutation actions appear on item menus.

### 7. Lifecycle, errors, and verification

Startup order: parse configuration; open/migrate SQLite; load and validate assets; reconstruct world; initialize bot identity; start owner and watcher; begin Telegram polling. Resource acquisition unwinds in reverse order on failure. Use contextual wrapped errors and structured slog fields containing operation IDs and group IDs, never tokens. Normalize failures into localized user errors at the transport boundary. Network sends have deadlines and bounded retries respecting Telegram retry-after; a send retry never reruns the game mutation. Durable operation results enable replay without duplicate ownership, but duplicate confirmation messages remain possible.

Shutdown: stop Telegram intake, stop the watcher, close request admission, and drain accepted work under a fresh 10-second shutdown context. Requests not started by the deadline are cancelled; in-flight transactions finish or roll back. Keep response sending alive for completed work within that deadline, join owned goroutines, then close storage. Do not reuse the already cancelled signal context for all draining work.

Use standard Go testing, temporary on-disk SQLite databases, fake Telegram transport, deterministic clock/token injection, and explicit queue coordination instead of sleeps. Feature tests cover rules through narrow ports; adapter tests cover SQL/CUE/Telegram contracts; app tests exercise complete workflows. Verify the allowed import directions using go list -deps -json inspection or an architecture test based on Go's import metadata, and test registry rejection of duplicate handlers and invalid hook dependencies. Tests must cover each capability's failure scenarios, cross-group references, grant replay after commit-before-send failure, rejected asset publication, and snapshot survival after deletion/restart. Run concurrency tests with the race detector inside devenv. A manual two-user/two-group smoke procedure supplements automated tests; no real token is needed for automated validation.

## Risks / Trade-offs

- [More explicit packages and ports require wiring] --> Limit interfaces to actual consumers, register features explicitly in app, and create no speculative future feature packages.
- [Cross-feature calls can bypass transaction or world ownership] --> Keep atomic grants in one coordinator with transaction-bound ports; invoke feature projectors only on the runtime owner after commit; test rollback across adapters.
- [One world owner limits throughput] --> Keep CUE evaluation and Telegram calls outside it, bound requests and transactions, and accept serialized gameplay at the intended scale.
- [Commit succeeds before a process crash or Telegram failure] --> SQL remains authoritative, startup reconstructs the world, and replay uses durable grant outcomes.
- [Several file saves form an unintended intermediate valid asset set] --> Validate full captured sets and debounce; separate saves are separate potential publications. For coordinated changes, prepare files before replacing them together. No multi-file filesystem transaction is promised.
- [Item schema now requires explicit mode] --> Update all shipped definitions with the schema in the same implementation step and fail startup clearly for incompatible external files.
- [Public menus expose item data to the group] --> This is the required group interface; restrict controls and never expose another inventory through forged callbacks.
- [SQLite schema upgrades constrain binary rollback] --> Refuse newer schemas and restore a verified pre-upgrade backup when rolling back; never silently reinterpret saved snapshots.
- [Polling does not provide a durable inbound queue] --> At-most-once inventory mutation applies to delivered/replayed update IDs; crash-proof recovery of every fetched-but-unprocessed Telegram update is outside this MVP's contract. Do not claim exactly-once intake.

## Migration Plan

Implement in the five agreed stages, running focused checks at each checkpoint. Migration 001 adds groups/admins; 002 adds characters; 003 adds snapshot/ownership/operation tables. No legacy database exists to migrate. The shipped CUE files need explicit modes and complete interface translations before the final application starts. Preserve existing translation keys for coin, stick, ping, and rarity labels.

Before deploying a later schema version, stop the process and take a consistent SQLite backup, including WAL state through SQLite backup facilities or a clean shutdown/checkpoint. Apply migrations before intake. A failure leaves no partial migration. Rollback means stopping the new binary and restoring the backup with the prior binary and compatible asset files. Asset reload rollback is automatic validation retention or restoring a previously valid file set; it never rewrites owned snapshots.

The five stages live as ordered sections of this change's tasks, not separate changes requiring intermediate archive. Archive this change only after implementation and at the user's request.
