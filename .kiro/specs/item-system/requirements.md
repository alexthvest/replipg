# Item System Requirements

## Boundary

Item System is an immutable pure Go catalog with no domain prerequisites. Inventory consumes definitions and owns quantities, starter-kit policy, slots/bonus aggregation, receipts and atomic healing coordination. Localization owns text. Runtime integration belongs in application composition, not the item domain.

## Requirement 1: Stable Item Values and Mechanics

1. The Item System shall define stable ItemID, Category, EffectKind, EffectDefinition and Definition values with no inventory/runtime/localization dependencies.
2. The Item System shall support consumable, weapon, armor and inert categories with the existing serialized values and closed heal_self_fixed_hp effect.
3. The Item System shall require positive whole healing HP for consumables, nonnegative weapon attack or armor defense, and zero incompatible mechanics; inert items shall have no effect or bonuses.
4. The Item System shall preserve coin, stick, cloth_armor and healing_potion identities, categories and numeric tuning; Coin shall remain an ordinary inert item without currency behavior.

## Requirement 2: Strict Immutable Catalog

1. The Item System shall load version-controlled mechanics-only definitions, separately from inventory starter lines and localization resources.
2. The Item System shall reject unknown JSON fields, trailing data, duplicate or missing identities, invalid bounded IDs, unsupported categories/effects and incompatible or invalid numeric values.
3. The Item System shall expose Lookup, Items and Effect with deterministic ordering and defensive copies, rejecting nonusable/unknown effects safely.
4. The Item System shall centralize definition validation for catalog construction and injected inventory content, without mutable globals or side effects.
5. The Item System shall neither open persistence nor create characters, inventories, receipts or starter grants.

## Requirement 3: Operator-Visible Read-Only Inspection

1. Where REPLIPG_DEV_INVENTORY is enabled, the application shall expose /dev_item <item-id> only after requiring a strictly positive REPLIPG_OWNER_TELEGRAM_USER_ID before startup side effects.
2. The application shall authorize the authenticated Telegram actor against the configured numeric owner ID before development behavior or output; usernames and group administrator status shall not confer access.
3. The probe shall require a human actor, supported group/supergroup and valid incoming ephemeral authorization, returning only a personal response without public or private fallback.
4. For a valid item, the probe shall show localized name, stable ID, category and applicable healing/equipment mechanics, and shall cause no character/inventory/control/receipt or gameplay state changes.
5. For unknown IDs or invalid arguments, the probe shall provide safe localized unknown-item or usage guidance to an authorized owner without state changes.
6. While development verification is disabled, the application shall omit the route/menu entry, retaining existing normal inventory and independent runtime/character development behavior.
7. The probe shall add no callbacks; existing inventory callback ownership, expiry and reopening protections shall remain unchanged.

## Requirement 4: Compatibility and Delivery

1. The extraction shall preserve schemas, migrations, serialized item values, canonical receipt encodings, starter tuning and existing inventory behavior without compatibility aliases.
2. The implementation shall provide isolated automated catalog, architecture, compatibility and fake-Telegram owner/non-owner/error/disabled/config/zero-state evidence through the pinned devenv tools.
3. The implementation shall document exact commands, prerequisites, expected output and state changes, and retain real Telegram success/access-control observations as pending until actually observed.
