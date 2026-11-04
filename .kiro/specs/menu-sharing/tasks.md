# Implementation Plan

The user authorized parallel agents for implementation. Tasks marked (P) can run independently within their declared files; dependent sharing/storage/composition tasks still wait for their prerequisites and reviews. Callback-source projection and the additive outbox operations do not consume the new view contract or sharing schema, so their former ordering-only dependencies are removed. Use the existing pinned devenv environment and test helpers; no new dependency, service, or build infrastructure is required. Each executable subtask is intended to fit a 1–3 hour work unit and includes its relevant tests.

Follow the concrete file ownership in the design's File Structure Plan. The first runnable milestone is character sharing, the second adds inventory, and the final milestone validates recovery and retention comprehensively. Include reproduction instructions and separate live evidence with each milestone; a missing live observation must remain explicitly unverified rather than be reported as passed. No task authorizes deleting public shares or changing menu visibility.

- [x] 1. Establish the sharing contracts and persistence prerequisites
- [x] 1.1 Define the injected sharing view contract and enforce its dependency boundary
  - Represent the three supported view kinds, final rendered body, owner/group identity, source edit target, frozen locale, and optional inherited usability deadline through the approved decoration contract.
  - Preserve existing presentation registration compatibility; make production injection explicit rather than introducing a global sharing service.
  - Extend import-boundary checks only for the exact approved contract edges; prove gameplay and runtime packages still cannot import sharing implementation or concrete adapters.
  - Completion is visible through compiling contract consumers and passing positive/negative boundary fixtures in the pinned development environment.
  - _Boundary: SharingContract_
  - _Requirements: 1.1, 1.5, 4.1, 4.4, 6.5_

- [x] 1.2 Integrate durable view and accepted-action storage with an additive migration
  - Add checked sharing metadata, retirement state, action outcomes, due-work scheduling, independent public/feedback retention state, and uniqueness constraints from the approved data model.
  - Provide transaction-bound storage operations and deterministic fixtures using existing isolated-database, clock, and randomness conventions.
  - Enforce one pending action per displayed view and one action per incoming update; retain action tombstones independently of purgeable delivery records.
  - Prove schema-4 upgrades retain existing gameplay/settings data and that active versus retired fingerprints obey the specified constraints.
  - Completion is visible through migration and storage tests demonstrating valid inserts, rejected conflicting reservations, rollback, and retained replay identities.
  - _Boundary: SharingStore — explicit persistence integration_
  - _Depends: 1.1_
  - _Requirements: 3.5, 3.6, 5.6, 6.3, 6.4, 7.1_

- [x] 1.3 (P) Integrate transient callback-source evidence with the transport boundary
  - Project accessible source text, formatting, keyboard callback values, optional receiver, and ephemeral message identity into the approved feature-neutral callback evidence.
  - Validate all entity ranges and UTF-16 boundaries before excluding known lexical entities; make unsupported or ambiguous formatting unavailable for sharing.
  - Preserve existing handling of inaccessible messages and unrelated callbacks, and keep source bodies and identity labels out of diagnostics.
  - Cover real-shaped fake updates with unavailable, malformed, lexical-entity, receiver-mismatch, and inaccessible-source cases.
  - Completion is visible when the mapper supplies valid source evidence without changing unrelated routing or recording personal content.
  - _Boundary: SharingService — explicit interaction and Telegram integration_
  - _Requirements: 2.1, 4.1, 4.2, 4.6, 6.3, 7.1_

- [x] 1.4 (P) Extend the outbox with transaction-bound status inspection and guarded cleanup
  - Reuse the existing content-free status query inside caller-supplied transactions while preserving the current status API.
  - Implement the approved cleanup result semantics, exact kind matching, timestamp cutoff, active-state fencing, and attempts-before-parent removal.
  - Keep feature projection checks outside outbox ownership; support cleanup of abandoned quarantined work only through the documented caller preconditions.
  - Test missing rows, kind conflicts, age boundaries, pending/attempting/retrying refusal, rollback, and terminal/quarantined cleanup without exposing payloads.
  - Completion is visible through outbox tests showing authoritative reads and atomic eligible deletion, with no change to sending or startup recovery.
  - _Boundary: DeliveryLedger_
  - _Requirements: 3.1, 3.6, 5.6, 5.7, 6.3, 6.4_

- [x] 2. Build exact-view sharing and durable outcome handling
- [x] 2.1 (P) Integrate frozen delivery schemas and bilingual sharing presentation
  - Add the public and personal-feedback payloads with pure versioned decoding and single-attempt retry policy.
  - Encode personal authorization through explicit callback fields and fixed deadline; reconstruct the typed authorization rather than decode an interface value.
  - Add English/Russian Share labels, numeric-owner attribution, pending/success/failure/uncertainty guidance, cooldown parameters, and unavailable/reopen guidance to the existing catalog validation workflow.
  - Freeze public body, attribution, destination and source locale; keep new personal guidance in the currently resolved group language.
  - Completion is visible through payload round-trip and bilingual catalog tests, including missing parameters, malformed authorization, no-username attribution, and public payloads with no controls or ephemeral reply target.
  - _Boundary: DeliveryLedger — explicit localization integration_
  - _Depends: 1.1_
  - _Requirements: 2.2, 2.3, 2.4, 2.5, 5.7, 6.1, 6.2_

- [x] 2.2 (P) Decorate rendered views with fingerprint-bound Share capabilities
  - Generate random opaque tokens and versioned canonical fingerprints from the final body and explicit formatting, preserving existing navigation and mutation controls.
  - Record only metadata and hashes for open views; never retain their body or introduce a character-view timer.
  - Carry inventory's existing render deadline and known edit target without extending them; atomically bind new-send message identity on first valid source use.
  - Test body and entity preservation, token grammar, deterministic injected randomness, inherited deadlines, and rejection of different-message bindings.
  - Completion is visible when all supported view kinds gain one Share row while their original body/controls remain unchanged and no publication is requested.
  - _Boundary: SharingService_
  - _Depends: 1.1, 1.2, 1.3, 2.1_
  - _Requirements: 1.1, 1.3, 2.1, 4.1, 4.3, 4.4, 4.6, 6.3_

- [x] 2.3 (P) Project authoritative delivery outcomes into view eligibility and personal feedback
  - Observe durable completion, rejection, pending and quarantine evidence through the injected ledger, without sending or querying runtime tables directly.
  - Persist success time from authoritative terminal evidence and the one-time uncertainty hold; release definite failures without a success cooldown.
  - Queue outcome feedback once while the accepted callback authorization is valid, preserving its original deadline and separating feedback failure from public success.
  - Provide bounded fair due-work processing with at most 100 entries and existing lifecycle supervision; pending work must not starve later outcomes.
  - Test exact 30-second boundaries, projection retries, feedback expiry, fixed uncertainty timestamps, and lag-independent eligibility refresh.
  - Completion is visible when fake durable evidence produces the specified persisted outcome, reservation release and single personal-feedback request without another public request.
  - _Boundary: SharingProjector_
  - _Depends: 1.2, 1.4, 2.1_
  - _Requirements: 3.1, 3.2, 3.3, 3.6, 3.7, 5.1, 5.2, 5.3, 5.4, 5.6, 6.1_

- [x] 2.4 Authorize Share callbacks and accept immutable public publication requests
  - Verify human owner, original group, token presence in the source keyboard, fingerprint, inherited usability, receiver consistency and source-message binding before publication.
  - Refresh outstanding evidence before checking reservation, cooldown or uncertainty hold; keep refresh, acceptance, replay identity and outbound enqueue in the callback transaction.
  - Append frozen-locale numeric owner attribution to the verified source body and validate complete text/entities without truncation; never read or mutate gameplay state.
  - Distinguish foreign/unknown controls from owned expired or unavailable views; use fresh short-lived callback authorization for rejection guidance without renewing the view.
  - Permit a new deliberate action after the required interval, retain prior public messages, and never retransmit an older uncertain delivery automatically.
  - Completion is visible through callback tests proving one publication request for concurrent/replayed actions, safe failure rollback, exact source preservation, and oversize rejection with personal guidance.
  - _Boundary: SharingService_
  - _Depends: 1.3, 1.4, 2.1, 2.2, 2.3_
  - _Requirements: 1.2, 1.4, 2.1, 2.2, 2.3, 2.6, 2.7, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 5.5_

- [x] 2.5 (P) Complete feedback projection and finite content cleanup
  - Inspect queued feedback independently, abandon uncertain feedback without further feedback or retransmission, and maintain separate public/feedback outcome and purge dispositions.
  - Remove resolved authorized payloads after their independent 24-hour retention intervals through guarded ledger cleanup; preserve pending work and replay tombstones.
  - Clear callback authorization after feedback enqueue or skip, retire expired inventory metadata safely, and retain usable character capabilities without copied body text or a new expiry.
  - Make repeated cleanup idempotent, fence state changes in the same transaction, and keep partially purged actions scheduled until remaining work is eligible.
  - Completion is visible through deterministic retention tests covering public/feedback completion at different times, unknown feedback, active-work preservation, rollback and replay after purge.
  - _Boundary: SharingProjector_
  - _Depends: 1.2, 1.4, 2.3_
  - _Requirements: 5.4, 5.6, 6.3, 6.4, 7.1_

- [ ] 3. Deliver the runnable character-sharing milestone
- [x] 3.1 (P) Integrate decoration into production character summaries
  - Pass the authenticated actor, original group, final localized character body and absence of an existing view deadline to the injected decorator.
  - Preserve character inspection, recovery, command declarations and legacy registration behavior; exclude development commands from decoration.
  - Cover character views without usernames and locale changes after rendering, without fetching live state on Share.
  - Completion is visible when production character inspection yields a private summary with Share and no new expiration policy or gameplay-side publication.
  - _Boundary: Composition, SharingContract — explicit character presentation integration_
  - _Depends: 1.1_
  - _Requirements: 1.1, 1.3, 1.4, 1.5, 2.2, 2.3, 4.4_

- [x] 3.2 Integrate the sharing ledger, routes and supervised worker into production
  - Construct the narrow adapter over the existing outbox and inject locale resolution without allowing sharing to import settings or runtime implementation.
  - Register both delivery schemas and Share routing before sealing, and construct the additional worker after registration using existing startup order and cancellation ownership.
  - Inject decoration only into supported production views while preserving existing settings, development-menu and command-publication behavior.
  - Test adapter state mapping, kind allowlist and missing-record integrity errors, plus registration/worker startup failures.
  - Completion is visible when the production executable can process a private character Share into a durable ordinary group send and personal outcome guidance through the existing runtime.
  - _Boundary: Composition, DeliveryLedger — explicit production integration_
  - _Depends: 1.4, 2.2, 2.3, 2.4, 2.5, 3.1_
  - _Requirements: 1.2, 1.5, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 6.5_

- [x] 3.3 Verify the automated character milestone through production composition
  - Exercise private character open, explicit Share, blocked double tap and deliberate repost at the 30-second boundary against fake Telegram with deterministic time.
  - Assert both public copies remain unchanged and button-free, while ordinary browsing, failed personal delivery and development/settings commands produce no automatic public snapshot.
  - Exercise source callback shapes and no-username attribution through the production test adapter; retain the original private view without refreshing gameplay state on Share.
  - Include runnable commands, expected observations and separate automated/live evidence in the milestone reproduction record and user-facing sharing instructions.
  - Completion is visible through passing automated production-flow checks and runnable live instructions; task 3.4 remains the separate mandatory live observation checkpoint.
  - _Boundary: Sharing verification — explicit character milestone integration_
  - _Depends: 3.1, 3.2_
  - _Requirements: 1.3, 1.4, 1.5, 2.4, 2.5, 3.1, 3.2, 3.3, 3.4, 5.5, 7.1, 7.2, 7.3, 7.4_

- [ ] 3.4 Record the live character-sharing milestone observation
  - Verify character Share in a dedicated Telegram group using an actual human client, including private browsing, no-username attribution, double-tap suppression and deliberate repost after 30 seconds.
  - Record the observations separately from automated evidence; do not infer success from fake Telegram or an API-only send.
  - Completion is visible through a recorded live observation of the expected personal and public messages.
  - _Boundary: Sharing verification — mandatory live character checkpoint_
  - _Depends: 3.3_
  - _Blocked: Requires owner-operated Telegram client verification; no Telegram client tool is connected in this environment._
  - _Requirements: 7.2, 7.3_

- [ ] 4. Deliver the runnable inventory-sharing milestone
- [x] 4.1 (P) Integrate sharing into inventory pages and item details
  - Decorate final numbered pages, item details, Back/navigation results and equipment-update views without changing their existing body or controls.
  - Use a single render instant for existing inventory controls and Share metadata, passing the exact inherited deadline and known edit target.
  - Keep ordinary item management ephemeral and preserve quantities, starter entitlement, equipment semantics, ownership and stale-control behavior.
  - Completion is visible when all supported inventory views expose Share while the public copy has no navigation, mutation or Share controls and gains no independent expiry.
  - _Boundary: Composition, SharingContract — explicit inventory presentation integration_
  - _Depends: 1.1_
  - _Requirements: 1.1, 1.3, 1.4, 2.1, 2.4, 4.4, 4.5_

- [x] 4.2 Verify exact inventory snapshots across navigation and failed edits
  - Cover pages, item selection, Back, equipment changes, later inventory changes and group-language changes without substituting current state for source content.
  - Exercise definite and ambiguous edit failures: an old source token must remain tied to its old body and a displayed replacement to its own body.
  - Reject missing keyboard membership, mismatched fingerprints, foreign message/actor/group binding, unsupported formatting and oversized attributed content without publishing it.
  - Verify expired owned controls receive reopen guidance using fresh callback authorization but cannot publish, and inaccessible messages retain existing mapper behavior.
  - Completion is visible through mapper-to-production tests that compare public text/entities to the chosen source and assert unchanged gameplay and personal control rules.
  - _Boundary: Sharing verification — explicit inventory/source integration_
  - _Depends: 1.3, 2.4, 4.1_
  - _Requirements: 1.4, 2.1, 2.2, 2.6, 2.7, 4.1, 4.2, 4.3, 4.5, 4.6, 5.5_

- [x] 4.3 Verify the automated bilingual inventory milestone and document its reproduction
  - Demonstrate page and item-detail sharing, equipment browsing, cooldown feedback and later intentional reposts through production composition in English and Russian.
  - Verify current-language controls/guidance alongside a snapshot whose previously displayed body retains its old language; verify settings authority and command visibility remain unchanged.
  - Record precise live reproduction commands and distinguish observed versus unperformed checks from fake-Telegram evidence.
  - Completion is visible through passing bilingual automated milestone checks and runnable live instructions; task 4.4 remains the separate mandatory live observation checkpoint.
  - _Boundary: Sharing verification — explicit bilingual inventory milestone integration_
  - _Depends: 3.2, 4.1, 4.2_
  - _Requirements: 1.3, 1.5, 2.2, 2.4, 3.1, 3.2, 3.3, 3.4, 6.1, 6.2, 6.5, 7.1, 7.2, 7.3, 7.4_

- [ ] 4.4 Record the live bilingual inventory-sharing milestone observation
  - Verify page/item sharing, navigation, equipment changes, original-view language preservation and cooldown behavior in a dedicated Telegram group using a human client.
  - Record English and Russian observations and confirm settings authority and personal browsing remain intact.
  - Completion is visible through recorded live observations of private browsing and explicit immutable public copies in both languages.
  - _Boundary: Sharing verification — mandatory live inventory checkpoint_
  - _Depends: 4.3_
  - _Blocked: Requires owner-operated Telegram client verification; no Telegram client tool is connected in this environment._
  - _Requirements: 7.2, 7.3_

- [x] 5. Validate recovery, retention and compatibility across the complete feature
- [x] 5.1 (P) Verify concurrent actions and restart behavior at delivery boundaries
  - Restart around pending work, an interrupted attempt, durable completion before projection, and queued feedback using the real isolated persistence/outbox boundaries.
  - Verify replay identities, per-view reservations, successful cooldown timestamps and uncertainty holds survive restart and cannot be renewed or bypassed by projection lag.
  - Exercise concurrent Share presses and projector/outbox races, definite rejection followed by retry, and manual resharing after unknown outcome without automatically resending the old action.
  - Assert expired or failed personal feedback never creates a compensating public request or alters a confirmed public outcome.
  - Completion is visible through deterministic integration and race checks proving preserved immutable payloads, one accepted action per update and no automatic repost of uncertain sends.
  - _Boundary: Sharing verification — explicit durability integration_
  - _Depends: 2.3, 2.4, 2.5_
  - _Requirements: 3.1, 3.3, 3.5, 3.6, 3.7, 5.1, 5.2, 5.3, 5.4, 5.6, 5.7, 7.1_

- [x] 5.2 (P) Verify retention and privacy across projected delivery outcomes
  - Verify 24-hour cleanup for independently completed public and feedback records, including permanently abandoned quarantined feedback and public attempts.
  - Prove active, unprojected, too-young and unrelated-kind work cannot be purged; retain due work until both delivery payloads are safely removed.
  - Verify deletion ordering, transaction rollback, action tombstones, retired inventory fingerprints and usable character metadata across cleanup and subsequent replay.
  - Capture diagnostics to prove bodies, source evidence, usernames, hashes and raw transport errors do not leak through normal, rejected or cleanup paths.
  - Completion is visible through cross-component retention/privacy tests demonstrating removed eligible content with unchanged authorization, replay fencing and active work.
  - _Boundary: Sharing verification — explicit retention and privacy integration_
  - _Depends: 1.4, 2.5, 5.1_
  - _Requirements: 3.5, 5.4, 5.6, 6.3, 6.4, 7.1, 7.4_

- [x] 5.3 Complete the repository gates and final runnable verification record
  - Run the repository's pinned formatting, test, vet, lint, race and production smoke workflows; fix feature regressions within the approved scope and rerun affected checks.
  - Verify the additive upgrade, older-binary schema refusal and documented whole-directory backup/rollback expectations without changing existing gameplay tables or Telegram command scopes.
  - Complete runnable sharing, restart and bilingual reproduction instructions, including cooldown behavior, no separate Share expiry, retained older shares and truthful uncertain-delivery guidance.
  - Record actual commands/results and live test-group observations separately; leave unavailable live checks explicit and do not substitute automated evidence for them.
  - Completion is visible through passing applicable repository gates, traceable milestone evidence, and no unexplained regression in existing character, inventory, settings, privacy or runtime behavior.
  - _Boundary: Sharing verification — explicit final system integration_
  - _Depends: 3.3, 4.3, 5.1, 5.2_
  - _Requirements: 1.3, 1.4, 1.5, 3.4, 4.4, 5.5, 5.6, 6.5, 7.1, 7.2, 7.3, 7.4_

## Implementation Notes

- User-authorized parallelism uses separate task-owned files; each task still requires an independent review and fresh verification before its completion checkbox and selective commit.
- Sharing store updates use revision fencing; reread a view/action after a successful update before another update in the same transaction.
- Callback source is transient evidence with body, formatting, keyboard callback values and ephemeral message identity; an opaque token alone never authorizes publication.
- Production Share registration accepts an injected RefreshFunc. Bind it to the projector before admission through existing registration/worker-factory ordering; do not reorder runtime recovery or introduce a feature import into runtime.
- Both sharing delivery kinds are single-attempt; unknown sends are not automatically reposted. Latest accepted-action state governs uncertainty guidance so historical unknown outcomes cannot override later confirmed results.
- Private delivery retention uses independent public/feedback projection clocks; cleanup preserves content-free replay identities and usable character capabilities without an added expiry.
- Live checkpoints 3.4 and 4.4 separate mandatory human-client evidence from automated milestone work. They remain unchecked until actual observations are available; their absence prevents feature-level GO even when all automated checks pass.
