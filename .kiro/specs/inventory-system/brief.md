# Brief: inventory-system

## Outcome and Current State

Players inspect globally persistent ownership and manage equipment through ephemeral group views. Opening inventory creates a missing character and grants starter supplies once. Earlier implementation and automated evidence exist; live gates remain partial. The approved 2026-11-04 split extracts item definitions without claiming fresh validation.

## Scope and Boundaries

Inventory owns quantities, starter-kit configuration/entitlement, two equipment slots and checked bonus aggregation, atomic grants and character-healing coordination, receipts, controls and personal views. It consumes item definitions through a consumer-owned catalog interface. Its persisted inventory_catalog is a compatibility projection. Item System owns categories, effects and definitions. Localization owns text; Battle owns eligibility, cooldowns, damage and loot selection. Trading, shops, currency, crafting, affixes, durability and standalone production consumption remain excluded. Coin is an ordinary inert item.

## Dependencies and Visible Result

Dependencies: item-system, telegram-runtime and character-system. Localization supplies presentation. Battle consumes inventory operations/modifiers and item definitions separately; menu-sharing consumes inventory views.

Observe /inventory and owner-only /dev_inventory_grant <item-id> <quantity> and /dev_inventory_heal <item-id> in a dedicated group. These probes share REPLIPG_DEV_INVENTORY with the application-layer /dev_item probe and require positive REPLIPG_OWNER_TELEGRAM_USER_ID. Server-side numeric owner identity and ephemeral group authorization precede development behavior/output. Mutations target only that owner. Existing runtime/character probes remain unchanged. See manual-verification.md for pending live gates.

## Approval

The user approved requirements, design, tasks and implementation by requesting “Implement the plan” on 2026-11-04. Preserve tuning, IDs, migrations, schema and receipt encoding; do not introduce compatibility aliases.
