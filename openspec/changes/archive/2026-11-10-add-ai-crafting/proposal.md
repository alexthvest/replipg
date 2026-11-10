# Proposal

## Why

Players should be able to experiment with materials and discover items that were never predefined. AI-generated recipes let the shared item universe grow through player crafting while keeping each discovered result consistent. Creative combinations can produce unusual, funny, or surreal hybrids; they are not restricted to conventional RPG or fantasy items.

## What Changes

### Confirmed requirements

- Let players combine any number of distinct material types, specifying quantities. Quantities distinguish recipes.
- For progressive generation, treat quantities as a separate budget per material that influences feasible component or finished-item identity, coverage, and construction. Insufficient material should inspire a smaller result or intermediate rather than a gameplay rejection. Illustrative costs such as two leather for small handwear versus five for trousers are not universal hard-coded recipes.
- Use a locally running AI model for an unknown combination to invent a new item. Do not fall back to hosted inference.
- Encourage imaginative hybrid results without an RPG theme restriction. For example, Fumo plus Neco-Arc can produce Fumo-Neco-Arc.
- Prefer emergent uses, mechanisms, behaviors, or identities arising from ingredient interactions over literal attachments, decorative combinations, or joined names. Keep the resulting names and descriptions concise and grounded.
- Adopt the reviewed English A prompt: raw substances progress through processed materials, components, and subassemblies toward complete objects, with meaningful changes at each step and no redundant intermediate when the supplied functions are already sufficient.
- Disable gameplay crafting failures: every resolved craft creates an item. Incongruous ingredients are inspiration, never a reason to declare a combination uncraftable.
- Share saved recipes across all groups. Repeated combinations reuse their saved generated item without another AI decision.
- Let the AI choose any supported combination of item components: `weapon`, `consumable`, and `material`, including none. Only ingredients whose saved components contain `material` are eligible; crafted materials can be crafted again.
- Consume the submitted ingredients only together with the successful item grant. Technical errors, invalid output, cancellation, and unavailable inventory preserve ownership.
- Adapt the shop quantity editor for crafting materials with one row of `1`, `-10`, `-1`, `+1`, `+10`, and localized Max controls, with one exact instance and no quantity controls for individual materials.
- Retire earlier cached uncraftable recipes so a new confirmation can discover an item for those combinations. Preserve historical attempts, existing inventory, and successful recipes.

### Implementation defaults

- Recipe identity is an order-independent multiset of complete immutable ingredient snapshots and exact quantities, aggregating identical snapshots. Different saved item versions are distinct ingredients; player, group, stack, and individual instance identities are excluded.
- Yield one item per resolved attempt, with a durable generated ID, bilingual text, supported rarity, ownership mode, and AI-selected components. Generated definitions live in the database rather than writing CUE or locale files.
- Use a separate `/craft` menu with paged material selection, quantity editing, review, explicit confirmation, and notice that successful crafting consumes the selected materials to create one item.
- Transport errors, refusal responses, malformed or unsuccessful output, cancellation, and validation failures are technical failures. They consume nothing and never create a reusable negative recipe. A later explicit confirmation may retry.
- Generate outside the runtime owner and SQL transactions, persist one authoritative successful definition per recipe, then revalidate available inventory and atomically fulfill each attempt. No long-lived crafting reservations initially; an inventory conflict aborts that attempt without consuming anything.
- Use separately operated local Ollama with `qwen3:4b` as the overridable development default. Validated endpoint/model, optional local authentication, deadlines, response/token bounds, and request-rate limits are documented in the design and README.

The success-only, unrestricted creative generation requirements supersede the earlier plan for permanent destructive uncraftable outcomes. They do not remove validation, technical failures, or inventory-conflict protection.

### Non-goals

Equipment, consumption effects, combat statistics, new component types or properties, bank payments, selling generated items through existing offers, rerolling successful recipes, a recipe encyclopedia, ingredient sourcing, automatic asset-file creation, and archiving this change without a separate request.

## Capabilities

### New Capabilities

- `ai-crafting`: Material selection, global quantity-sensitive creative item discovery, generated definition validation, permanent successful recipe reuse, generated item persistence, and atomic ingredient consumption and item fulfillment.

### Modified Capabilities

None. Existing catalog grants, inventory navigation/inspection, snapshot stacking, and sale settlement retain their contracts. The new crafting capability introduces its own mutation workflow.

## Impact

Depend on the supported components and snapshot compatibility work in `add-item-components`. Its markers alone enable no actions; this separate crafting system adds material-based actions without adding equipment or consumption behavior. No edits to that change or `add-group-encounters` are part of this scope.

Add rules under `internal/modules/crafting`, coordination under `internal/application/crafting`, a local model-service adapter, durable SQLite recipes and attempts, localized crafting views and menu sessions, configuration, and an application-owned generation worker. Reuse item snapshots, inventory grant rules, runtime projection, and live menu invalidation. Preserve existing shop reservations when consuming materials. Use Go's standard library for HTTP and JSON. Additive migrations preserve old snapshots and catalog assets; the success-only compatibility migration retires cached negative recipe outcomes without undoing historical attempts or touching successful discoveries.
