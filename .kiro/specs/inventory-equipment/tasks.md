> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

# Implementation Plan

Implement the approved compact numbered inventory UI, ordinary inert items such as Coin, one-time starter supplies, two equipment slots, and coordinated healing. The plan follows three runnable milestones. Reuse the existing devenv environment, runtime, character implementation, temporary-database fixtures, controlled clocks, and fake Telegram boundary; add no dependency or new deployment infrastructure.

Every executable subtask is an estimated 1–3 hour work unit with focused tests and an observable done condition. Order is sequential unless marked `(P)`. The sole parallel task may overlap only its preceding peer after their common foundation is complete; all shared service, control and composition edits remain sequential. Boundaries map to concrete files in design.md. No tests are optional.

Each milestone's automated integration gate also includes reproducible commands and a Telegram test-group demonstration, following project steering. Documentation and live observations are part of those gates, not standalone implementation tasks. Missing live access leaves the corresponding gate incomplete and must be recorded as manual verification required. Specification generation performs no live interaction.

## Milestone 1: Starter Kit and Compact Inventory Inspection

- [x] 1. Establish inventory contracts and runtime prerequisites
- [x] 1.1 Define typed inventory values and safe failures
  - Define stable item/operation identities, four item categories including inert, two slots, snapshots, mutation inputs/results, effects and equipment modifiers matching the approved design.
  - Validate whole quantities, bounded identities, expected revisions and the closed self-healing effect; keep internal and external actor identities distinct.
  - Provide inspectable domain errors and storage wrappers retaining their cause and runtime storage-failure classification without revealing raw data.
  - Done when contract tests validate all supported values and failures, including inert items, without importing feature implementations.
  - _Boundary: Inventory Contract_
  - _Requirements: 1.1, 1.2, 1.6, 1.7, 4.7, 5.4, 6.6, 8.2, 8.3, 9.1, 9.2_

- [x] 1.2 Load and validate the immutable catalog and starter supplies
  - Provide version-controlled consumables, weapons, armor and an inert Coin definition; choose and document seed tuning without treating mockup quantities as mandatory balances.
  - Validate category-specific mechanics, positive starter quantities, duplicate/undefined identities, unknown fields, and mechanics-only item definitions; validate bounded plain-text names/descriptions in localization resources.
  - Keep returned catalog values immutable and equipment initially unequipped; no inert-item effects or currency behavior are introduced.
  - Done when valid embedded content loads without I/O side effects and invalid catalog/starter fixtures fail safely and deterministically.
  - _Boundary: Catalog_
  - _Depends: 1.1_
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 1.6, 1.7, 2.2, 9.2_

- [x] 1.3 Integrate the additive inventory schema
  - Add the next ordered migration for registered catalog mechanics, inventory roots, positive ownership, equipment assignments, permanent operation receipts and expiring controls.
  - Enforce restrictive ownership/catalog foreign keys, two compatible slots, integer/range constraints and expiry lookup indexes; allow inert category only with zero mechanics.
  - Update both explicit migration inclusion lists and deliberate schema-version fixtures while preserving earlier migrations and existing runtime/character state.
  - Done when fresh and version-2 databases upgrade and reopen, invalid quantity/slot/category states fail, and migration failure or future-schema refusal retains existing guarantees.
  - _Boundary: Inventory Composition and Schema Integration_
  - _Depends: 1.1, 1.2_
  - _Requirements: 1.5, 1.7, 2.4, 4.1, 5.5, 7.3, 8.1, 8.6_

- [x] 1.4 Add generic transactional preparation before update admission
  - Introduce the optional domain-neutral startup preparation callback and invoke it in a bounded transaction after migrations and before registration, Telegram setup or readiness.
  - Preserve nil-callback behavior; propagate cancellation, timeout, preparation and commit failures with a safe startup stage and normal resource cleanup.
  - Avoid feature imports, network calls and goroutines in preparation.
  - Done when runtime tests prove the callback sees migrated storage, failures roll back, no updates are admitted prematurely, and existing startup behavior remains compatible.
  - _Boundary: Inventory Composition and Runtime Integration_
  - _Depends: 1.3_
  - _Requirements: 1.4, 1.5, 8.6, 9.2_

- [x] 1.5 Carry optional actor labels for personal headers
  - Extend mapped command and callback identities with optional requesting-actor username metadata while keeping numeric identity authoritative.
  - Preserve missing labels, reject control characters during presentation, and retain unrelated command/message routing behavior.
  - Keep labels out of inventory ownership, receipts, controls and diagnostics; do not create profile storage.
  - Done when mapper fixtures preserve optional labels for both commands and callbacks without changing authorization, and absent labels support the personal `you` fallback.
  - _Boundary: Inventory Interaction and Transport Integration_
  - _Requirements: 3.8, 7.1, 9.1, 9.2_

- [x] 1.6 Enforce exact inventory contract dependencies
  - Allow future gameplay consumers to import the exact inventory contract without importing inventory service or nested implementation packages.
  - Restrict the inventory contract to approved value dependencies; preserve runtime-to-gameplay and character-to-other-gameplay prohibitions.
  - Cover nested/platform-specific sources and command composition using positive and negative fixtures rather than adding battle implementation.
  - Done when import review confirms character/inventory contract use preserves dependency direction without service, transport, driver or concrete-storage leakage.
  - _Boundary: Inventory Composition and Architecture Integration_
  - _Depends: 1.1_
  - _Requirements: 1.6, 4.8, 5.6, 6.6, 8.5, 9.1_

- [x] 2. Build persistent opening and personal navigation
- [x] 2.1 Persist catalog compatibility and inventory read state
  - Reconcile registered mechanics in the supplied transaction: allow new identities/display edits and reject removed or mechanically changed registered definitions atomically.
  - Provide ownership/root reads, stable item ordering, bounded pages, revision metadata, equipment summaries and existing-character identity resolution without health-table mutation.
  - Establish isolated storage/clock fixtures and zero-modifier/empty-view behavior before inventory initialization; never grant supplies during ordinary reads or startup preparation.
  - Done when reopening preserves state, incompatible content rolls back new registrations, and indexed pages include every positive owned quantity including inert items.
  - _Boundary: Inventory Store_
  - _Depends: 1.2, 1.3, 1.6_
  - _Requirements: 1.5, 1.7, 2.4, 3.2, 3.4, 3.5, 4.7, 5.5, 8.1_

- [x] 2.2 (P) Render the approved numbered inventory and detail views
  - Render the owner header, numbered item/quantity rows, equipped markers, two-slot summary and matching numbered selection buttons using supplied view/control values.
  - Provide bounded pages, Previous/Next and Back controls, equipment details and battle-only healing descriptions; inert details expose no equip/use action.
  - Use plain text, sanitized transient labels with `you` fallback, and Unicode-safe limits validated against the existing response contract.
  - Done when rendering fixtures reproduce the approved layout, preserve item/button mappings on every page, fit response limits and never expose standalone consumption.
  - _Boundary: Inventory Presentation_
  - _Depends: 1.1, 1.2, 1.5_
  - _Requirements: 3.2, 3.3, 3.4, 3.5, 3.8, 3.9, 6.7, 7.5, 9.1, 9.2_

- [x] 2.3 Establish shared replay receipts and operation isolation
  - Canonicalize mutation equality with versioned inputs and original application revisions; detect changed-input reuse before current-state checks.
  - Implement permanent receipt storage and operation-local savepoint handling on caller transactions, with no feature-owned commits or nested public mutations.
  - Roll back expected rejection locally and propagate storage/scan/cleanup failures for outer rollback; establish deterministic starter identity and later mutation namespaces.
  - Done when receipt tests prove matching replay is harmless, conflicting reuse changes nothing, rejected work can be followed by a safe outer commit, and savepoint failure remains retryable storage failure.
  - _Boundary: Inventory Store_
  - _Depends: 1.1, 2.1_
  - _Requirements: 2.5, 2.7, 8.2, 8.3, 8.5, 8.6, 9.2_

- [x] 2.4 Persist inventory initialization and quantity transitions
  - Provide root creation, positive quantity increments/decrements, last-copy deletion, conditional revision advancement and one-way starter entitlement updates through the supplied transaction.
  - Keep quantity arithmetic checked, preserve equipment ownership constraints, and leave starter entitlement pending for roots initialized by later gameplay grants.
  - Exercise multi-line writes and rejection/failure rollback using the shared operation isolation, including final-copy removal and unchanged assignments.
  - Done when store tests persist/reopen complete starter and quantity transitions, reject invalid or exhausted quantities without partial writes, and expose all write primitives needed by opening, rewards and consumption.
  - _Boundary: Inventory Store_
  - _Depends: 2.1, 2.3_
  - _Requirements: 2.2, 2.5, 2.7, 5.1, 5.2, 5.3, 5.4, 5.5, 6.5, 8.1, 8.4, 8.6_

- [x] 2.5 Coordinate automatic character creation and one-time starter opening
  - Use the supplied character service and caller transaction to create missing characters, establish inventory roots and grant configured starter quantities through private grant helpers.
  - Commit starter receipt, entitlement and quantities together; retain received kit after tuning changes and preserve existing character state except legitimate character-service recovery.
  - Keep inspection/bonus lookups creation-free and distinguish absent characters from existing characters without initialized inventories.
  - Done when new/existing players receive exactly one kit across repeated/concurrent cross-group opens, failed initialization leaves no partial entitlement, and a preexisting inventory root populated through the private grant fixture does not lose starter eligibility.
  - _Boundary: Inventory Service_
  - _Depends: 2.1, 2.3, 2.4_
  - _Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 3.4, 3.7, 8.1, 8.6_

- [x] 2.6 Resolve bound opaque read and navigation controls
  - Generate persistent random tokens and bind their actions/item/page references to actor, character, group, expected revision and absolute lifetime.
  - Validate token structure, ownership and expiry; map page-local display numbers to server-side item references and clamp navigation after ownership changes.
  - Provide bounded opportunistic expiry cleanup while retaining permanent receipts; keep labels and personal content out of control records.
  - Done when cross-user/group, malformed, unknown and expired read controls expose no inventory data, and valid controls survive restart until their original deadline.
  - _Boundary: Inventory Interaction_
  - _Depends: 2.1, 2.3, 2.5_
  - _Requirements: 3.5, 3.8, 7.1, 7.2, 7.3, 7.4, 7.5, 8.1, 9.1, 9.2_

- [x] 2.7 Handle authorized ephemeral inventory entry and navigation
  - Register the production inventory command and read/navigation callback route using the existing registry and supplied work scope.
  - Apply original timestamp, human/group and incoming-ephemeral authorization before opening inventory; return fixed usage text for unsupported arguments only when authorized.
  - Send refreshed restricted responses for callbacks without replacing ephemeral messages; leave acknowledgement to runtime and permit no public/private fallback.
  - Done when handler tests display the compact summary/details for the originating actor, ordinary commands or invalid actors cause no initialization, and stale/expired controls provide safe reopen guidance when authorized.
  - _Boundary: Inventory Interaction_
  - _Depends: 1.5, 2.2, 2.5, 2.6_
  - _Requirements: 3.1, 3.2, 3.3, 3.5, 3.6, 3.7, 3.8, 3.9, 6.7, 7.1, 7.2, 7.3, 7.4, 9.1, 9.2_

- [ ] 3. Integrate and verify the first runnable inventory milestone
- [x] 3.1 Compose inventory startup and the complete production menu
  - Share the existing character service with inventory, load catalog before runtime construction and attach catalog reconciliation to generic startup preparation.
  - Register inventory alongside character and independently enabled character/devsmoke behavior; publish the complete ephemeral group menu.
  - Update composition fixtures deliberately and supply reproducible inventory/seed/cross-group access instructions with the executable change.
  - Done when the actual production options expose both normal commands, incompatible catalog prevents admission, and character/runtime regression fixtures remain valid.
  - _Boundary: Inventory Composition_
  - _Depends: 1.4, 1.6, 2.1, 2.7_
  - _Requirements: 1.3, 1.4, 1.5, 2.1, 3.1, 3.6, 3.8, 9.5, 9.7_

- [x] 3.2 Exercise starter and numbered navigation through fake Telegram
  - Boot the production executable with isolated temporary storage and local fake Telegram; verify complete menu declaration and preparation/readiness ordering.
  - Drive first/repeated openings for two actors and multiple groups, item details, inert-item viewing, pagination and owner-label fallback.
  - Reopen storage after restart and verify kit uniqueness, shared ownership and bound read-control lifetime without real network calls.
  - Done when executable tests show the approved personal UI and persistent quantities with one starter grant per character and no cross-user view leakage.
  - _Boundary: Inventory Verification_
  - _Depends: 3.1_
  - _Requirements: 1.7, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 3.1, 3.2, 3.3, 3.5, 3.8, 3.9, 7.1, 7.3, 8.1, 9.6, 9.7_

- [ ] 3.3 Close the inspection milestone with fault and startup evidence
  - _Verification: Automated fault/startup and executable checks PASS with independent review; live Telegram demonstration not run, MANUAL_VERIFY_REQUIRED. See manual-verification.md._
  - Verify failed starter initialization/commit, catalog incompatibility, preparation timeout/cancellation and foreign or expired navigation preserve preceding committed state.
  - Run applicable devenv checks and reproduce first-open, repeat-open, cross-group inspection and restart in a dedicated Telegram test group using documented commands.
  - Record actual automated and live results separately; do not inspect or mutate the workspace's live database for fixtures.
  - Done when automated cases pass and the first runnable demonstration/reproduction record exists; retain manual verification required if live evidence is unavailable.
  - _Boundary: Inventory Verification_
  - _Depends: 3.2_
  - _Requirements: 1.4, 1.5, 2.7, 3.6, 7.3, 8.1, 8.6, 9.5, 9.6, 9.7_

## Milestone 2: Equipment Management

- [ ] 4. Deliver replay-safe two-slot equipment changes
- [x] 4.1 Define slot rules and checked derived combat attributes
  - Define compatible weapon/armor assignment, replacement, empty-slot and already-equipped no-op rules without changing total ownership.
  - Reject consumables/inert items as equipment; derive only additive weapon attack and armor defense with checked whole-number arithmetic.
  - Keep maximum/current health, recovery and base attributes unchanged, leaving battle loadout policy downstream.
  - Done when pure rule tests cover all slot/category combinations, zero bonuses, replacements and numeric boundaries without mutating character values.
  - _Boundary: Inventory Service_
  - _Depends: 1.1, 1.2, 2.5_
  - _Requirements: 1.6, 1.7, 4.1, 4.3, 4.4, 4.6, 4.7, 4.8_

- [x] 4.2 Persist equipment assignments and current bonuses
  - Read/update the two matching slots through the caller transaction while preserving owned quantities and category/ownership constraints.
  - Provide current equipment/modifier snapshots using the same catalog and inventory revision; never persist effective character attributes.
  - Cover database rejection of dangling or incompatible assignments and preservation of unequipped copies.
  - Done when equipped/replaced/unequipped state reopens correctly and every assignment remains backed by positive owned quantity.
  - _Boundary: Inventory Store_
  - _Depends: 2.1, 4.1_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.7, 4.8, 5.5, 8.1_

- [x] 4.3 Apply equipment changes with revision and replay checks
  - Check matching/conflicting receipts before current-state/revision validation; require the expected inventory revision for new equipment requests.
  - Coordinate assignments, actual-change revision increments and accepted receipts within operation isolation, including accepted no-ops without duplicate bonuses.
  - Return current equipment state and original application revision on replay without undoing later changes.
  - Done when replacements and unequips are atomic, stale changes reject, replay cannot reassign old equipment, and failed operations leave quantities and receipts intact.
  - _Boundary: Inventory Service_
  - _Depends: 2.3, 4.1, 4.2_
  - _Requirements: 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9, 8.2, 8.3, 8.4, 8.6_

- [x] 4.4 Connect equipment detail controls to personal mutations
  - Extend existing opaque controls with equip/unequip actions bound to the actor/group and expected revision; derive operation identity from the token rather than callback delivery identity.
  - Validate binding/expiry before resolving accepted replay, reject unused stale controls, and issue fresh controls from the resulting state.
  - Display applicable Equip/Unequip actions and updated equipped markers while keeping inert/healing details read-only.
  - Done when valid callbacks refresh the owner's equipment view, repeated accepted controls are harmless, and foreign/expired/stale controls cannot mutate equipment.
  - _Boundary: Inventory Interaction_
  - _Depends: 2.2, 2.6, 2.7, 4.3_
  - _Requirements: 3.3, 3.8, 3.9, 4.9, 6.7, 7.1, 7.3, 7.4, 7.5, 7.6, 8.2, 8.3_

- [ ] 4.5 Verify the runnable equipment milestone
  - _Verification: Automated executable and concurrent-control checks PASS with independent review; live Telegram demonstration not run, MANUAL_VERIFY_REQUIRED. See manual-verification.md._
  - Exercise equip, replacement, unequip, ownership preservation and unchanged character base stats through composed fake Telegram and persisted storage.
  - Race controls from one revision and multiple groups; replay the accepted control after later changes and restart, and prove rejected controls do not alter assignments.
  - Include reproducible test-group steps and record actual equipment demonstration results alongside applicable automated checks.
  - Done when the personal UI shows correct assignments/bonuses, concurrency/replay tests pass and milestone evidence is recorded; retain manual verification required for unobserved live behavior.
  - _Boundary: Inventory Verification_
  - _Depends: 4.4_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.9, 7.3, 7.6, 8.1, 8.2, 8.3, 8.4, 9.5, 9.6, 9.7_

## Milestone 3: Healing and Final Consistency

- [x] 5. Stabilize composable gameplay grants and healing
- [x] 5.1 Expose validated atomic reward grants
  - Extend the private starter-grant foundation into the approved caller-facing grant operation for existing characters, including inventory-root creation without consuming starter entitlement.
  - Canonicalize and aggregate reward lines with checked arithmetic, validate the whole reward before applying it, and preserve equipment assignments.
  - Reject missing characters, undefined items and invalid/exhausted quantities atomically; accept inert Coin through the same ordinary grant/stack path and leave loot selection to the caller.
  - Done when multi-item grants commit together, equivalent replay is harmless, changed-input reuse conflicts, and invalid trailing lines cannot leave partial rewards.
  - _Boundary: Inventory Service_
  - _Depends: 2.3, 2.4, 2.5_
  - _Requirements: 1.7, 2.6, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 8.2, 8.3, 8.6_

- [x] 5.2 Coordinate one healing consumption with character health
  - Validate the frozen self-healing effect and owned quantity, resolve inventory replay first, and use the injected character service to account for due recovery.
  - Reject full HP, pending incapacitation, absent copies and nonconsumable items without spending; clamp accepted over-healing through the character health service.
  - Derive a bounded distinct character operation identity, apply healing/decrement/revision/receipts in the supplied transaction and delete last-copy ownership cleanly.
  - Done when one accepted action spends one item and heals only its owner, all rejection cases preserve operation-local state, and replay after last-copy use or later damage never heals/spends twice.
  - _Boundary: Inventory Service_
  - _Depends: 2.3, 2.4, 2.5, 5.1_
  - _Requirements: 1.7, 5.5, 6.1, 6.2, 6.3, 6.4, 6.5, 6.6, 6.7, 8.2, 8.3, 8.5, 8.6_

- [x] 5.3 Verify inventory and character coordination in a caller-owned action
  - Combine actual inventory/character services with a test-owned battle-state fixture without implementing battle policy or importing future gameplay implementations.
  - Inject failure before/after each receipt, during cleanup and at outer commit; assert inventory, health, fixture state and replay availability commit or roll back together.
  - Intentionally commit after expected domain rejection to prove savepoint isolation, including due recovery normalization and failed starter work; test unexpected character replay as an invariant failure.
  - Done when every fault leaves the preceding durable state intact and no feature-owned transaction or partial effect escapes the outer action.
  - _Boundary: Inventory Verification_
  - _Depends: 5.1, 5.2_
  - _Requirements: 2.7, 6.1, 6.3, 6.6, 8.2, 8.3, 8.5, 8.6, 9.6_

- [x] 6. Integrate development verification without production use actions
- [x] 6.1 Add self-only development grant and healing probes
  - Implement opt-in ephemeral grant/heal commands using the same human/group authorization and inventory operations as production-compatible gameplay callers.
  - Target only the requesting character, derive repeat-safe operation identities from incoming updates and reuse existing separately enabled character damage/defeat controls for preparation.
  - Keep full-health/incapacitation checks intact and leave standalone production inventory consumption absent.
  - Done when probe handler tests demonstrate valid grants/healing, reject malicious arguments/targets and conflicting identity reuse, return harmless replay results for matching duplicate requests, and cannot grant equipment effects to inert items.
  - _Boundary: Inventory Verification_
  - _Depends: 2.7, 5.1, 5.2_
  - _Requirements: 6.1, 6.3, 6.4, 6.7, 7.1, 7.2, 8.2, 9.3, 9.6_

- [x] 6.2 Integrate the strict opt-in inventory verification mode
  - Parse the false-default development flag in command composition before side effects, preserve independently controlled character/devsmoke modes and reject invalid boolean values safely.
  - Register probe routes/menu entries only while enabled and remove them from the managed menu when disabled; normal inventory remains available.
  - Include ignored-local-environment examples and safe reproduction instructions with composition changes.
  - Done when enabled startup exposes self-only probes, disabled startup has neither routes nor menu declarations, and invalid flags fail before storage/Telegram activity.
  - _Boundary: Inventory Composition_
  - _Depends: 3.1, 6.1_
  - _Requirements: 9.3, 9.4, 9.5, 9.7_

- [x] 6.3 Verify healing and probe lifecycle through the executable
  - Drive damage, accepted healing, full-health rejection, incapacitation rejection, last-copy exhaustion and restart through the local fake Telegram boundary.
  - Keep domain clocks controllable without changing transport authorization time; verify development probes supply self-only action authorization without introducing production battle policy.
  - Restart with probes disabled and prove stale development commands cannot mutate items while normal inventory inspection/equipment still work.
  - Done when executable tests observe matching health/quantity outcomes, no production Use action, and no disabled probe route or declaration.
  - _Boundary: Inventory Verification_
  - _Depends: 5.3, 6.2_
  - _Requirements: 6.1, 6.2, 6.3, 6.4, 6.5, 6.7, 8.1, 9.3, 9.4, 9.6_

- [ ] 7. Complete fault, privacy and release verification
- [x] 7.1 Verify storage failures and cancellation remain retryable
  - Inject query execution, scan, iteration, close, busy/cancellation and savepoint cleanup failures through inventory operations and dispatch.
  - Prove safe wrappers preserve causes/classification, outer rollback restores quantities/equipment/receipts, and no failed update is reported as a successful mutation.
  - Cover preparation rollback as well as handler work without exposing raw SQL, supplied values or personal payloads.
  - Done when fault tests preserve the previous committed state and existing runtime retry/cancellation behavior for every failure class.
  - _Boundary: Inventory Verification_
  - _Depends: 5.3, 6.3_
  - _Requirements: 2.7, 8.5, 8.6, 9.2, 9.6_

- [x] 7.2 Verify concurrent mutations and durable later-state replay
  - Race grants, equipment changes and consumption against isolated real storage; preserve serializable accepted results, positive ownership and compatible assignments.
  - Replay after restart, later grants/damage/loadout changes and changed starter configuration; distinguish original application revision from current state.
  - Prune expired controls in bounded batches and prove permanent receipts remain authoritative with no replay window reopened.
  - Done when race checks pass and no duplicate kit, reward, healing or equipment rollback occurs across the covered schedules.
  - _Boundary: Inventory Verification_
  - _Depends: 4.5, 5.3_
  - _Requirements: 2.5, 2.6, 5.2, 5.5, 7.3, 8.1, 8.2, 8.3, 8.4, 9.6_

- [x] 7.3 Verify post-commit delivery and personal-data isolation
  - Reject or expire restricted delivery after successful item operations, then reopen inventory with a valid new interaction and verify committed state without duplicate effects.
  - Use sensitive label, command and response fixtures to prove profile/header content and personal messages do not enter inventory tables; operational diagnostics must also exclude opaque tokens and item payloads.
  - Exercise foreign actors/groups, callback and ordinary-command variants, and response-size boundaries without public or private-chat fallback.
  - Done when personal-data assertions and later-inspection tests pass, including transient owner labels and inert-item details, while health endpoints remain free of inventory contents.
  - _Boundary: Inventory Verification_
  - _Depends: 6.3_
  - _Requirements: 3.6, 3.8, 3.9, 7.1, 7.2, 7.3, 8.7, 9.1, 9.2, 9.6_

- [ ] 7.4 Close release gates and the final runnable demonstration
  - _Verification: Canonical check, smoke, build, inventory executable and architecture gates PASS; final independent coverage/design reviews found no code defects. Live milestone observations remain MANUAL_VERIFY_REQUIRED. See validation-report.md and manual-verification.md._
  - Run canonical formatting, full tests, vet, lint, race and production smoke through devenv, plus inventory executable checks and review of package dependencies.
  - Confirm additive migration/catalog compatibility, nil preparation compatibility and preservation of character/runtime behavior without dependency or lock changes.
  - Supply actual commands/results, compatible whole-volume rollback guidance and final test-group demonstrations of grant, equipment, healing rejection/acceptance, restart and disabled probes.
  - Done when prescribed automated checks and all milestone evidence are recorded; retain failed/unrun/manual-required gates explicitly instead of claiming feature completion.
  - _Boundary: Inventory Verification_
  - _Depends: 3.3, 4.5, 6.3, 7.1, 7.2, 7.3_
  - _Requirements: 1.5, 3.1, 4.9, 5.1, 6.1, 6.3, 6.6, 8.1, 8.5, 8.6, 8.7, 9.3, 9.4, 9.5, 9.6, 9.7_

## Planning Notes

- Core product/tech/structure steering is absent; approved requirements/design, existing source and incremental-delivery steering define the plan.
- The private grant/receipt path is complete in milestone 1 so starter replay and navigation have no hidden milestone-3 prerequisite. Milestone 3 adds the public reward capability and coordinated consumption.
- Read navigation and shared control infrastructure precede equipment mutation controls. Pure rendering can overlap inventory read persistence only after foundation; service/control/composition changes cannot overlap their prerequisites.
- The user-approved header uses only optional transient actor metadata. Coin remains an ordinary inert item; no economy or currency subsystem is added.
- Manual Telegram evidence is required during implementation and is not claimed by specification generation.
- Task-plan review gate: PASS. Independent task-graph sanity review: PASS after one task-local repair pass covering storage prerequisites and duplicate-request wording.
- Final quick-spec sanity review: PASS, independent review of requirements, design and tasks directly from disk. Implementation status and remaining live gates are recorded below.

## Implementation Notes

- Owner subsequently confirmed personal inventory rendering, equipped weapon bonuses and unchanged character base stats. Partial live evidence is recorded in manual-verification.md; other checklist outcomes were not individually reported.
- Software implementation and automated verification are delivered. Tasks 3.3, 4.5 and 7.4 remain unchecked solely for the required live Telegram observations; no live Telegram messages were sent or observations fabricated.
- Canonical pinned checks passed: `./scripts/check`, `./scripts/smoke`, `./scripts/build`, uncached inventory executable tests, and uncached production architecture checks. Independent reviews cover each implementation task and the final design/requirements seams.
- Expected domain sentinels retain domain classification. Callback handling checks storage classification before mapping wrapped domain failures to personal responses; savepoint cleanup faults remain retryable and cannot complete an update.
- `Service.EffectiveStats` exposes checked derivation through consumer-owned interfaces; the pure package function remains available within the implementation boundary.
- Executable fixtures use temporary storage, a local TLS fake Telegram proxy and optional temporary Go build overlays for additional catalog entries. Production tuning, the workspace `data/` directory and dependency/lock files are unchanged by fixtures.

## In-place navigation follow-up (owner approved)

- [x] Add validated ephemeral edit targeting to the response contract and Telegram delivery, preserving default send behavior and no-fallback semantics.
- [x] Edit the originating view for inventory navigation, equipment actions, and stale/expired notices; reject callbacks without an ephemeral message target before mutation.
- [x] Cover exact edit payloads, empty keyboard removal, rejected edits, callback ownership, and same-message navigation/equipment in automated tests; update reproduction instructions.
- [ ] Owner live verification of in-place inventory navigation and equipment actions (see manual-verification.md).
