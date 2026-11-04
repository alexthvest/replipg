# Item System Design

## Boundary and Interfaces

`internal/item/contract` defines immutable value structs ItemID, Category, EffectKind, EffectDefinition and Definition and safe typed errors/validation, importing standard library only. Definition carries ID, category, attack bonus, defense bonus, healing HP and the configured effect kind. Its Validate and EffectDefinition methods centralize mechanical rules. EffectDefinition carries item ID, kind and healing HP. Preserve all existing serialized identifiers. No type aliases remain in inventory contracts.

`internal/item` imports only standard library and its exact contract. Its strict immutable catalog loads embedded `internal/item/items.json` containing only definitions. Preserve coin/inert, stick/weapon +1 attack, cloth_armor/armor +1 defense, healing_potion/consumable 10 HP with heal_self_fixed_hp. IDs retain the existing 1–64 ASCII letter/digit/underscore/hyphen policy. Strict decoding rejects unknown fields, extra documents, duplicate IDs, unsupported categories/effects and incompatible numeric fields. Validation belongs to item contracts; no duplicate inventory category/effect validator is introduced.

The catalog exposes `Lookup(ItemID) (Definition, bool)`, `Items() []Definition`, and `Effect(ItemID) (EffectDefinition, error)`. Lookup values and returned slices in deterministic content order cannot mutate catalog state. Unknown and nonusable effects return safe item errors. Inventory declares the consumer-owned subset interface, defensively validates/copies injected definitions and starter lines, and translates item errors to inventory failures where needed. Item never imports inventory. Inventory retains its compatibility projection and transaction ownership; no schema or migration changes are needed. Starter lines remain separate inventory seed data. Localization owns names/descriptions; inventory/composition validates bilingual content through its existing adapter.

## Operator Integration

`cmd/replipg` owns `/dev_item <item-id>` registration, handler and menu wiring outside the pure domain. It shares REPLIPG_DEV_INVENTORY and requires positive REPLIPG_OWNER_TELEGRAM_USER_ID before storage/network activity. Numeric authenticated owner identity, human actor, group/supergroup and valid original ephemeral authorization are mandatory before behavior/output. Non-owner administrators also reject. The handler reads immutable definitions and current group localization, returning localized name, stable ID, translated category and applicable healing/attack/defense mechanics (inert: no mechanics). Missing/extra arguments give safe usage; unknown IDs give safe unknown-item guidance only to an authorized owner. No callbacks or gameplay writes are created, including no character/inventory initialization or receipts. Existing runtime update bookkeeping remains runtime-owned. Responses use the existing ephemeral pipeline without public/private fallback.

The same configured owner check protects inventory grant/heal probes; their mutations target only the requesting owner. Disabled mode omits all three routes/menu entries. Runtime/character development modes remain unchanged. Item inspection uses a read-only settings locale lookup, returning English for an unconfigured group without creating settings or synchronization work; existing normal settings resolution stays unchanged.

## Dependencies and Verification

Domain dependencies: none. Visible demonstration runtime prerequisite: telegram-runtime and application composition/localization. Inventory depends on item contracts. Localization depends on stable item content and inventory UI; Battle depends on item definitions/effects and inventory operations/modifiers.

Requirements 1–2 map to item contract/catalog tests; 3 maps to application fake-Telegram and menu/config tests; 4 maps to compatibility/import regressions, pinned scripts/check and scripts/smoke and manual-verification.md. Use isolated temporary storage; never inspect the live database. Keep live success/non-owner rejection evidence pending if not observed. User approval for requirements, design, tasks and implementation was explicit on 2026-11-04.
