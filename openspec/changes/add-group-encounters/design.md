# Design

## Context

See `proposal.md` for motivation and agreed scope. The runtime owns one Donburi world and serializes typed operations. SQLite is authoritative; projections update after commit and failures stop the owner through `runtime.ProjectionError`. Content bundles publish atomically. Characters currently contain identity and name, while inventory snapshots preserve bilingual item data.

The separate `add-bank-shop-and-live-menus` change has completed tasks but is not archived. Its current code includes background bank work, transaction-bound inventory ports, and ordered menu delivery. Build against that working tree without editing its planning artifacts or treating the bank as a combat dependency. Encounter controls must outlive ephemeral menu sessions and recover after restart.

This is a cross-cutting change with new persisted state, scheduling, and delivery, so a design artifact is required.

## Goals / Non-Goals

**Goals:** Keep combat deterministic under a supplied clock and random source; keep SQL and projections consistent; use small feature-owned ports; isolate future equipment damage and reward policy changes; bound background work independently of group count.

**Non-goals:** A general workflow engine, per-group goroutines, a generic distributed event bus, cross-group transactions, or an equipment model. World events will be a later coordinating feature linking local encounters, not an executable category in this release.

## Decisions

### Package and project structure

Planned paths below describe implementation work; this planning capture creates none of them. Each new Go source area gets adjacent `*_test.go` coverage.

```text
assets/
  mobs/
    schema.cue                 new: package mobs and attack constraints
    slime.cue                  new: one common mob definition
    goblin.cue                 new: another common mob definition
    <unique-mob-id>.cue        new: one selected unique mob
  events/
    schema.cue                 new: package events and reference constraints
    settings.cue               new: shared spawn and initial combat settings
    <event-id>.cue             new: one event per file, referencing one mob
  locales/{schema,en,ru}.cue   changed: matching flat mob/event/UI keys
internal/
  modules/
    encounters/
      definition.go            new: mob/event/settings values and validation
      encounter.go             new: round state and targeting rules
      rewards.go               new: initial participant eligibility policy
      ecs.go                   new: scoped encounter projections
    health/
      health.go                new: elapsed recovery and bounded damage values
      service.go               new: narrow health persistence port
      ecs.go                   new: character health projection
  application/encounters/
    ports.go                   new: transaction-bound feature ports
    service.go                 new: submission, resolution, and reward orchestration
    scheduling.go              new: activity and persisted spawn scheduling
    delivery.go                new: durable announcement intent and outcomes
  adapters/
    cueassets/{load,watch}.go   changed: mobs/events and directory lifecycle
    sqlite/
      migrations/005_encounters.sql  new, if 004_shop remains latest
      encounters.go            new: encounter/schedule/delivery queries and UoW
      health.go                new: health persistence
      contracts.go             changed: compile-time port checks
    telegram/router.go         changed: encounter callback namespace
  content/bundle.go            changed: immutable mob/event catalogs and settings
  app/
    encounters.go              new: typed owner routes and projection updates
    encounter_worker.go        new: owned scheduler and delivery lifecycle
    {app,modules,characters}.go changed: composition, activity, health profile
  ui/views/encounter.go        new: public round/result rendering
  ui/views/character.go        changed: health display
  modules/localization/        changed: validated encounter templates
cmd/replipg/main.go            unchanged unless thin startup wiring is required
README.md                     changed: assets, mechanics, recovery and operations
```

Domain modules depend on core values and feature-owned values, never adapters, runtime, app, or application coordinators. `application/encounters` coordinates health, characters, encounters, and inventory through explicit transaction-bound ports. SQLite implements those ports. App owns composition and post-commit projection updates. Use standard library timers and existing dependencies only.

### CUE contracts and captured content

Keep stable IDs as top-level definition keys, following the item catalog pattern. One `<id>.cue` file defines one mob or event; `schema.cue` contains constraints and `events/settings.cue` shared settings rather than extra definitions. Loader validation checks file/ID correspondence and cross-catalog references; CUE alone cannot establish filesystem layout constraints.

Proposed mob fields: name translation key, maximum health, and an ordered list of attacks with ID, announcement translation key, damage, target type (`single`, `multiple`, `all`), and target count only for `multiple`. Proposed event fields: mob ID, category (`common`, `unique`), positive selection weight, round duration, maximum rounds, and guaranteed per-player item quantities. The initial reward policy uses this fixed loot list; probabilistic loot can be added later.

Shared settings include spawn minimum/maximum delay, activity window, initial player maximum health, fixed player damage, and regeneration amount/interval. All bounds and arithmetic need overflow-safe validation. Unique content should have a lower selection weight and require several baseline players to win before the round limit. Tune values in assets, with deterministic balance examples; no minimum party-size gate.

Extend the bundle rather than loading an independent mutable catalog. Both languages must supply mob names, announcements, and UI templates. Watch creation, deletion, and recreation of the new directories. For backward-compatible custom asset roots, both catalogs absent means spawning disabled; a partial catalog or event referencing absent content is invalid. The shipped assets include both schemas and initial definitions.

Capture event settings, mob health/attacks, bilingual announcements, fixed player damage, and reward item snapshots at spawn. Existing encounters never consult mutable catalog definitions for combat or rewards. Future equipment will replace the damage calculation boundary; do not infer weapon behavior from existing item names or change the saved item snapshot format now.

### Persistent state and health

Use separate health records keyed by group and character rather than enlarging character identity with encounter rules. Persist current/max health and last regeneration accounting time. Initialize existing and new characters at the chosen full-health baseline. An encounter participant stores an irreversible encounter-local knockout flag separately from current health.

Materialize regeneration lazily using whole elapsed intervals, retaining partial-interval credit, capping at maximum, and preventing backward timestamps. Apply recovery before health reads and before combat damage/submission checks. Recovery also counts elapsed offline time and can restore health during an encounter, but never clears its knockout flag. At full health discard accumulated recovery credit so it cannot immediately heal future damage. Initially persist each character's max health and recovery parameters; later balance reloads affect newly initialized records rather than silently rewriting existing health contracts. Changing established recovery parameters needs an explicit migration or later policy change.

Persist group activity and next spawn time; encounter ID/group, captured definitions, phase, round/revision, announced attack, bound message, deadline, and terminal result; participants and knockout flags; round submissions; processed mutation outcomes; reward ledger; and delivery intents. Index due timestamps and enforce one active encounter per group with a database constraint. Submission uniqueness is `(encounter, round, character)`; round resolution uniqueness is `(encounter, round)`; reward uniqueness is `(encounter, character, reward-entry)`. Also deduplicate Telegram mutation updates by bot/update identity.

### Scheduling and default policy choices

The following are proposed design defaults, not additional user-confirmed preferences:

- Successful human character/profile/inventory interactions, renames, shop interactions, and accepted encounter attacks refresh recent gameplay activity. Ordinary chat, invalid actions, settings/admin actions, background delivery, and scheduler work do not.
- The initial due time is first qualifying activity plus a sampled delay. Subsequent due times are terminal time plus a sampled delay. Inactive groups retain eligibility state but never spawn until a qualifying interaction occurs. Resuming activity after an overdue time can start one encounter, never a backlog.
- Select events by positive weights. Sample a persisted encounter random seed and use deterministic draws over sorted eligible participant IDs for target selection. Use an ordered mob attack cycle to keep authoring and tests simple; the first round announces the first attack. Only targeting type/count is promised in advance; exact targets are selected at resolution.
- On downtime, apply elapsed regeneration and resolve the one stored overdue open round once. If the encounter continues, announce a new round and give it its full duration; do not simulate unannounced rounds.

These choices provide complete behavior for the draft specs; they can be revised during review. Numeric balance values and the exact unique mob are deferred authoring choices, not architectural blockers.

### Round state and transaction ordering

```text
scheduled --> announcing --> open round --> resolving
                  ^                           |
                  +----- next round ----------+
                                              +--> victory
                                              +--> defeat
                                              +--> escaped
```

At spawn, persist the encounter and first announcement intent, reserving the group's active slot. A round becomes open only after announcement delivery and owner-side binding; calculate its deadline then. This prevents network delay from consuming the action window. Previously open rounds retain their stored deadline after restart.

Submission validates group, message binding, encounter/round revision, deadline, existing knockout status, and health before creating a missing character. Commit character/health creation, participation, one attack, deduplication result, and activity together. The first accepted attack establishes participation immediately; it does not deal damage until the deadline. A later-round first attack is allowed while the encounter remains active.

At the deadline, in one transaction:

1. Check the expected round revision and absence of a completed resolution.
2. If the first round has no participants, record escape.
3. Apply the accepted attack sum using checked arithmetic; clamp monster health at zero.
4. On zero monster health, record victory and grant each contributing character the captured reward list, including knocked-out contributors.
5. Otherwise apply elapsed recovery, select eligible targets for the announced attack, apply damage, and permanently mark new knockouts.
6. If all participants are knocked out, record defeat immediately. Otherwise, on the final round record escape; if neither applies, prepare the next announcement.
7. Save resolution/reward results, the next spawn schedule for terminal encounters, and delivery intent, then commit.

Do not call the administrator grant service or independently committing repositories inside the transaction. Reuse inventory insertion/snapshot semantics through a unit of work, preserving shop reservations and ownership rules. Inventory overflow or any recipient failure rolls back the entire resolution; log contextual errors and retry the same round. Deterministic target draws and captured reward terms prevent retry rerolls. Project committed changes on the owner and invalidate affected inventory/profile views; projection failure is fatal until rehydration.

### Scheduler ownership, delivery, and cancellation

Add one bounded background worker, not one timer per encounter. It polls indexed due work in small batches through typed owner operations and performs Telegram I/O outside the ECS owner. A separate system-operation envelope identifies scheduler work without fabricating participant actions or refreshing activity. If current runtime binding requires a valid actor scope, follow the existing worker pattern while retaining the explicit system input and never using that actor for combat eligibility.

Persist announcement/update intent before sending, then report delivery outcomes through the owner. Use a dedicated encounter callback namespace containing opaque encounter/round references; validate the originating group/message and bound revision on every action. Do not rely on ephemeral personal menu tokens. Serialize delivery by encounter revision and recheck jobs before sending so delayed results cannot overwrite newer rounds. Reuse delivery retry/backoff concepts from live menus without tying durable combat state to menu TTLs.

Initial sends can be duplicated after an ambiguous transport failure; only the recorded message binding is actionable. Retrying an edit should render the same desired revision. Expired or deleted messages need a replacement announcement and rebinding, rejecting old callbacks. A permanently unavailable group must not block the runtime or other groups: keep its delivery retry delayed and prohibit new encounters while its active slot remains occupied. Do not silently simulate combat for an undeliverable new round.

All worker contexts have app-owned cancellation and completion channels. Stop claiming jobs on shutdown, cancel bounded network work, await the worker, then drain accepted owner mutations before closing SQLite/transport. Startup hydrates health and encounters after characters and inventory. Return contextual persistence errors without exposing credentials; make Telegram delivery failures retryable rather than rolling back committed combat.

## Risks / Trade-offs

- Equal loot permits low-contribution participation: deliberate initial policy, isolated in reward calculation for later revision.
- Attack-only combat offers limited tactics: announced single/multi/all attacks establish a foundation without adding unrequested actions.
- A single runtime owner can be delayed by large rewards: cap event loot sizes and process a bounded number of due operations; keep network I/O outside the owner.
- Undeliverable announcements can hold a group's encounter: isolate retries per group and expose diagnostics; administrative cancellation can be a later feature.
- Passive recovery and combat clocks can drift: inject a clock, store accounting times, cap recovery, and test restart/backward-clock boundaries.
- Existing custom assets may lack new directories: treat both absent as disabled spawning while rejecting broken partial definitions.
- Schema and asset changes cross the completed shop change: use the next actual migration number and preserve its inventory reservations and delivery ordering.

## Migration Plan

1. Back up the stopped database and matching assets using the repository's documented procedure. Confirm the separate shop migration is present before allocating the next number.
2. Add encounter/health/schedule/delivery tables and constraints through the existing transactional migration runner. Initialize existing characters at full health without altering character IDs or inventory snapshots.
3. Ship valid bilingual mob/event assets and settings with the binary; validate all assets before accepting gameplay. Hydrate committed state before starting worker/intake.
4. Verify old character, inventory, grants, shop, and menu behavior alongside new encounters in automated integration tests; perform the documented disposable-group smoke test if credentials are available.
5. Roll back by stopping the binary and restoring the verified pre-upgrade database and compatible assets/binary. Do not downgrade `user_version` or reinterpret captured snapshots manually.

## Open Questions

- Which initial numeric settings and loot quantities provide the desired encounter frequency and difficulty? Select explicit values and document example common/unique outcomes before adding shipped assets.
- Which unique mob and localized announcement text should ship first? Its choice does not alter the schemas or combat flow.
