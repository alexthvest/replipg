# Inventory System Design

## Approval and History

The user approved this split, requirements, design, tasks and implementation on 2026-11-04 by requesting “Implement the plan”. The original design remains in design-history.md as historical context. This design governs the extracted boundary; existing functional requirements and completed task evidence remain valid history, not fresh split verification.

## Ownership and Dependencies

`internal/inventory` owns quantities, starter policy, two equipment slots, checked bonus aggregation/effective stats, controls, receipts, persisted compatibility and atomic healing coordination. `internal/inventory/contract` owns inventory snapshots, mutation requests/results and failures; item fields use exact `internal/item/contract` types, without compatibility aliases. Item definitions/category/effect validation move to Item System. Runtime, character health, battle eligibility and localization text retain their owners.

Inventory production code may use standard library, exact item contracts, its own packages, runtimecontract, interaction, exact character contracts and localization for presentation. Inventory contract may import item contract, character contract and runtimecontract, never implementations. Item production and contract packages have no inventory, runtime, localization, persistence or Telegram imports. Future gameplay uses exact contracts; composition alone constructs concrete services.

## Catalog and Starter Interface

Inventory declares the consumer-owned catalog interface with `Lookup(itemcontract.ItemID) (itemcontract.Definition, bool)`, `Items() []itemcontract.Definition`, and `Effect(itemcontract.ItemID) (itemcontract.EffectDefinition, error)`.

`NewService(catalog, starter []inventorycontract.GrantLine, characters, clock)` preserves existing error-return construction and receives the narrow catalog and character ports. Construction validates and defensively copies the injected catalog and starter lines, preventing caller mutation. Definitions use Item System validation rather than duplicate category/effect rules. Starter validation remains inventory-owned: reject duplicates, undefined identities, nonpositive quantities and numeric overflow. The production starter seed is separate from `internal/item/items.json`, retaining Coin 10, Stick 1, cloth_armor 1 and healing_potion 3. All equipment starts unequipped. No tuning or serialized identifier changes are permitted.

Item errors are translated to inventory typed failures at inventory service boundaries where callers require inventory classifications. Item never imports inventory. `Effect` may remain a delegating service capability for existing inventory consumers while the definition/effect types and rules belong to item contracts. The canonical item API is documented in item-system/design.md.

## State and Compatibility

Keep every existing schema and migration unchanged. `inventory_catalog` remains the persisted inventory compatibility projection of item ID/category/numeric mechanics. Preparation uses the caller transaction to reject missing or changed registered definitions and atomically register new definitions before admission. Display text is excluded. Starter changes affect only pending entitlements.

Preserve existing table layouts, canonical request field names/order/version, operation IDs, receipts, frozen healing semantics, revisions, savepoints and error classification. Grants do not initialize characters or consume starter entitlement. Open alone ensures the character and grants the kit once. Equipment preserves ownership and changes only matching slots. Effective stats add equipped modifiers with checked arithmetic, leaving character base stats/health intact. Healing resolves replay before current eligibility, delegates recovery/health to Character System, consumes one copy, clamps over-heal, rejects full health/incapacitation, and commits receipts/inventory/health with caller-owned battle state. Existing detailed flow and fault policies remain in design-history.md.

## Presentation and Operator Integration

Normal `/inventory`, opaque callbacks, pagination, transient labels, in-place ephemeral edits, owner/group/expiry/revision binding, no fallback and absence of production Use actions remain unchanged. All names/descriptions stay in localization resources. Inventory/composition validates retained item IDs and localized text bounds (80/400 runes and existing safe Unicode policy); pure item code does not import localization.

With `REPLIPG_DEV_INVENTORY=true`, composition requires strictly positive `REPLIPG_OWNER_TELEGRAM_USER_ID` before storage/network side effects. Authorize authenticated numeric actor ID against it before any development behavior/output for `/dev_inventory_grant <item-id> <quantity>`, `/dev_inventory_heal <item-id>` and application-layer `/dev_item <item-id>`. Never use usernames, group administration or client claims. Grant/heal target only the requesting owner's character and retain existing human/group/ephemeral authorization. Invalid/unknown arguments return safe localized usage or rejection only to an authorized owner. Existing runtime and character development behavior stays unchanged.

The item probe is read-only, creates no character/inventory/control/receipt, and has no callbacks. It displays localized name, stable ID, category and applicable mechanics only. Inventory grants/healing keep existing state changes, replay and rejection behavior. All responses remain ephemeral within groups/supergroups; no public/private fallback. Disabled mode omits routes/menu descriptions. The existing localized menu pipeline includes only enabled commands.

## Traceability and Verification

| Requirements | Component and evidence required |
|---|---|
| 1.1–1.7 | Item port, starter validation, compatibility projection, checked modifiers |
| 2.1–2.7 | Open, starter receipt, atomic initialization and restart tests |
| 3.1–3.9, 7.1–7.7 | Existing personal view/control and in-place edit regressions |
| 4.1–4.9 | Slots, derived modifiers and current-state/replay regressions |
| 5.1–5.6, 6.1–6.7, 8.1–8.7 | Grant/healing coordination, quantity integrity, failure/replay tests |
| 9.1–9.7, 10.1–10.5 | Privacy, owner/disabled/config rejection, executable probes and manual record |

Run focused item/inventory/composition checks, then pinned `./scripts/check` and `./scripts/smoke` through devenv. Exercise an existing database fixture and assert schemas, migrations, saved receipts, ownership and frozen mechanics remain compatible. Fake Telegram must prove successful owner use, rejected non-owner/administrator use, invalid owner configuration before side effects, zero writes from item inspection, wrong-chat/ordinary/malformed requests, disabled routes/menu and unchanged runtime/character probes. Real Telegram verification is a separate pending gate; never infer it from automated tests. See manual-verification.md.
