# Implementation Plan

Implement the approved flat English/Russian translation catalogs and one persistent language per group. Everyone can inspect ephemeral `/settings`; only verified current administrators can change the group language. Preserve existing gameplay, item identities, user edits, and restricted-delivery contracts. Use the existing pinned devenv tools and fake-Telegram test infrastructure without new dependencies.

Each executable subtask is an estimated 1–3 hour work unit with focused automated checks and an observable completion condition. Follow the File Structure Plan and component contracts in design.md for exact paths and ownership. Ordering is sequential; no `(P)` markers are assigned because shared catalogs, runtime boundaries, composition, and milestone gates need ordered integration. Dependencies identify cross-group or non-obvious prerequisites; normal ordering still applies. No tests are optional.

Each milestone ends with automated integration checks, reproducible commands and expected observations, and a demonstration in a Telegram test group. Live evidence is recorded separately from automated results. Without live access, record the missing demonstration and leave the milestone gate incomplete; never infer completion from documentation or fake tests. These obligations are included in integration gates rather than standalone documentation or deployment tasks. Task generation itself performs no live interaction.

Task-plan review passed with all 61 acceptance criteria mapped and all 23 executable task bodies checked for boundaries and observable completion. Task-graph sanity review mode: `independent`; verdict: `PASS`. These are planning checks, not evidence of implementation or application-test execution.

## Milestone 1: Ephemeral Language Selection and Character Translation

- [x] 1. Establish shared translation foundations
- [x] 1.1 Load strict flat bilingual catalogs
  - Define the two supported locales and the required logical message/parameter manifest covering current settings, gameplay, command and development text.
  - Load immutable embedded string maps with dotted ordinary keys, explicit plural suffixes and reserved item keys; reject duplicate/unknown keys, nesting, arrays, null/non-string values, trailing data and literal/plural collisions.
  - Expand required forms per locale rather than assuming identical raw key sets; allow no metadata fields or fallback language.
  - Done when valid flat fixtures load deterministically and invalid shapes or missing keys fail with safe key/locale diagnostics.
  - _Boundary: Catalog_
  - _Requirements: 1.1, 8.1, 8.2, 8.3, 9.1_

- [x] 1.2 Format validated parameters and language-specific word forms
  - Support explicit string/integer values and restricted named substitutions; reject unexpected/missing parameters, wrong types, unsupported template constructs and unresolved output.
  - Select English and Russian integer plural forms; reject negative/fractional count inputs and preserve numeric values and UTC deadlines.
  - Apply Unicode and final presentation limits without altering player-provided names or existing sanitization rules.
  - Done when boundary counts, malformed templates, wrong parameter types, Cyrillic text and safe username fixtures render correctly or return controlled errors without leaking raw keys.
  - _Boundary: Catalog_
  - _Requirements: 4.4, 4.5, 4.6, 8.2, 8.3, 8.4, 9.1_

- [x] 1.3 Integrate complete English/Russian content and retained-item validation
  - Populate both catalogs for every current player-visible production/development surface and command description, including error, stale-control and fallback labels.
  - Provide paired item name/description keys for every retained item; preserve intended current item wording and historical identities, including leather-armor presentation under its existing identity.
  - Reuse the existing item-text validation policy for both languages while keeping mechanics-only seed definitions, constructor revalidation and mechanics reconciliation.
  - Done when exhaustive content checks cover every retained item and message, translated names/descriptions meet current limits, and locale choice leaves mechanical definitions identical.
  - _Boundary: Catalog and Inventory Validation Integration_
  - _Requirements: 4.1, 4.2, 4.3, 4.7, 5.1, 5.2, 5.4, 8.1, 8.2, 8.5, 9.1_

- [x] 1.4 Integrate shared runtime capabilities
  - Establish transport-neutral transaction, membership and menu capabilities with safe membership status and classified menu failures carrying retry delay.
  - Allow only the approved shared-presentation and settings dependency directions; preserve runtime isolation, standard-library-only catalogs, feature contracts and concrete-adapter restrictions.
  - Extend controlled-time and fake capability fixtures as needed without real network calls or new test infrastructure.
  - Done when existing database/client implementations can satisfy the new capabilities without feature imports.
  - _Boundary: Runtime Contract Integration_
  - _Requirements: 3.1, 3.2, 6.2, 6.4, 7.7, 9.1, 9.2, 9.3_

- [x] 2. Persist group settings and authorize transactional changes
- [x] 2.1 Integrate the additive localization schema
  - Add the next ordered migration for group locale/revision, desired/applied menu state, retry timing and actor/group-bound settings controls, with strict constraints and indexes.
  - Update explicit migration embedding and ordering plus intentional schema-version fixtures; do not change older migrations or gameplay tables.
  - Preserve the current migration failure and future-schema refusal guarantees.
  - Done when fresh and schema-3 databases upgrade/reopen, invalid settings/control states are rejected, and preexisting character, inventory and starter-kit data remain unchanged.
  - _Boundary: Settings and Persistence Schema Integration_
  - _Depends: 1.4_
  - _Requirements: 2.5, 3.4, 5.3, 7.1, 9.2_

- [x] 2.2 Persist validated group selection and menu intent
  - Resolve existing group language or create an English managed-group record only when absent; never create gameplay state or derive the choice from an individual client.
  - Implement inspection and expected-revision changes, plus desired/applied menu snapshots and conditional acknowledgement/failure bookkeeping.
  - Validate stored values before upserts; classify scan/read/write failures as operational storage errors rather than silently defaulting or overwriting corrupt data.
  - Done when isolated-store tests prove independent groups, atomic locale/menu updates, same-language behavior, durable reopening and safe failure/revision handling.
  - _Boundary: GroupStore_
  - _Depends: 1.1, 2.1_
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.5, 3.6, 3.7, 6.4, 6.5, 6.7, 7.1, 7.3, 7.4, 7.5, 9.2_

- [x] 2.3 Validate opaque settings controls and repeated changes
  - Create compact random controls with actor, group, expected locale revision, selection, five-minute expiry and consumed state.
  - Reject malformed, expired, cross-actor, cross-group, consumed and stale controls; bind successful consumption and language/menu updates to the current transaction.
  - Preserve later choices against control reuse and duplicate updates, including same-language selections; perform bounded expired-control cleanup without storing permission evidence.
  - Done when controlled-clock and replay tests show rejected controls change nothing and accepted controls cannot overwrite a later language selection.
  - _Boundary: SettingsControls and GroupStore_
  - _Requirements: 3.4, 3.8, 7.2, 7.3, 7.7, 9.2_

- [x] 2.4 Integrate pre-transaction preparation and human callback identity
  - Invoke optional route preparation before the dispatch transaction under the existing handler timeout; leave existing handlers compatible.
  - Preserve transactional update completion and response snapshots; isolate preparation panics safely, and leave cancellation/unexpected preparation failure recoverable through existing intake behavior.
  - Carry callback bot identity from transport so command and callback settings entry enforce the same human-actor rule.
  - Done when tests prove preparation precedes transaction acquisition, callback acknowledgement still precedes lookup, bot callbacks are identifiable, and existing routing/deduplication behavior is unchanged.
  - _Boundary: Interaction and Transport Preparation Integration_
  - _Depends: 1.4_
  - _Requirements: 2.7, 3.1, 3.2, 7.2, 7.7, 9.2_

- [x] 3. Deliver runnable administrator settings and a translated character interface
- [x] 3.1 Verify current group membership through the Telegram boundary
  - Perform bounded bot/actor membership reads with validated returned identities and supported status values; expose no provider models or raw errors.
  - Accept administrator/owner evidence only with sufficient bot privileges; distinguish ordinary members from unavailable verification and preserve caller cancellation.
  - Keep checks outside transactions and uncached across requests.
  - Done when fake API cases cover administrator, owner, member, missing bot rights, unknown/nil/mismatched results, timeout and cancellation without unsafe authorization.
  - _Boundary: MembershipPort_
  - _Depends: 1.4, 2.4_
  - _Requirements: 3.1, 3.2, 3.3, 9.2, 9.5_

- [x] 3.2 Publish complete translated chat command menus
  - Replace the group's default-language menu with the complete enabled command set, preserving stable names and ephemeral declarations.
  - Remove owned English/Russian variants in the same chat scope; preserve unrelated member/administrator/other-language scopes and existing global English publication.
  - Map rejection, rate limiting, forbidden, invalid and indeterminate outcomes into sanitized shared failures with retry delay; require the whole replacement/cleanup sequence to succeed.
  - Done when fake API assertions prove exact scope ownership, preserved command behavior and inspectable retry classification without raw transport leakage.
  - _Boundary: MenuPort_
  - _Depends: 1.4_
  - _Requirements: 4.8, 6.1, 6.2, 6.3, 6.4, 6.8, 9.3_

- [x] 3.3 Expose ephemeral settings inspection and authorized selection
  - Open settings only for valid human group actors with current restricted authorization; provide read-only member/unverifiable views and administrator choices with recognizable language names.
  - Prepare private permission evidence bound to the original actor/chat/update/control and enforce its five-second freshness alongside control/revision checks before mutation.
  - Confirm committed changes in the selected language, distinguish pending menu publication, and edit the original personal message on callbacks; issue localized stale/denied guidance only when restricted delivery is authorized.
  - Done when handler tests show English/Russian inspection and changes, ordinary-command silence, no gameplay initialization, expired-evidence rejection and no public/private-chat fallback.
  - _Boundary: SettingsHandler_
  - _Depends: 1.2, 1.3, 2.2, 2.3, 2.4, 3.1_
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 6.5, 7.7, 9.2_

- [x] 3.4 Reconcile durable menu intent outside transactions
  - Build deterministic localized enabled-menu fingerprints and reconcile the earliest due group through short snapshot/result transactions around network publication.
  - Fence both success and failure bookkeeping by attempted revision/fingerprint so obsolete requests cannot acknowledge or delay newer settings.
  - Retain pending intent indefinitely with configured capped backoff and retry-after; schedule the designed periodic refresh and keep storage failures distinguishable from remote failures.
  - Done when deterministic worker tests prove safe crash-after-send retry, changed-in-flight convergence, pending status, periodic repair, capped retry and cancellation without network-held database locks.
  - _Boundary: MenuBuilder and MenuWorker_
  - _Depends: 2.2, 3.2_
  - _Requirements: 6.1, 6.3, 6.4, 6.5, 6.6, 6.7, 7.1, 7.3, 9.3_

- [x] 3.5 Integrate supervised feature workers and live transport ports
  - Initialize transport capabilities before feature registration without mutable deferred globals, while preserving migrations, transactional preparation and update-admission ordering.
  - Add named worker factories, unique-name validation, startup-failure handling, readiness handshakes and correctly sized supervision results; keep feature-specific composition outside runtime packages.
  - Stop new iterations at admission shutdown, use cancellable idle waits, drain active publication/acknowledgement under the existing deadline, and join owned work before resource closure.
  - Done when runtime tests cover nil-worker compatibility, failed factories, worker startup/critical failure, shutdown admission, in-flight drain and forced cancellation without leaked work.
  - _Boundary: Runtime Composition and Background Lifecycle Integration_
  - _Depends: 1.4, 3.4_
  - _Requirements: 6.4, 6.6, 7.1, 7.7, 9.2, 9.3_

- [x] 3.6 Translate character production and development interactions
  - Inject presentation dependencies and resolve the current group locale after existing actor/group/restricted-authorization checks, before rendering usage, summaries and domain failures.
  - Translate creation notices, health/stat labels, states and recovery guidance while preserving UTC values, health operations and permanent operation identities.
  - Fail invalid presentation registration explicitly; require presentation dependencies for every caller and remove legacy English-only registration/rendering paths.
  - Done when bilingual character tests preserve identical gameplay results and existing privacy behavior, including enabled damage/defeat development flows.
  - _Boundary: Character Presentation_
  - _Depends: 1.2, 1.3, 2.2_
  - _Requirements: 1.3, 1.4, 1.5, 1.6, 1.7, 4.1, 4.3, 4.4, 4.7, 4.8, 5.6, 9.1_

- [x] 3.7 Integrate executable localization preparation and registration
  - Construct validated catalogs, retained-item checks and one ordered enabled-command description set before runtime admission, including settings and development flags.
  - Compose existing feature preparation with saved-locale validation, managed-menu fingerprint refresh and expired-control cleanup; inject settings resolution and live membership/menu ports explicitly.
  - Register settings, character presentation and the menu-worker factory; preserve existing catalog preparation, global command publication and startup failure gates.
  - Done when the executable boots with fake Telegram, rejects invalid resources/saved locales before intake, opens ephemeral settings, changes language and renders the next character response accordingly.
  - _Boundary: Executable Localization Integration_
  - _Depends: 1.3, 2.2, 2.3, 3.3, 3.4, 3.5, 3.6_
  - _Requirements: 1.2, 1.3, 2.1, 3.5, 4.8, 6.1, 6.6, 7.4, 7.5, 8.1, 8.2, 8.5, 9.2, 9.3_

- [x] 3.8 Verify the first runnable language-selection milestone
  - Exercise member/admin settings, English default, Russian selection and confirmation, complete translated menus, restart persistence and translated character inspection through the fake Telegram executable.
  - Prove a failed personal reply leaves the saved selection intact without a public/private fallback or gameplay initialization.
  - Include reproducible commands, bot-permission expectations, owned-menu-scope limitations and the Telegram test-group demonstration with actual evidence status.
  - Done when focused automated gates pass and the live group demonstration/evidence is complete; otherwise record the live prerequisite and leave this gate unchecked.
  - _Boundary: Settings and Character Milestone Integration_
  - _Requirements: 1.1, 1.2, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 3.5, 3.7, 6.1, 6.5, 6.7, 6.8, 7.1, 9.4, 9.5_

## Milestone 2: Translated Inventory and Remaining Bot Text

- [x] 4. Apply the group language to every remaining player-facing surface
- [x] 4.1 Render bilingual inventory pages and retained-item details
  - Translate inventory headers, owner fallback, equipment/category/effect labels, item names/descriptions, equipped markers and action/navigation buttons through explicit locale input.
  - Preserve sanitized player labels, compact numbering, stable item/control associations and the existing read-only/battle-only item behavior.
  - Budget both languages against existing content limits and retain valid formatting offsets and complete pagination.
  - Done when bilingual renderer fixtures cover every retained item, empty/full/page-boundary views and worst-case labels without changing quantities, mechanics or button targets.
  - _Boundary: Inventory Presentation_
  - _Depends: 1.2, 1.3_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.6, 5.1, 5.2, 5.4, 8.4, 9.1_

- [x] 4.2 Integrate inventory locale resolution and development responses
  - Resolve current group language once per authorized inventory command/callback and pass it to current page/detail rendering, including usage, failures and expired/stale-control guidance.
  - Preserve opaque controls, actor/chat binding, five-minute expiry, message-edit behavior, item/equipment transactions and gameplay replay receipts; previously opened views refresh only on the next accepted interaction.
  - Translate enabled grant/heal development messages without changing their canonical operations or production availability.
  - Done when the executable shows Russian inventory/items after selection and English after switching back, including old controls, with unchanged starter entitlement, ownership and equipment outcomes.
  - _Boundary: Inventory Handler and Presentation Integration_
  - _Depends: 2.2, 3.7, 4.1_
  - _Requirements: 1.3, 1.4, 1.6, 1.7, 4.1, 4.2, 4.7, 5.1, 5.3, 5.5, 5.6, 9.1, 9.2_

- [x] 4.3 Integrate translated public and restricted development probes
  - Resolve each valid originating group's language for enabled smoke interactions and translate public receipts, restricted replies, labels and usage text.
  - Preserve existing command/callback prefixes, message visibility, acknowledgement, group-message routing and development-flag behavior.
  - Ensure enabled development descriptions use the shared command set and disabling a flag removes its menu declaration during synchronization.
  - Done when fake transport tests show both public and restricted probes in the group language without echoing personal text or exposing disabled routes.
  - _Boundary: Devsmoke and Command Composition Integration_
  - _Depends: 1.3, 2.2, 3.7_
  - _Requirements: 1.3, 1.4, 4.1, 4.2, 4.7, 4.8, 6.1, 6.6, 7.7, 9.1_

- [x] 4.4 Verify the complete bilingual interface milestone
  - Exercise the same actor in English and Russian groups through settings, character, inventory/details/equipment and enabled development probes, including public messages.
  - Check all current visible strings against catalog coverage, confirm values and mechanics are identical across locales, and verify historical messages stay untouched until normal interaction updates them.
  - Include reproducible commands and expected observations for a live group language switch, item inspection and unchanged ownership, with separate actual evidence.
  - Done when bilingual integration gates pass and the live demonstration is recorded; missing live execution leaves this milestone gate incomplete.
  - _Boundary: Complete Localization Milestone Integration_
  - _Requirements: 1.3, 1.4, 1.5, 1.6, 1.7, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 8.1, 8.3, 8.4, 9.1, 9.4_

## Milestone 3: Failure Recovery and Final Regression Gate

- [x] 5. Verify durable consistency, privacy and operational recovery
- [x] 5.1 Exercise authorization, replay and storage failure across boundaries
  - Cover lost administrator status, insufficient bot rights, evidence expiry, forged/misbound/expired controls, repeated updates and controls, and concurrent language choices with deterministic time.
  - Inject read/save/commit failures and corrupt saved selections, proving no partial settings/gameplay changes, false success or silent English overwrite.
  - Verify safe diagnostics and immutable prepared-response language when a previously accepted delivery is retried after a language change; use existing response contracts without expanding outbox kinds.
  - Done when integration tests demonstrate ordered accepted changes, repeat-safe state and no private content or unauthorized visibility fallback under every injected failure.
  - _Boundary: Authorization and Persistence Regression Integration_
  - _Depends: 2.3, 3.3, 4.4_
  - _Requirements: 2.6, 2.7, 3.1, 3.2, 3.3, 3.4, 3.6, 3.7, 3.8, 5.6, 7.2, 7.3, 7.4, 7.5, 7.6, 7.7, 9.2_

- [x] 5.2 Exercise menu convergence and worker lifecycle after interruption
  - Test menu rejection, timeout/indeterminate acceptance, retry-after, capped repeated failures and restart after remote success but before local acknowledgement.
  - Change locale during both successful and failed publication; verify neither obsolete result acknowledges, clears or postpones the newer desired menu, and periodic refresh repairs drift.
  - Restart with changed enabled commands and exercise readiness, critical storage failure, graceful drain and deadline cancellation with durable pending state.
  - Done when fake executable/lifecycle tests prove convergence to the latest saved language, truthful pending feedback and no orphaned workers or network-held writer locks.
  - _Boundary: Menu and Runtime Recovery Integration_
  - _Depends: 3.4, 3.5, 4.3_
  - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 7.1, 7.3, 9.2, 9.3_

- [x] 5.3 Complete the localization release verification milestone
  - Run the repository's formatting, full tests, vet, lint, race and fake executable smoke workflow plus focused catalog/settings checks; investigate failures without broad unrelated refactors.
  - Verify schema-3 upgrade, unchanged existing gameplay state, schema-4 older-binary refusal, invalid-catalog startup rejection and full dependency-boundary enforcement.
  - Include reproduction commands and operational notes for persistent locale, required bot privileges, competing menu overrides, pending-menu recovery and stopped whole-directory rollback with external menu restoration.
  - Demonstrate English/Russian group switching and restart persistence in a Telegram test group; record actual results, risks and any unperformed checks separately.
  - Done when required automated gates and the live recovery demonstration have passed with reviewable evidence; otherwise keep this final gate unchecked and state the remaining prerequisite.
  - _Boundary: Final Localization Validation Integration_
  - _Requirements: 2.5, 5.3, 5.6, 6.6, 6.8, 7.1, 7.7, 8.1, 8.2, 8.3, 8.4, 8.5, 9.1, 9.2, 9.3, 9.4, 9.5_

## Implementation Notes

- Implementation ran in parallel at the user's explicit request, with separate ownership for presentation, settings/persistence, runtime/transport, and executable integration. This intentionally overrides the original sequential scheduling guidance, while retaining the approved functional boundaries.
- All implementation and automated regression tasks have code review and executable evidence. Gates 3.8, 4.4 and 5.3 were closed on 2026-10-28 at the user's explicit request after their report “All works well”. Manual evidence is user-reported overall acceptance, without per-step logs or independent agent execution; see manual-verification.md. Automated coverage is recorded separately in validation-report.md.
- Independent reviews found and resolved permission/control expiry across database reads, rejected callbacks creating managed settings, a translated owner-fallback ambiguity, and obsolete executable composition. Regression tests cover each correction.
- Tests-first execution was partial: actual initial catalog compilation failures, command-composition failure, UTF-16 overflow failure and the permission-freshness regression were observed before their fixes. Some settings and renderer tests followed implementation; an initial Nix sandbox denial was not a valid RED result. No claim of complete TDD-process compliance is made.
- Existing uncommitted roadmap, devenv and item seed edits were preserved. The bilingual presentation uses leather armor for the historical cloth_armor identity without changing mechanics or the user's seed edits.
