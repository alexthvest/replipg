# Design

## Context

See proposal.md for motivation and scope. `items.Definition` and `items.Snapshot` are currently value-only structs. Snapshot equality uses Go comparability; canonical JSON bytes are hashed and compared by SQLite before a matching snapshot ID is reused. Inventory stacks are selected by that snapshot ID. `content.Bundle` shallow-copies definitions safely because they currently contain no reference-bearing fields. Introducing a generic component map would invalidate these assumptions.

The runtime uses Donburi, but durable item classification is held in catalog definitions and saved inventory snapshots. Inventory inspection already combines saved item fields with live interface translations. The pending group-encounters change also plans to capture item snapshots.

## Goals / Non-Goals

**Goals:** Compose validated marker components while preserving detached values, stable snapshot identity, and current transaction boundaries. Keep room for separately typed component properties in a later change.

**Non-Goals:** Replacing the runtime ECS, creating separate Donburi entities for catalog definitions, introducing a generic property bag or plugin framework, or moving rarity and ownership mode into components. Gameplay scope is defined in proposal.md.

## Decisions

### 1. Typed composition at the content boundary

Use `components: {weapon: {}, consumable: {}, material: {}}` as the asset shape. All markers are optional, the object can be empty, and omission defaults to empty. Close the CUE component container and each marker schema so unknown names and properties fail. Scalars, lists, and null are invalid. Existing item assets may omit the field; do not infer classifications from names or assign speculative classifications to user-authored assets.

Add a comparable `items.Components` value with explicit presence state for each supported marker, value-based membership accessors, and deterministic enumeration by component key (`consumable`, `material`, `weapon`). Store it in both Definition and Snapshot. Empty marker objects must be distinguished from absence; a bare empty Go struct per marker cannot encode that distinction.

Use strict JSON decoding/encoding for this component value so the CUE-to-Go path and persisted snapshots agree on supported names and empty-object values. Reject unknown names, marker properties, null, and wrong shapes rather than silently dropping data. Decode into temporary state and assign only after successful validation; decoding an empty object must reset any previously present markers. A missing outer field leaves the zero empty value when decoding fresh definitions or snapshots.

This is component composition with supported types rather than a single kind enum. Adding a type requires a schema, a typed presence field, codec support, labels, and tests; adding data or behavior later requires an explicit compatibility design. A generic map was considered, but it would lose comparability and introduce aliasing into snapshots and published bundles. A bitmask alone is compact but gives future typed component data no clear home.

### 2. Preserve the snapshot identity contract

Keep snapshot version 1 as a backward-readable additive format extension. Canonical JSON omits `components` for the empty value and preserves the exact existing field order and encoding for every pre-component snapshot. Nonempty components serialize as an object with marker names in stable order and empty object values. Decoding explicit empty components produces the same empty value and canonical bytes as omission.

Custom snapshot marshaling, or an equivalent explicit wire representation, must apply to ordinary `json.Marshal` as well as `Canonical()` so grant operation results and shop order snapshots share one format. Do not rely on `omitempty` to omit a nonzero-sized struct automatically. Components contain only values, so existing direct snapshot comparisons and Bundle shallow copies remain valid.

SQLite's digest plus byte equality reuse logic remains authoritative and needs no query or schema change. Byte-identical empty-component snapshots reuse legacy snapshot IDs, preserving stack identity and acquisition order. Different membership changes canonical bytes and therefore separates stacks. Reordering asset declarations does not change the captured value or bytes.

A version bump was considered but would separate otherwise identical legacy stacks unless accompanied by a data migration. Keeping the additive format avoids rewriting snapshots, digests, operation results, shop orders, reservations, or historical identities. Older binaries cannot understand nonempty components reliably; rollback after classified grants requires restoring a pre-upgrade database backup, not silently running an old executable against new data.

### 3. Capture and reload components with the bundle

`content.Bundle.Capture` copies components into the same snapshot as rarity, mode, and bilingual text. Existing atomic publication and caller contexts remain in place. Valid component edits are allowed for continuously active IDs; the existing ownership-mode restriction still applies. Validation failures return contextual item/component errors through the loader, preserving the previous whole bundle on reload. No new goroutines, resources, locks, interfaces, or dependencies are necessary.

### 4. Localize descriptive classification in inspection

Add `menu.item.components`, `item.component.none`, and `item.component.<type>.name` interface keys in both languages and require them through the existing locale validation path. Inspection displays saved membership using current localized labels in deterministic component-key order; an empty set displays the localized empty label. Labels are live interface text like rarity labels and are not copied into grant snapshots. Item text remains saved bilingual text.

Inventory rows, pagination, and callbacks retain their current responsibilities. No action is enabled by component presence. Keep future gameplay systems outside the content codec and views.

### 5. Project structure and boundaries

```text
assets/
  items/schema.cue                         changed: closed component schemas
  locales/schema.cue                       changed: required interface keys
  locales/en.cue                           changed: English component labels
  locales/ru.cue                           changed: matching Russian labels
internal/
  modules/items/
    components.go                         new: typed markers, membership, strict codec
    components_test.go                    new: valid/invalid shapes and value semantics
    definition.go                         changed: component field and validation
    snapshot.go                           changed: capture format and canonical encoding
    snapshot_test.go                      changed: legacy bytes and component equality
  content/
    bundle.go                             changed: component capture
    bundle_test.go                        changed: immutable bundle/capture regression checks
  adapters/cueassets/
    load_test.go                          changed: component asset validation
    watch_test.go                         changed: valid edits and atomic rejection
  adapters/sqlite/
    grants_test.go                        changed: legacy reuse and component-aware stacks
    inventory_test.go                     changed: persistence/restart classification
    shop_test.go                          changed: saved quote/reservation compatibility
  modules/localization/
    inventory.go                          changed: required component interface keys
    catalog_test.go                       changed: missing-label validation
  ui/views/
    inventory.go                          changed: localized inspection field
    inventory_test.go                     changed: saved markers and empty classification
cmd/replipg/                              existing entry point; no new wiring
```

Application grant and shop services continue to pass whole snapshots through their existing transaction ports; codec changes are covered by saved grant replay and shop order recovery tests. The item module owns component meaning and serialization; the CUE adapter loads assets, content owns coherent capture, SQLite persists snapshots, and views render them. Existing asset files with omitted components continue to work. The user explicitly selected catalog assignments during implementation: bread is consumable; stick is material and weapon; coin, Fumo, Dakimakura, and Neco-Arc are materials. Legacy compatibility remains covered by fixtures that omit components.

## Risks / Trade-offs

- Canonical-byte drift could split legacy stacks -> assert literal pre-change JSON and digest fixtures and exercise snapshot-ID reuse against a reopened database.
- Permissive decoding could erase unsupported component data -> use closed CUE schemas and strict component codecs for both assets and stored snapshots.
- Reference-bearing component fields could mutate published content -> retain comparable value types now; require an ownership/copying redesign before adding mutable properties.
- Future marker properties or new component types may be incompatible with older builds -> evolve validation and snapshot format deliberately with migration tests.
- Separate encounter planning mentions the current snapshot format -> implementation must pass whole extended snapshots through capture and rewards; this proposal does not rewrite that change's artifacts.

## Migration Plan

1. Implement compatible decoding and byte-preserving empty-set encoding before adding nonempty component fixtures or runtime values.
2. Verify a pre-component database reopens, existing snapshots and historical records read correctly, and identical unclassified grants merge into legacy stacks.
3. Deploy updated schema, locale assets, and binary together. Existing catalog assets remain valid without components. Operators can assign supported components to future grants through normal validated reloads.
4. Do not backfill owned items or modify stored canonical bytes and digests. No SQL migration is planned.
5. Before classified grants exist, rollback can use the old binary and assets. Afterward, restore a matching pre-upgrade backup for rollback; do not discard components from newly persisted snapshots.
