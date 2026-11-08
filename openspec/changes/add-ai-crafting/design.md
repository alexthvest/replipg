# Design

## Context

See proposal.md for the agreed gameplay rules and the proposed defaults. This design is warranted by a new local model-service boundary, durable global outcomes, and cross-package inventory mutations. Item generation must use a locally running model, with no hosted inference fallback.

The application has a single runtime owner, transaction-bound inventory writers, immutable bilingual snapshots, and durable shop reservations. Network work must run outside the owner and SQL transactions. `add-item-components` supplies typed comparable markers; crafting must consume that representation rather than introduce a separate kind enum. Its no-action requirement describes markers in isolation; crafting introduces a separate, explicitly confirmed action rather than making inspection mutate ownership.

## Goals / Non-Goals

**Goals:** Keep permanent recipe identity separate from attempt identity, preserve shop obligations, and make every accepted attempt recoverable and at most once in its inventory effects.

**Non-Goals:** A generic AI agent framework, an extensible recipe language, generated CUE files, an equipment system, changes to existing bank settlement, hosted inference, or installing/starting/managing the model service from the bot. Local runtime-specific wire details belong only in the adapter.

## Decisions

### 1. Canonical recipes use complete saved ingredient definitions

Proposed default: canonicalize every selected snapshot using the item module's existing stable encoding, aggregate identical canonical bytes with checked positive integer sums, sort by canonical bytes, and encode a versioned list of snapshot/quantity pairs. Quantities are absolute: do not divide by a common factor. Use a digest for lookup and full canonical-byte equality to avoid treating collisions as identical. Keep recipe identity encoding versioned independently of item snapshot format and preserve the canonical ingredient bytes once a recipe is created.

Exclude entry IDs, instance IDs, group, character, language selection, and selection order. An individual material contributes one unit per selected instance; equivalent instances aggregate. A quantity or any saved definition field change creates another recipe. This matches snapshot-based stacking and prevents asset reloads from silently changing a recipe's meaning. Catalog-ID-only identity was considered, but would conflate different saved materials. A future identity revision needs explicit migration rather than reinterpreting existing recipe keys.

Recipe identity and one-item yield are proposed defaults, not additional confirmed user choices. A nonempty selection with one material type is valid; there is no hard-coded limit on distinct types. Page large selections and bound individual transport messages, not the total recipe type count. Reject arithmetic overflow and unavailable quantities explicitly. Do not apply the existing grant helper's 1..1000 output-quantity limit as an ingredient-type or total recipe limit.

### 2. Separate immutable outcomes from per-player attempts

Add additive SQLite tables for:

- `crafting_recipes`: durable recipe identity, full canonical ingredients, a nullable terminal outcome, optional output snapshot reference, and immutable generation metadata. A terminal outcome is either success or uncraftable; it is never overwritten.
- `crafting_attempts`: durable attempt ID, unique confirmation token, original transport identity, group/character/user, frozen selected entry allocations, recipe reference, state, and saved terminal result. Retain completed results for idempotency.
- `crafting_jobs`: at most one active generation job per unknown recipe, with claim token, lease/deadline, and retry/recovery state. Multiple attempts wait on the same job.

Use database constraints and guarded transitions as well as application validation. Recipe digests index a collision-safe canonical comparison; uniqueness of full canonical identity establishes the winner. Concurrent late replies load the accepted winner rather than overwrite it. A generated item gets an application-assigned stable ID in a reserved generated namespace, captured together with its validated bilingual definition and components. Do not let AI choose system IDs or reference existing catalog items as a substitute for generating a result.

Recipe outcomes are global; attempt ownership and input consumption remain group-local. Persisting a valid recipe outcome separately from fulfilling attempts means a discovery survives even if its original player's materials become unavailable. This is preferable to coupling global discovery to one inventory transaction. Failed fulfillment neither invalidates the recipe nor consumes items.

### 3. Generation is asynchronous and bounded

Use a feature-owned `Generator` port accepting canonical ingredient definitions/quantities and returning a typed success or explicit uncraftable decision. One application-owned worker initially processes claimed unknown recipes sequentially, bounding concurrent provider traffic without adding a worker pool. Known recipes bypass this queue. The worker claims through typed runtime operations, performs network I/O outside the owner, and submits results back to the owner; it never accesses the Donburi world directly.

Durable claims have attempt tokens; expiry/restart permits recovery, while a stale result cannot mutate another claim or overwrite a saved outcome. Pending attempts outlive menu sessions. Shutdown stops new intake, cancels provider work, joins the worker, and leaves unfinished durable jobs recoverable. Keep response channels buffered or cancellation-aware so shutdown cannot strand a sender. Inject clock and generator dependencies for deterministic tests.

Transport/authentication errors, provider refusal, malformed output, and invalid generated data are technical failure states. Report them without consumption or a negative recipe. Mark affected active attempts technically failed so a later explicit confirmation may retry; pending work interrupted by process shutdown remains recoverable. Use finite provider request and body limits; do not automatically retry endlessly. If a valid outcome has already committed, delivery errors cannot turn it into a technical rejection or erase it.

### 4. Validate AI data before making it authoritative

The external response contract is a strict tagged union: success with a complete definition, or uncraftable without an item. Reject ambiguous variants, unknown fields, trailing content, unsupported components/properties, missing bilingual text, invalid rarity/mode, and values exceeding existing item text limits. Validate again in the application/domain boundary so a fake or alternative adapter cannot bypass rules. Every supported marker is optional and multiple markers are permitted. Generated snapshots remain detached immutable values.

Treat ingredient text as data, not instructions. Send only saved item definitions and quantities required for the decision; exclude Telegram identities, group history, credentials, and unrelated inventory. Request bounded structured output. Never execute AI output or let it mutate files, configuration, rarity vocabulary, or component schemas. AI selects descriptive content and supported classification; it does not set inventory allocations or output quantity.

Use `net/http` and `encoding/json` behind a narrow adapter targeting the configured local inference service; no new dependency is justified yet. The operator runs the local model service separately. Do not send inference requests to a hosted service or add automatic remote fallback. The local runtime/model and exact request mapping can be chosen within this fixed port and strict outcome contract without changing the feature's architecture. Do not expose a placeholder adapter as working generation. Local endpoint, optional authentication, model, deadlines, and body bounds are validated configuration, with diagnostic messages that never echo credentials. Do not require a hosted API key. Unknown discovery can be disabled; saved recipes continue working. Runtime setting changes or model upgrades never invalidate saved outcomes.

### 5. Revalidate and fulfill atomically without crafting reservations

At confirmation, validate scope, save exact input allocations and the confirmation's durable attempt, and either link a known outcome or queue unknown discovery. No SQL transaction remains open while AI works. Confirmations are single-use independently of Telegram update IDs: repeated presses with different update IDs still refer to the same attempt. A distinct craft requires a new review and token.

At fulfillment, in one transaction:

1. Load the attempt and authoritative outcome; replay a terminal attempt rather than rerun it.
2. Revalidate all selected entries, saved snapshots, material membership, owner/group, and available quantities. Account for every outstanding shop reservation. Check aggregate allocations so duplicate entry references cannot overspend.
3. Decrement or delete the exact selected inputs, leaving snapshots and other reservations intact. An entry with a sale reservation cannot be deleted while reserved units remain.
4. On success, grant one output using existing snapshot reuse and stacking rules; on uncraftable, grant nothing.
5. Record the terminal result and commit all inventory effects together. An unavailable allocation terminates the attempt without consumption; a persistence error rolls back and leaves it recoverable.

No crafting reservation table is needed initially. This avoids expanding sale-specific reservation contracts, but a sale or another craft can use materials while discovery is pending. The UI must describe pending selection honestly; fulfillment can fail for unavailable materials. Long-lived crafting reservations were considered but add shared reservation migrations and cancellation obligations that the agreed scope does not require.

After commit, reconcile the affected inventory projection from SQL and invalidate active inventory/crafting menus before reporting success. Projection or Telegram delivery failures retry synchronization/delivery using the durable result and never rerun inventory effects.

### 6. Separate crafting menus from read-only inventory menus

Proposed `/craft` flow: browse eligible owned entries in bounded pages, edit positive available quantities, accumulate any number of material types across pages, review, then confirm. Use saved item names in the group's current language and existing formatting escaping. New confirmation sessions bind user, group, message, exact selection, and token. Browsing and cancellation before confirmation have no mutation or provider side effects.

Adapt the existing shop quantity-editor interaction from `internal/app/shop.go` for crafting with a single row of plain `1`, `-10`, `-1`, `+1`, `+10`, and localized Max buttons in that order. Reset selects one, decrement buttons subtract ten or one and clamp at one, increment buttons add one or ten and clamp at the current available quantity, and Max selects that available quantity. Compute bounded increments without integer overflow. For crafting, availability excludes sale reservations; the shop's bank amount calculation and 1000-unit order limit are shop-specific and do not become crafting ingredient limits. If availability becomes zero, show unavailability and require selection revision rather than produce an invalid quantity. Individual materials select one exact instance without a quantity field/editor, matching the shop's individual-item behavior. Keep Remove and Back to materials separate from the quantity row; Remove clears only that entry and Back preserves edits and all selections across pages. Update the existing message after edits.

Extract reusable quantity-choice construction into `internal/ui/menus/quantity.go` if helpful, with caller-supplied button steps, captions, and limits. Keep the shop's existing controls, pricing, sale eligibility, callback scope, and confirmation logic unchanged. If extraction touches shop code, add focused regression coverage for its current controls and limits. Crafting must not depend on the shop application service.

The review explains ingredient destruction for both terminal outcomes. Show a known uncraftable result before confirmation and still allow the intentional destructive attempt. On confirmation, show pending or the saved outcome. Technical failure, unavailable inventory, and disabled discovery have distinct localized feedback. Successful output uses saved bilingual item text; interface and component labels remain live translations. A lost/expired menu never authorizes a new attempt, while an accepted attempt continues durably after expiry or restart.

### 7. Project structure and dependency boundaries

```text
internal/
  modules/crafting/
    recipe.go, recipe_test.go                 new: canonical identity and validation
    outcome.go, outcome_test.go               new: typed decisions and item validation
    attempt.go, attempt_test.go               new: scoped immutable intent/state rules
  application/crafting/
    ports.go                                 new: generator, repositories, unit of work
    service.go, service_test.go               new: confirm, save outcome, fulfill/replay
    recovery.go, recovery_test.go             new: claims and durable recovery
  adapters/ai/
    crafting.go, crafting_test.go             new: bounded HTTP and strict wire mapping
  adapters/sqlite/
    crafting.go, crafting_test.go             new: recipes, attempts, jobs, transactions
    migrations/<next>_crafting.sql            new: allocate next unused migration number
    inventory.go, inventory_test.go           changed: exact available-unit consumption
  config/config.go, config_test.go            changed: discovery enablement and AI settings
  modules/localization/
    crafting.go, catalog_test.go              new/changed: required crafting text keys
  ui/menus/
    crafting.go, crafting_test.go             new: selections and single-use confirmation
    quantity.go, quantity_test.go             new if needed: shared shop/craft quantity choices
  ui/views/
    crafting.go, crafting_test.go             new: localized material/review/result views
  app/
    crafting.go, crafting_test.go             new: typed routes and projection updates
    shop.go, shop_routes_test.go              changed if needed: reuse quantity choices
    crafting_worker.go, worker_shutdown_test.go new/changed: owned asynchronous generation
    app.go, modules.go                        changed: composition and shutdown wiring
  adapters/telegram/
    router.go, router_test.go                 changed: craft command/callback routing
assets/locales/
  schema.cue, en.cue, ru.cue                   changed: matching flat crafting keys
assets/items/                                no generated files or schema expansion
cmd/replipg/                                 existing thin entry point
.env.example, README.md                      changed: documented settings and crafting flow
```

The item module continues to own item snapshots/components; crafting owns recipes and outcomes; inventory owns ownership invariants. Application coordination imports those domains and uses explicit transaction ports. Adapters implement ports and never own gameplay decisions. Runtime wiring and worker ownership remain in app. Put adjacent tests beside new or changed Go files. If a reusable consumption operation is needed, add it to inventory rather than making crafting import shop coordinators. Allocate actual file splits to package cohesion during implementation.

## Risks / Trade-offs

- Permanent low-quality outcomes affect every group -> validate complete bounded definitions and test prompts with representative materials before enabling discovery; no silent rerolls after acceptance.
- Repeated known rejection intentionally destroys items -> make loss and known rejection explicit before each confirmation.
- Slow generation races with sales -> revalidate at fulfillment and abort without consumption; respect existing reservations throughout.
- In-flight jobs can produce duplicate local inference requests after crash -> unique authoritative outcomes and token checks preserve gameplay consistency, though compute usage cannot be exactly once without a runtime idempotency contract.
- Unlimited recipe combinations increase local compute/storage demands -> bounded execution, one generation worker, enable/disable control, configurable inference rate limits, and no automatic unbounded retries. Any future player quota is a separate product decision, not an implicit ingredient-type cap.
- New component types or canonical encoding changes could split identities -> preserve versioned bytes and add compatibility fixtures before evolution.

## Migration Plan

1. Integrate and verify `add-item-components` first, including immutable saved membership and legacy compatibility. Re-read its current implementation because it is being developed separately.
2. Add new crafting tables and constraints using the next migration number available at implementation time. Preserve existing snapshots, shop orders, reservations, and canonical bytes.
3. Ship locales, binary, and documented configuration together. Leave new discovery disabled until a real local runtime adapter and settings are validated. Existing material-less legacy snapshots stay ineligible without backfill.
4. Run the selected model service locally and configure its endpoint and model. Enable discovery after deterministic fake-generator tests, local HTTP-contract tests, reservation race/restart tests, and operational local-model configuration. Automated checks require neither real model weights nor real credentials. If the service stops, unknown discovery fails without consuming ingredients; saved recipes remain available and no hosted fallback occurs.
5. To disable discovery, stop new provider requests but retain outcomes and known-recipe crafting. Before binary rollback, stop intake and settle or preserve pending attempts; retain the schema-version guard. An older binary that ignores new persisted crafting state is not a supported rollback. Restore only a coordinated backup with the explicitly accepted loss of newer gameplay, rather than deleting recipe/attempt tables in place.

## Open Questions

Local inference runtime/model, API mapping, optional authentication, and concrete timeout, response-size, rate, and resource settings remain deployment choices within the generator contract. Selecting them is an explicit implementation task before enabling real discovery; using a locally running model is already confirmed. If the selected local service cannot support the required structured outcome contract, revisit the adapter design rather than silently weaken validation. Proposed recipe identity, one-item yield, UI, and failure/concurrency defaults are reviewable in proposal.md and the delta spec.
