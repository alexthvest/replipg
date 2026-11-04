> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

# Research & Design Decisions

## Summary

- **Feature**: `menu-sharing`
- **Discovery Scope**: Extension; integration-focused discovery across presentation, callback mapping, and durable delivery.
- **Key Findings**:
  - Existing public sends and ephemeral-origin callbacks support explicit sharing without an SDK upgrade. Immediate delivery discards outcomes needed for durable cooldowns.
  - Callback-source content plus a render-time fingerprint can preserve exact view content without retaining the text of every open character menu.
  - Existing outbox status and supervised feature workers support a transaction-bound outcome projector. No delivery hooks or startup reordering are needed.
- **Context**: Core steering files `product.md`, `tech.md`, and `structure.md` are absent. The roadmap, development-workflow steering, README, existing specs, and code supply current project conventions.
- **Verification scope**: Source inspection and official documentation research only. No implementation, automated runtime tests, or live Telegram checks were performed for this design.

## Research Log

### Skills and project guidance

- **Sources Consulted**: `kiro-spec-design/SKILL.md`; its design principles, light discovery, synthesis, and review-gate rules; design/research templates; root `AGENTS.md`; `.kiro/steering/development-workflow.md`.
- **Findings**: Keep the new feature boundary explicit, preserve domain ownership and pinned tools, use runnable vertical milestones, and review traceability before writing the design.
- **Implications**: One feature with injected contracts; no unrelated gameplay or settings work. Independent exploration covered presentation and delivery; synthesis and final review remain in the primary context.

### Source view and failed edits

- **Sources Consulted**: `internal/character/commands.go`; `internal/inventory/commands.go`, `controls.go`; `internal/interaction/events.go`; `internal/telegram/mapper.go`; locally installed pinned SDK models; [Telegram CallbackQuery](https://core.telegram.org/bots/api#callbackquery).
- **Findings**: Character has no existing callback expiry. Inventory tokens expire five minutes after each render. The mapper currently drops callback message text, formatting, keyboard, and receiver information. Telegram supplies the originating bot message for accessible message callbacks, but callback data need not still occur in its keyboard.
- **Implications**: Add transport-neutral source evidence. Match the token against the source keyboard and match canonical body/formatting against the saved fingerprint. Bind the ephemeral message ID, known at edit time or on first valid callback. Failed edits leave the old token usable for its old body; do not designate the latest attempted render as authoritative.
- **Limitation**: Exactness means the verified source view carried by an accepted callback, not an undocumented guarantee about the client's latest pixels. Missing/inaccessible evidence fails closed.

### Text integrity, attribution, and transport limits

- **Sources Consulted**: `internal/runtimecontract/responses.go`; `internal/telegram/delivery.go`; `internal/inventory/localized_render.go`; [sendMessage](https://core.telegram.org/bots/api#sendmessage), [formatting options](https://core.telegram.org/bots/api#formatting-options).
- **Findings**: Local contracts support text links and UTF-16 validation with a 4096-unit ceiling. Character has no public owner label; inventory's fallback is personal “you.” Usernames are transient presentation metadata, not ownership evidence.
- **Implications**: Append a localized attribution with a visible stable user ID and optional `tg://user?id=` text link. Do not require usernames or profile lookups. Validate the complete body plus footer without truncation. Preserve explicit supported formatting; normalize automatically detected lexical entities separately so a generated mention does not cause a false fingerprint mismatch.

### Durable outcome and recovery seams

- **Sources Consulted**: `internal/interaction/dispatch.go`; `internal/outbox/store.go`, `worker.go`, `retry.go`, `recovery.go`, `reconciliation.go`, `types.go`; `internal/app/runtime.go`, `workers.go`; `cmd/replipg/localization.go`.
- **Findings**: Dispatch commits feature writes, queued deliveries, and update identity together. Outbox completion persists authoritative terminal time but discards the receipt. Recovery quarantines interrupted sends before feature registration. Feature worker factories are created after registration and use existing supervised polling. Reconciliation cannot prove Telegram acceptance or safely replay an uncertain public send.
- **Implications**: Add transaction-bound status inspection; project durable evidence into sharing state from both callbacks and a feature worker. Use one public-send attempt per accepted action. No automatic retransmission of unknown sends. Preserve pending reservations until authoritative outcome evidence exists.

### Retention and package boundaries

- **Sources Consulted**: `internal/persistence/migrations/0001_runtime.sql`; `internal/architecture/boundaries_test.go`; `internal/interaction/events.go`; localization catalogs and manifest.
- **Findings**: Outbox payloads currently retain their original bytes; attempts restrict parent deletion. Character/inventory cannot import feature implementations or concrete storage. `ActorUsername` must not become a profile field in persistent feature state.
- **Implications**: Store only hashes and authorization metadata for open menus. Add outbox-owned, guarded cleanup of resolved sharing deliveries and attempts. Retain replay tombstones without bodies or display labels. Document the narrow exception that explicitly authorized delivery payloads can temporarily contain copied menu text already including a display label.

## Architecture Pattern Evaluation

| Option | Strengths | Risks / Limitations | Decision |
|--------|-----------|---------------------|----------|
| Immediate public response | Minimal transport use | No durable outcome/cooldown and weak crash recovery | Rejected |
| Outbox with transactional outcome hooks | Atomic outcome projection | Changes worker, failure, recovery, and registration paths | Rejected as unnecessary |
| Outbox with feature status projector | Reuses authoritative evidence and worker lifecycle | Feedback may lag by a polling interval | Selected |
| Persist every rendered body | Easy reconstruction | Retains character text indefinitely unless a new expiry is imposed | Rejected |
| Render-time fingerprint plus callback-source body | Exact-content validation with small open-view metadata | Requires source projection and normalization tests | Selected |

## Design Decisions

### Decision: One shared decoration and authorization contract

- **Context**: Character and inventory need identical Share semantics without sharing gameplay implementation.
- **Selected Approach**: Inject `sharing/contract.Decorator` into production presentation; both features submit their final body and existing usability deadline. Sharing registers its own callback prefix and owns publication.
- **Rationale**: Generalize the two concrete view integrations, without building a generic menu framework or extending settings.
- **Trade-offs**: Architecture allowlists need one narrow additional contract edge.

### Decision: Verify and copy rather than regenerate

- **Alternatives Considered**: Regenerate current gameplay state; persist all bodies; fingerprint the rendered view and verify callback-source content.
- **Selected Approach**: Canonical SHA-256 fingerprint, opaque random Share token, source-keyboard membership, owner/group checks, and message binding. Full content enters persistent storage only on authorized publication.
- **Follow-up**: Test server-added lexical entities, failed edits, delayed callbacks, receiver mismatches, and inaccessible messages. Character metadata remains available without a new timer; inventory inherits its existing deadline.

### Decision: Project outcomes from existing outbox evidence

- **Selected Approach**: New transaction-bound status read and a bounded sharing worker. A callback first refreshes its own view's outstanding action, so projection lag cannot bypass cooldowns. Successful timestamps come from durable outbox terminal evidence.
- **Build vs Adopt**: Adopt existing outbox, worker lifecycle, validation, localization, random-token and standard hashing facilities. Build only feature policy, source evidence projection, and two additive outbox operations. No external package is introduced.
- **Simplification**: No delivery receipt storage, send hooks, runtime startup reorder, new scheduler, or dedicated transport client.

### Decision: Uncertain outcomes never trigger automatic reposting

- **Selected Approach**: Keep interrupted/indeterminate work quarantined. Project an unknown result, retain a 30-second uncertainty hold anchored once to its durable observation time, and report uncertainty privately where possible. After that hold, another explicit Share press may create a new action and warns about the prior uncertain result.
- **Trade-offs**: A fresh deliberate repost may duplicate a message that was accepted without confirmation. Exactly-once Telegram sending is not promised. Definitely rejected sends do not start a cooldown.
- **Retention**: After projection, no resend or reconciliation is allowed for that old uncertain action. Its immutable delivery content can be purged after 24 hours while a content-free unknown tombstone remains.

### Design review results

- Focused delivery and presentation reviews checked the draft against source findings. The primary review applied the design gate after repairing their local findings.
- Clarified unavailable versus inaccessible source behavior; preserved fresh callback authorization for expired-view rejection guidance.
- Made retired-view hash nullability explicit and added separate public/feedback outcome and purge dispositions, including quarantined feedback cleanup.
- Specified the feedback wire schema and concrete purge eligibility/result contract without adding outcome hooks or changing startup order.
- Mechanical traceability check found all 41 numeric acceptance criteria in the design mapping. Boundary sections, concrete component/file mapping, and absence of placeholders were checked. No unresolved requirements gap was found.

### Task-plan review results

- Generated five major groups with 18 sequential executable subtasks, sized for 1–3 hours each. Character sharing is the first runnable milestone, followed by inventory and comprehensive recovery/retention validation.
- Mechanical task checks found all 41 acceptance criteria covered, no unknown requirement references, preceding explicit dependencies, and an observable completion condition plus boundary annotation for every executable subtask.
- Task-plan review gate passed. Fresh independent task-graph sanity review returned `PASS`, with no blocking prerequisite, ordering, boundary, deliverable, or design-alignment finding.
- Design and tasks were approved through the requested fast-track continuation. All implementation checkboxes remain unchecked; no implementation or live verification is claimed.

## Risks & Mitigations

- Callback source may be unavailable or normalized differently — reject publication rather than fetch newer state; cover with recorded-shape fake updates and a live test-group milestone.
- Success followed by failure to persist the outcome — existing outbox quarantine prevents automatic duplicate delivery; private guidance states uncertainty.
- Slow outcome projection — refresh inside every Share callback and use authoritative timestamps; bounded fair worker batches avoid starvation.
- New text entering persistent work — restrict it to explicit delivery payloads and purge resolved content after 24 hours, preserving active work and deduplication metadata.
- Scope creep into menu visibility, deletion, or gameplay — enforce explicit exclusions and import-boundary tests.

## References

- [Telegram Bot API: CallbackQuery](https://core.telegram.org/bots/api#callbackquery) — source message and callback-data caveat.
- [Telegram Bot API: sendMessage](https://core.telegram.org/bots/api#sendmessage) — standalone public messages and formatting.
- [Telegram Bot API: ephemeral messages and commands](https://core.telegram.org/bots/api#ephemeral-messages-and-commands) — transient client visibility and short response authorization.
- [Approved requirements](requirements.md) — 41 acceptance criteria; the authoritative product behavior.
