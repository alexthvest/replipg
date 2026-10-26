# Brief: inventory-equipment

## Problem

Monster loot needs durable personal value, and battles need meaningful consumable and equipment choices. Without a coherent inventory boundary, combat, rewards, and future economy features would directly mutate loosely structured item data.

## Current State

There are no item definitions, ownership records, equipment slots, inventory views, or atomic grant/use operations.

## Desired Outcome

Players can inspect and manage their inventory entirely through ephemeral group interactions, equip weapons and armor, and consume usable items during battle. Item ownership and effects remain consistent across duplicate callbacks, failures, and restarts.

## Approach

Separate immutable, version-controlled item definitions from persisted item stacks and equipment assignments. Expose atomic domain operations for granting, equipping, unequipping, and consuming items; battle and reward code call those operations instead of editing inventory tables directly.

## Scope

- **In**: Seeded item definitions; consumables; weapons; armor; stackable quantities where appropriate; equipped slots; inventory summary and item detail; ephemeral management UI; equip/unequip validation; combat stat modifiers; atomic grant and consume operations; battle-safe item effect contracts.
- **Out**: Trading, gifting, crafting, shops, currency, item durability, randomized affixes, player storage, auctions, admin item editing, and loot selection policy.

## Boundary Candidates

- Item catalog loaded from version-controlled configuration or migrations.
- Inventory aggregate for ownership, quantities, and atomic mutations.
- Equipment rules and derived combat modifiers.
- Ephemeral inventory presentation and callback handlers.

## Out of Boundary

- Loot eligibility and probability belong to battle-system.
- Character base stats and incapacitation belong to character-system.
- Telegram transport details belong to telegram-runtime.

## Upstream / Downstream

- **Upstream**: telegram-runtime supplies persistence, transactions, callbacks, and ephemeral responses; character-system supplies the owning character.
- **Downstream**: battle-system reads equipped modifiers, consumes selected items, and grants resolved loot.

## Existing Spec Touchpoints

- **Extends**: None.
- **Adjacent**: character-system remains the source of base stats; battle-system owns when item effects may be used.

## Constraints

All mutating callbacks must carry compact opaque identifiers and be validated against the requesting user and current state. Consumption and reward grants must be atomic and idempotent. Definitions are code/seed managed in the MVP, not editable through Telegram.
