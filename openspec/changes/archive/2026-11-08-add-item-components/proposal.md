# Proposal

## Why

Items currently have rarity and ownership mode but cannot express overlapping classifications. Composable item components let content describe an item as both consumable and material, and provide a place for future type-specific properties.

## What Changes

- Add an item `components` object supporting `weapon: {}`, `consumable: {}`, and `material: {}` markers in any combination, including an empty object.
- Validate supported component types and reject unsupported names or properties. Components initially describe items without enabling gameplay actions.
- Preserve components in immutable grant-time snapshots and include them in snapshot equality and stack matching, independently of declaration order.
- Keep existing saved snapshots readable as having no components, without reclassifying owned items from the live catalog.
- Display saved classification using localized component labels during item inspection.
- Apply the user-selected catalog classifications: bread is consumable; stick is material and weapon; coin, Fumo, Dakimakura, and Neco-Arc are materials.

### Confirmed requirements

Multiple components per item, descriptive markers initially, empty component sets allowed, rejection of unknown component types, immutable captured components, and order-independent equality and stacking.

### Proposed defaults and assumptions

Omitted asset components mean an empty set for compatibility. Old snapshots likewise mean an empty set. Inspection displays localized component labels, or a localized empty value. Existing catalog assets remain unclassified until deliberately assigned components; names alone do not determine classification.

### Non-goals

Equipment, consumption, crafting, combat effects, filtering menus, arbitrary scriptable components, data-bearing component properties, a plugin registry, and rewriting existing item snapshots. Rarity and ownership mode remain separate fields. This change does not implement or modify the separate group-encounters proposal.

### Unresolved decisions

None blocking this scope. Catalog classifications were selected during implementation; future component properties are deferred.

## Capabilities

### New Capabilities

None; classification belongs to the existing item and inventory capabilities.

### Modified Capabilities

- `item-definitions`: Component validation, reload behavior, immutable snapshot capture, and legacy snapshot compatibility.
- `inventories`: Component-aware stack identity and localized inspection of saved components.

## Impact

Changes affect `assets/items/schema.cue`, locale assets, `internal/modules/items`, `internal/content`, CUE decoding/validation, inventory inspection, and adjacent tests. SQLite snapshot reuse, saved grant results, and shop order snapshots require compatibility verification. No new dependencies, database columns, workers, or startup wiring are planned. The pending encounter proposal captures item snapshots and should reuse the extended value when implemented.
