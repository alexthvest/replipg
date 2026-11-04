> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

> Historical discovery below predates item extraction. The approved 2026-11-04 split uses pure item definitions with no text fields; localized text remains exclusively here and inventory/composition validates it. The legacy-text-shape decision is superseded.

# Research & Design Decisions

## Summary

- **Feature**: `localization`
- **Discovery Scope**: Extension with security-sensitive permission verification and durable external menu synchronization. Integration-focused discovery was supplemented by direct inspection of transactional dispatch/lifecycle and official Telegram/Unicode documentation.
- **Key Findings**:
  - Both interaction handlers and scheduler handlers execute inside transactions; neither may perform Telegram HTTP within those transactions.
  - Existing feature boundaries can remain intact by sharing immutable presentation and injecting a locale resolver. Item text is not part of persisted mechanics reconciliation.
  - A group preference cannot be implemented using Telegram client-language menu selection. Per-chat menu ownership, durable retry state and operator handling of competing scopes are necessary.
  - Existing outbox contracts deliver messages, not command configuration. A settings-owned menu worker avoids widening that contract.

## Research Log

### Instructions and Existing Context

- **Context**: Requirements were generated and the user approved continuation to design. Earlier dialogue confirmed group-wide language, ephemeral settings, admin-only changes, and embedded catalogs.
- **Sources Consulted**: `AGENTS.md`; `.agents/skills/kiro-spec-design/SKILL.md` and its design-principles, discovery-light, synthesis and review-gate rules; localization brief/requirements; `.kiro/steering/roadmap.md`; `.kiro/steering/development-workflow.md`.
- **Findings**: `product.md`, `tech.md`, and `structure.md` do not exist; roadmap and incremental delivery steering provide the available architectural context. The pinned toolchain and dependency versions are recorded in `go.mod` and devenv configuration.
- **Implications**: Preserve the modular monolith and runnable vertical delivery. No new localization dependency or external translation service. Requirements approval is recorded with this design; design approval remains a separate user gate.

### Runtime Transactions, Preparation, and Delivery

- **Sources Consulted**: `internal/interaction/{events,dispatch,registry}.go`; `internal/telegram/processor.go`; `internal/runtimecontract/{database,work,responses}.go`; `internal/scheduler/worker.go`; `internal/outbox/routing.go`; `internal/app/{runtime,composition,shutdown}.go`.
- **Findings**: Router commits feature state, processed update and cursor atomically. Immediate responses are snapshotted before commit and sent afterward. Acknowledgement precedes dispatch. Scheduler handlers execute under a transaction and outbox projections return closed message-response types. App owns cancellation, admission, readiness and worker joins.
- **Implications**: Introduce optional pre-transaction handler preparation for membership evidence. Add a dedicated supervised menu worker with persisted desired/applied state. No HTTP in WorkScope or startup preparation callbacks. Reuse existing rendered response semantics; do not rerender accepted deliveries from a new group locale.

### Feature Rendering and Item Compatibility

- **Sources Consulted**: `internal/character/{commands,render,development}.go`; `internal/inventory/{catalog,commands,render,controls,store,service,development}.go`; `internal/inventory/items.json`; `internal/devsmoke/register.go`; command composition files.
- **Findings**: Character status and inventory categories currently leak internal English values into presentation. Inventory tokens are already opaque, actor/chat bound, expiring and independent of display text. Item text has 80/400-rune and Unicode safety validation. Stored catalog reconciliation compares stable mechanics, not display names/descriptions. Service construction revalidates legacy catalog text, so removing those fields would widen the refactor.
- **Implications**: Retain legacy item fields and schema; bilingual resources become production presentation authority. Inject a resolver and pass locale explicitly into existing renderers. Export/reuse item-text validation rather than introduce a second policy. Preserve owner-label sanitization and all gameplay canonical requests.
- **Working-tree constraint**: Existing user edits use Russian names, including leather-armor wording under the historical `cloth_armor` ID. Preserve that intent and stable mechanics instead of restoring the README's old cloth wording.

### Telegram Membership and Menu Capabilities

- **Sources Consulted**: Pinned local `github.com/go-telegram/bot v1.27.0` API usage; existing `internal/telegram/client.go` and `commands.go`; [getChatMember](https://core.telegram.org/bots/api#getchatmember), [command precedence](https://core.telegram.org/bots/api#determining-list-of-commands), [setMyCommands](https://core.telegram.org/bots/api#setmycommands), [deleteMyCommands](https://core.telegram.org/bots/api#deletemycommands), consulted 2026-10-28.
- **Findings**: Current dependency exposes membership lookup and scoped command operations. Reliable lookup of another user's membership requires the bot to administer the chat. Narrower command scopes and language variants may override a chat default. Command names must remain stable while descriptions and ephemeral declarations are published as a complete set.
- **Implications**: Validate bot and actor identities/status through bounded reads; deny unverifiable changes without disabling settings inspection. Publish default-language chat menus independent of client locale. Own and clean known `en`/`ru` variants in that scope; document unmanaged member/admin/other-language overrides. README's prohibition against per-chat menus must intentionally change.
- **Additional gap**: `interaction.Callback` currently lacks `ActorIsBot`; map it so settings callbacks preserve the human-only rule.

### Plural Rules and Resource Validation

- **Sources Consulted**: [Unicode CLDR 49 language plural rules](https://www.unicode.org/cldr/charts/49/supplemental/language_plural_rules.html), consulted 2026-10-28; existing numeric inventory layout and UTC character deadlines.
- **Findings**: English integer cardinal counts distinguish one/other; Russian distinguishes one/few/many. Existing item quantities need not be rewritten into noun phrases. Go templates can support named substitution but unrestricted template execution is unnecessary.
- **Implications**: Support a small validated integer-count form selector and restricted named fields; keep date/time values and mechanics unchanged. Exact manifest validation prevents silently mixing languages. No full ICU engine, decimal formatting framework or generic translation platform is needed.

### Architecture and Migration Integration

- **Sources Consulted**: `internal/architecture/boundaries_test.go`; `internal/persistence/migrate.go`; `internal/persistence/migrations/0003_inventory.sql`.
- **Findings**: Character/inventory imports are explicitly allowlisted. Runtime cannot import feature implementations. Migrations are explicitly embedded and ordered, not discovered from the directory automatically.
- **Implications**: Add specific immutable-localization import edges; compose settings in the executable. Add migration 0004 and update both embed and ordered registration. A runtimecontract transaction interface lets the existing concrete DB satisfy worker needs without feature-to-persistence imports.

## Architecture Pattern Evaluation

| Option | Strengths | Risks / Limitations | Decision |
|---|---|---|---|
| Shared embedded catalogs and injected locale | Fits fixed two-language scope; no dependency; stable feature mechanics | Requires exact validation and import allowance | Selected by user |
| Typed Go translation bundles | Compile-time discoverability | Translation edits require Go code | Rejected during discovery |
| External i18n engine | Broader formatting support | Extra dependency and integration surface | Unneeded for current scope |
| Reuse scheduler/outbox for menu HTTP | Existing durable infrastructure | Transactional scheduler and message-only outbox require unrelated redesign | Rejected |
| Settings-owned desired/applied menu worker | Explicit consistency, safe idempotent publication | Adds one supervised loop | Selected |

## Design Decisions

### Decision: Separate Immutable Presentation From Group Preferences

- **Context**: The same translated content serves all features, while a group setting is mutable and durable.
- **Selected Approach**: Standard-library-only catalog package; settings-owned preference store; injected consumer resolver functions and explicit locale renderer arguments.
- **Rationale**: Generalizes the actual shared behavior without creating a new domain layer or allowing runtime to import gameplay.
- **Trade-offs**: Render/register signatures need deliberate compatibility adapters and architecture fixtures.
- **Follow-up**: Validate every visible production/development string, including errors and stale controls.

### Decision: Fresh Permission Evidence Before Transaction

- **Context**: Administrator checks require external I/O; a settings handler already runs transactionally.
- **Alternatives Considered**: HTTP within transaction; transport-specific membership fields on all events; optional route preparation.
- **Selected Approach**: Optional `PreparingHandler` returns a handler holding private, five-second, event-bound evidence. Mutation validates it alongside control/revision checks.
- **Rationale**: Preserves closed events and runtime/domain separation while avoiding network-held writer locks.
- **Trade-offs**: Telegram membership and SQLite cannot be atomically observed. The operational guarantee is a fresh per-request check with bounded freshness.
- **Follow-up**: Tests must cover cancellation, panic isolation, forged/misbound evidence and lost administrator status.

### Decision: Persist Menu Intent With Settings

- **Context**: Saved group language and remote menu configuration cannot commit together.
- **Selected Approach**: Atomic desired locale/fingerprint update; serialized menu publication outside transactions; conditional acknowledgement; indefinite capped backoff and periodic refresh.
- **Rationale**: Generalizes menu synchronization as desired/applied state and adopts Telegram's idempotent complete replacement rather than building a new outbox command protocol.
- **Trade-offs**: Menus can lag behind gameplay text, so settings explicitly shows pending publication. A 15-minute refresh repairs later drift.
- **Follow-up**: Startup refresh and enabled-command fingerprints must include ephemeral flags and development command removal.

### Decision: Keep Item Mechanics and Legacy Text Shape

- **Context**: Current item constructor, seed parsing and tests depend on legacy text fields while mechanics persistence excludes them.
- **Selected Approach**: Retain legacy fields; use stable IDs to look up bilingual presentation and validate it through the current text policy.
- **Rationale**: Avoids an unnecessary content migration and preserves intentional current edits.
- **Trade-offs**: Two text representations remain temporarily; production rendering must have a single explicit authority, the bilingual resources.
- **Follow-up**: Tests assert every retained item has both translations and language choice cannot alter canonical gameplay requests.

### Decision: Use Flat Translation Resources

- **Context**: After reviewing a nested resource example, the user explicitly selected the flat variant.
- **Selected Approach**: One JSON string map per locale. Ordinary dotted keys resolve directly; plural variants use form suffixes; item text uses `items.<stable-id>.name` and `.description`. No nested objects or in-band metadata.
- **Rationale**: Easier text editing while retaining the existing typed presentation API and embedded-catalog approach.
- **Trade-offs**: Required-key validation expands plural suffixes per locale rather than comparing raw English and Russian key sets. Reserve the item prefix and reject literal/plural key collisions.
- **Follow-up**: Reject nested and non-string values, validate all locale-specific forms and placeholders, and preserve item IDs and mechanics. The earlier nested resource/version-field proposal is superseded; the binary manifest defines the bundled format.

## Risks & Mitigations

- Missing bot administrator rights: settings inspection remains read-only and changes explain verification failure; document required deployment privileges.
- Competing command scopes: narrowly own group defaults and known variants; document cleanup of unmanaged overrides and rollback.
- Menu accepted before local acknowledgement: retry complete replacement and fence acknowledgement by current revision/fingerprint.
- Corrupted saved locale: fail validation/operation without default overwrite; report sanitized operational failure.
- Translation formatting/size drift: validate manifest, grammar forms, item limits, final runtime content, and bilingual pagination.
- Existing working-tree changes: only create/update localization spec artifacts during design; implementation must preserve unrelated edits.

## Design Review Record

- Mechanical coverage review matched all 61 acceptance-criterion IDs to the design traceability table and checked populated boundary/file sections.
- Independent runtime review identified two local contract gaps: transport-neutral menu retry classification and fencing obsolete failure bookkeeping. The draft was repaired to define `runtimecontract.MenuError` and require revision/fingerprint predicates for both success and failure updates.
- The repair also made migration registration, resource versioning, and provider identity validation explicit. No new requirement ambiguity was found; design approval remains pending.
- The subsequent flat-resource revision removed in-band versioning, updated resource/lookup contracts and validation tests, and preserved all acceptance-criterion mappings. This changes resource representation only; requirements remain approved and design approval remains pending.

## References

- [Telegram Bot API](https://core.telegram.org/bots/api) — authoritative membership and command-management contracts.
- [Unicode CLDR 49 plural rules](https://www.unicode.org/cldr/charts/49/supplemental/language_plural_rules.html) — English/Russian cardinal categories.
- `AGENTS.md` and `.kiro/steering/development-workflow.md` — architecture, validation and incremental-delivery constraints.
