# Proposal

## Why

Players should be able to experiment with materials and discover items that were never predefined. AI-generated recipes let the shared item universe grow through player crafting while keeping each discovered outcome consistent.

## What Changes

### Confirmed requirements

- Let players combine any number of distinct material types, specifying quantities. Quantities distinguish recipes.
- Use a locally running AI model for an unknown combination to invent a new item or reject the combination as uncraftable. Do not fall back to a hosted inference service.
- Share saved recipes across all groups. Repeated combinations reuse their saved outcome without another AI decision.
- Let the AI choose any supported combination of item components: `weapon`, `consumable`, and `material`, including none. Only ingredients whose saved components contain `material` are eligible; crafted materials can be crafted again.
- Consume the submitted ingredients on both successful crafting and a valid AI rejection. Persist rejections globally as well as successes.
- Adapt the shop quantity editor for crafting materials with one row of `1`, `-10`, `-1`, `+1`, `+10`, and localized Max controls, with one exact instance and no quantity controls for individual materials.

### Proposed defaults and unresolved decisions

- Proposed recipe identity: an order-independent multiset of complete immutable ingredient snapshots and exact quantities, aggregating identical snapshots. Different saved item versions are distinct ingredients; player, group, stack, and individual instance identities are excluded.
- Proposed success yield: one item per attempt, with a durable generated ID, bilingual text, supported rarity, ownership mode, and AI-selected components. Generated definitions live in the database rather than writing CUE or locale files.
- Proposed interface: a separate `/craft` menu with paged material selection, quantity editing, review, explicit confirmation, and a warning that every resolved attempt consumes ingredients, including a previously rejected recipe.
- Proposed failure policy: transport errors, refusal responses, malformed output, cancellation, and validation failures are technical failures, not gameplay rejections; they consume nothing and do not create a negative recipe.
- Proposed concurrency policy: generate outside the runtime owner and SQL transactions, persist one authoritative outcome per recipe, then revalidate available inventory and atomically fulfill each attempt. No long-lived crafting reservations initially; an inventory conflict aborts that attempt without consuming anything.
- The local inference runtime, model, endpoint/API contract, optional authentication, deadlines, output/token bounds, and request-rate/resource limits need selection before a real adapter can be completed. This proposal does not choose a runtime or model. The model service is operated separately from the bot.

These defaults are specified for review in the design and delta spec; they are not additional user-confirmed decisions.

### Non-goals

Equipment, consumption effects, combat statistics, new component types or properties, bank payments, selling generated items through existing offers, recipe rerolls or resets, a recipe encyclopedia, ingredient sourcing, automatic asset-file creation, and implementation or archiving during this planning capture.

## Capabilities

### New Capabilities

- `ai-crafting`: Material selection, global quantity-sensitive recipe discovery, AI outcome validation, permanent success/rejection reuse, generated item persistence, and atomic ingredient consumption and fulfillment.

### Modified Capabilities

None. Existing catalog grants, inventory navigation/inspection, snapshot stacking, and sale settlement retain their contracts. The new crafting capability explicitly introduces its own mutation workflow.

## Impact

Depend on the supported components and snapshot compatibility work in `add-item-components`. Its markers alone enable no actions; this separate crafting system adds material-based actions without adding equipment or consumption behavior. No edits to that change or `add-group-encounters` are part of this capture.

Add rules under `internal/modules/crafting`, coordination under `internal/application/crafting`, a local model-service adapter, durable SQLite recipes and attempts, localized crafting views and menu sessions, configuration, and an application-owned generation worker. Reuse item snapshots, inventory grant rules, runtime projection, and live menu invalidation. Preserve existing shop reservations when consuming materials. Use Go's standard library for HTTP and JSON unless the selected local runtime demonstrates a concrete dependency need. New SQLite migrations are additive; old snapshots and catalog assets are not rewritten.
