# Implementation Plan

The implementation follows three runnable milestones: creation/inspection (tasks 1–3), health/recovery (tasks 4–5), and release verification (task 6). Every executable subtask includes focused tests and an observable completion condition; expected size is 1–3 hours. No tests are optional.

Reuse the existing pinned runtime and devenv workflow. The upstream runtime validation record still mentions outstanding live verification; reconcile its evidence before treating the sequential runtime-to-character implementation handoff as complete. This specification plan does not itself perform live verification.

Order is sequential unless marked `(P)`. A parallel marker permits overlap only with independent peers in its own group after the listed prerequisites finish. Tasks touching the shared character service or command composition remain sequential. File ownership follows the approved design's File Structure Plan.

## Milestone 1: Creation and Inspection

- [x] 1. Establish the character foundation
- [x] 1.1 Define the character contract and safe error vocabulary
  - Establish stable character and operation identities, snapshots, states, eligibility, health-change inputs, and mutation results matching the approved service surface.
  - Separate external player identity from internal character identity; preserve whole-number health and base attributes.
  - Define inspectable domain failures and sanitized storage errors that retain underlying causes without exposing raw text.
  - Done when contract tests validate supported values and error inspection, and the contract depends only on standard-library and runtime-contract capabilities.
  - _Boundary: Character Contracts_
  - _Requirements: 1.3, 4.5, 6.1, 6.2, 6.5, 8.1, 8.2_

- [x] 1.2 Load and validate immutable character tuning
  - Provide embedded production and development settings using the design's initial values; development settings shorten only the recovery duration.
  - Reject unknown fields, fractional or invalid attributes, nonpositive durations, and percentages outside the exclusive zero-to-100 range.
  - Preserve exact decimal percentage values and validate configuration without storage or network activity.
  - Done when valid defaults load deterministically and each invalid fixture returns a safe setting-specific error before side effects.
  - _Boundary: Character Rules_
  - _Requirements: 3.1, 3.2, 3.3, 8.3, 8.6_

- [x] 1.3 Add the character schema to ordered runtime migrations
  - Add immutable migration 0002 for characters and mutation receipts to the existing ordered persistence migration set; keep migration 0001 unchanged.
  - Enforce unique user ownership, distinct internal identity, integer health/attributes, valid active/incapacitated state, receipt equality fields, and foreign-key integrity.
  - Adjust schema-version fixtures deliberately and preserve existing runtime cursor, outbox, and scheduled state during upgrade.
  - Done when fresh and version-1 databases upgrade and reopen successfully, invalid state fails atomically, and failed/future migrations retain the existing startup safeguards.
  - _Boundary: Runtime Integration_
  - _Requirements: 1.3, 1.4, 4.1, 4.4, 5.1, 6.4, 7.1, 7.2, 7.4_

- [x] 1.4 Enforce the narrow gameplay contract dependency
  - Permit gameplay consumers to depend on the character contract while preventing imports of character implementation packages.
  - Enforce the contract's standard-library/runtime-contract-only rule and retain runtime-to-gameplay, transport, and concrete-persistence restrictions.
  - Cover synthetic future consumers without implementing inventory, encounters, or battles.
  - Done when positive fixtures accept composition and contract use and negative fixtures reject each forbidden dependency, including nested and platform-specific production files.
  - _Boundary: Runtime Integration_
  - _Depends: 1.1_
  - _Requirements: 3.5, 6.1, 6.5_

- [x] 2. Prepare authorized ephemeral command entry
- [x] 2.1 Define and validate generic group command declarations
  - Introduce transport-neutral menu declarations with canonical names, descriptions, and ephemeral flags.
  - Validate unique names, field bounds, and the maximum declaration count without embedding gameplay behavior in runtime contracts.
  - Done when invalid declarations are rejected safely and valid declarations retain their intended ephemeral setting.
  - _Boundary: Runtime Integration_
  - _Requirements: 2.1, 2.5, 8.2_

- [x] 2.2 Preserve source authorization time and actor context
  - Map original command timestamps, bot-sender flags, and chat-sender presence into additive transport-neutral metadata.
  - Preserve missing timestamps as missing rather than inventing receipt-time authorization, and avoid retaining profile objects.
  - Retain current routing behavior for unrelated interactions.
  - Done when mapper fixtures distinguish human, bot, and anonymous/chat senders and preserve the original timestamp without leaking transport models.
  - _Boundary: Runtime Integration_
  - _Requirements: 1.6, 2.5, 2.6, 8.1, 8.2_

- [x] 2.3 Publish scoped ephemeral menus through the shared Telegram client
  - Map complete group menu declarations to the pinned library and default-language all-group scope.
  - Reuse bounded calls and safe API failure classification; treat a false result as failure and preserve idempotent replacement behavior.
  - Keep command names and gameplay interpretation supplied by composition rather than owned by the adapter.
  - Done when fake API tests observe exact scoped declarations and success, rejection, rate-limit, cancellation, and malformed-result behavior without real Telegram requests.
  - _Boundary: Runtime Integration_
  - _Depends: 2.1_
  - _Requirements: 2.1, 2.5, 8.2, 8.6_

- [x] 2.4 Integrate optional menu publication into startup
  - Validate and copy optional declarations before I/O, preserving the existing no-menu-management behavior when absent.
  - Publish after identity verification and before polling admission/readiness; unwind startup on failure without losing queued updates.
  - Update startup fixtures to cover the new generic stage while retaining registration sealing, migration, recovery, and shutdown guarantees.
  - Done when readiness follows successful publication, failed publication remains not-ready, and nil declarations preserve existing runtime tests.
  - _Boundary: Runtime Integration_
  - _Depends: 2.3_
  - _Requirements: 2.1, 2.5, 3.3, 7.4, 8.6_

- [x] 3. Deliver automatic character creation and inspection
- [x] 3.1 Persist one character per player through caller transactions
  - Implement automatic creation and existing-character lookup using a supplied transaction, immutable tuning, and an injected clock.
  - Generate stable opaque internal identities, handle owner conflicts without swallowed SQL errors, and preserve stored attributes on repeated creation attempts.
  - Build isolated temporary-storage fixtures using existing test clock support; avoid a feature-owned database lifecycle or nested transaction.
  - Return safe errors while preserving unexpected storage causes, including failed row scans and resource cleanup.
  - Done when repeated and competing creation attempts yield one full-health character per user, distinct users remain separate, and state survives reopen and configuration changes.
  - _Boundary: Character Store and Service_
  - _Depends: 1.1, 1.2, 1.3_
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 3.1, 3.4, 6.1, 6.3, 7.1, 7.4, 8.1, 8.6_

- [x] 3.2 (P) Render private character summaries from contract snapshots
  - Format current/maximum health, base attack and defense, active/incapacitated state, and UTC recovery deadline where applicable.
  - Include the first-use creation notice and fixed usage/domain-error text without raw errors or sensitive diagnostic content.
  - Work only on presentation and its isolated fixtures; consume the settled contract and do not edit the service, store, or their test helpers.
  - Done when snapshot-based tests cover first-use, existing, and incapacitated summaries independently of pending persistence implementation.
  - _Boundary: Character Interaction_
  - _Depends: 1.1_
  - _Requirements: 2.2, 2.3, 2.4, 3.5, 8.2_

- [x] 3.3 Handle actor-only ephemeral character commands
  - Register the production character command with no target arguments and invoke creation/inspection through the approved service surface.
  - Validate individual human actor, group context, incoming ephemeral identity, and expiry based on original message time plus 15 seconds.
  - Produce no mutation or fallback response for unsupported ordinary, missing-authority, expired, bot, or chat-sender requests; return authorized usage errors for invalid arguments.
  - Request the rendered personal response only for the originating actor and propagate storage failures to the transaction owner.
  - Done when handler tests prove authorized first use, repeated inspection, actor isolation, exact expiry rejection, and exclusively restricted response requests.
  - _Boundary: Character Interaction_
  - _Depends: 2.2, 3.1, 3.2_
  - _Requirements: 1.1, 1.2, 1.5, 1.6, 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 8.1, 8.2_

- [x] 3.4 Integrate production character behavior into the executable
  - Construct validated character dependencies before runtime creation and always install the character route and ephemeral declaration.
  - Keep command composition thin, make construction failures testable, and preserve independently opted-in runtime smoke behavior.
  - Verify declared menu entries have registered handlers and update existing production smoke fixtures for generic menu publication.
  - Include setup/reproduction instructions and managed-menu scope/override constraints with this executable change.
  - Done when normal startup exposes the character command, invalid tuning exits before storage/network, and existing runtime smoke behavior remains runnable.
  - _Boundary: Runtime Integration, Character Interaction_
  - _Depends: 2.4, 3.3_
  - _Requirements: 1.1, 1.5, 2.1, 3.2, 3.3, 8.5, 8.6_

- [x] 3.5 Verify the first runnable milestone end to end
  - Exercise the actual command/runtime against temporary SQLite and fake Telegram for first use, repeat inspection, two users, and one user across two groups.
  - Restart against the same state and verify stable identity, stored attributes, restricted replies, and durable cursor behavior.
  - Provide reproducible milestone commands and record a dedicated live-group demonstration separately from automated evidence, without credentials or personal content.
  - Done when automated flows pass and creation/inspection visibility and restart persistence have recorded live evidence; if live access is unavailable, report manual verification required and leave this gate incomplete.
  - _Boundary: Character Verification_
  - _Depends: 3.4_
  - _Requirements: 1.1, 1.2, 1.3, 1.4, 2.1, 2.4, 7.1, 8.5, 8.6_

## Milestone 2: Health and Recovery

- [x] 4. Implement durable health and recovery transitions
- [x] 4.1 Define bounded health and exact recovery rules
  - Implement pure damage/healing rules, immediate zero-health incapacitation, and configuration-based partial recovery with no passive regeneration.
  - Use exact decimal percentage arithmetic, whole-HP ceiling with a minimum of one, and safe integer bounds.
  - Establish UTC deadlines rounded upward to storage precision and reject time/revision exhaustion without partial success.
  - Snapshot deadline and recovery-health amount for each incapacitation so subsequent configuration edits do not alter its outcome.
  - Done when deterministic tests cover zero/excessive amounts, full-health healing, extreme integers, fractional configuration, small health totals, pre-deadline blocking, and the exact recovery boundary.
  - _Boundary: Character Rules_
  - _Depends: 1.1, 1.2_
  - _Requirements: 3.3, 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 5.1, 5.2, 5.3, 5.4, 8.6_

- [x] 4.2 (P) Persist mutation identities and replay equality
  - Record globally unique operation identities with their character, kind, amount, and applied revision in the caller transaction.
  - Distinguish matching replay from changed-input reuse, retain receipts across restart, and avoid consuming identities for rejected or rolled-back work.
  - Cover accepted no-op receipts and prohibit cleanup that could re-enable old mutations.
  - Work only on receipt persistence and its isolated fixtures; do not edit pending health rules or shared service files.
  - Done when real-storage tests accept matching receipts, reject conflicting reuse, and prove rollback/reopen semantics independently of pending health transition code.
  - _Boundary: Character Store and Service_
  - _Depends: 1.1, 1.3, 3.1_
  - _Requirements: 7.1, 7.2, 7.4, 7.6_

- [x] 4.3 Normalize recovery and expose effective eligibility
  - Extend existing-character creation/inspection and transaction-bound lookups to normalize an expired incapacitation exactly once using the stored deadline and recovery-health amount.
  - Return current snapshots and distinguish missing, active, and incapacitated characters without creating missing characters.
  - Use one clock reading per operation and preserve recovery semantics before/at/after the deadline and after restart.
  - Done when repeated/concurrent observations recover once, later damage is not undone, absence remains absence, and no worker or manual recovery command is needed.
  - _Boundary: Character Store and Service_
  - _Depends: 3.1, 4.1_
  - _Requirements: 1.2, 5.2, 5.3, 5.4, 5.5, 5.6, 6.1, 6.2, 6.3, 7.1, 7.6, 8.6_

- [x] 4.4 Apply replay-safe health changes within gameplay transactions
  - Validate mutations and check receipt equality before applying state transitions; normalize current state for accepted new operations and matching replay.
  - Reject missing targets, invalid amounts, pending incapacitation, and conflicting operation reuse without partial character or receipt effects.
  - Commit health change, zero-health incapacitation/recovery snapshot, and successful receipt together in the supplied transaction.
  - Return current normalized state plus the original applied revision for matching replay; keep successful no-ops idempotent without unnecessary revision changes.
  - Done when damage/healing clamp correctly, defeated characters remain blocked until due, and replay never reapplies damage, extends incapacitation, or restores health a second time.
  - _Boundary: Character Store and Service_
  - _Depends: 4.2, 4.3_
  - _Requirements: 4.1, 4.2, 4.3, 4.4, 4.5, 4.6, 4.7, 5.1, 5.5, 6.4, 6.5, 7.2, 7.3, 7.4, 7.6_

- [x] 5. Integrate development probes and coordinated gameplay effects
- [x] 5.1 Add authorized self-only health demonstrations
  - Implement development damage and defeat commands using the same actor/ephemeral validation as personal inspection.
  - Target only the requesting player and use update-derived, namespaced operation identities; route effects through the character service.
  - Preserve automatic creation for valid first-use development commands and return current incapacitation/recovery state after rejected actions.
  - Done when command tests demonstrate damage and defeat, reject target spoofing and malformed amounts, and prove duplicate commands cannot repeat health changes or bypass recovery.
  - _Boundary: Character Verification_
  - _Depends: 3.3, 4.4_
  - _Requirements: 1.1, 1.6, 2.5, 2.6, 4.4, 4.6, 7.2, 7.6, 8.3, 8.6_

- [x] 5.2 Integrate strict development mode at command composition
  - Parse the explicit development flag with a false default and safe rejection of invalid values before runtime side effects.
  - Select short-duration development tuning and install development routes/declarations only while enabled; keep production behavior independent of runtime transport smoke mode.
  - Include flag examples and safe test-group reproduction commands with the executable change.
  - Done when enabled startup exposes only the self-only probes, disabled startup omits them and uses production tuning, and invalid flag values exit before storage/network.
  - _Boundary: Runtime Integration, Character Verification_
  - _Depends: 3.4, 5.1_
  - _Requirements: 3.2, 3.3, 8.3, 8.4, 8.5_

- [x] 5.3 Verify atomic coordination with other gameplay state
  - Use test-owned battle/inventory fixtures to combine character changes and receipt writes with unrelated gameplay writes in one caller transaction.
  - Inject failure before and after receipt insertion and at commit; assert no partial character, fixture, receipt, or completed-update visibility.
  - Confirm results remain tentative until the transaction owner succeeds and feature operations never acquire a separate transaction.
  - Done when successful coordination commits together and every injected failure leaves the preceding durable state and operation-identity availability intact.
  - _Boundary: Character Verification_
  - _Depends: 4.4_
  - _Requirements: 4.4, 6.4, 6.5, 7.4, 8.6_

- [x] 5.4 Verify the health-and-recovery milestone
  - Exercise authorized damage, defeat, blocked pre-deadline actions, exact partial recovery, and inspection using a composed runtime and injected character clock.
  - Retain transport wall-clock authorization semantics; use controlled character time in automated tests rather than changing the system clock or sleeping through recovery.
  - Reopen before/after the deadline and verify original recovery timing/amount and absence of passive regeneration.
  - Record live-group observations with the short development duration and reproducible restart steps separately from fake API evidence.
  - Done when automated flows pass and the live demonstration records partial recovery and restart behavior; report manual verification required and leave this gate incomplete if live access is unavailable.
  - _Boundary: Character Verification_
  - _Depends: 5.2, 5.3_
  - _Requirements: 2.2, 2.3, 4.1, 4.4, 4.6, 4.7, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 7.1, 8.3, 8.5, 8.6_

## Milestone 3: Replay and Release Verification

- [x] 6. Validate failure, replay, privacy, and release behavior
- [x] 6.1 Verify concurrent transitions and later-state replay
  - Race creation, distinct valid health operations, and recovery observations against isolated real SQLite state.
  - Replay operations after restart, subsequent damage, and subsequent defeat; verify matching replay reports current state and changed-input reuse is rejected before transitions.
  - Change creation/recovery tuning between runs and prove stored attributes and established incapacitation outcomes remain stable.
  - Done when race-detector tests show one owner, serializable accepted mutations, and no duplicate damage, deadline extension, or extra recovery health.
  - _Boundary: Character Verification_
  - _Depends: 4.4_
  - _Requirements: 1.3, 1.4, 3.4, 5.5, 5.6, 7.1, 7.2, 7.3, 7.6, 8.6_

- [x] 6.2 Verify storage failures and cancellation preserve runtime classification
  - Inject query execution, row-scan, iteration, close, lock, and cancellation failures through character storage operations.
  - Assert safe wrappers preserve inspectable storage causes while never exposing SQL, supplied data, or raw driver text.
  - Exercise dispatch rollback and durable-completion behavior so retryable failures do not become false terminal success.
  - Done when fault tests retain the previous durable character/receipt state, return safe errors, and preserve the runtime's retry/cancellation semantics.
  - _Boundary: Character Verification_
  - _Depends: 4.4, 5.3_
  - _Requirements: 7.4, 8.1, 8.2, 8.6_

- [x] 6.3 Verify post-commit delivery failure and personal-data isolation
  - Reject or expire restricted delivery after a successful character transaction, then inspect with a new valid command.
  - Assert identity, health, and receipt state remain committed and no public/private fallback is attempted.
  - Populate sensitive command/profile/response fixtures and exercise malicious arguments, bot senders, and anonymous/chat senders.
  - Done when safe diagnostics/probes contain no sensitive fixtures, actor isolation holds, and later inspection shows the committed state without resetting it.
  - _Boundary: Character Verification_
  - _Depends: 5.4_
  - _Requirements: 1.2, 1.6, 2.5, 2.6, 7.5, 8.1, 8.2, 8.6_

- [x] 6.4 Verify development-to-production transitions and runtime compatibility
  - Restart with development mode disabled, replace the managed menu, and prove cached development commands have no registered owner.
  - Confirm original production command behavior, runtime smoke opt-in behavior, startup readiness ordering, and preserved queued updates.
  - Exercise additive schema upgrade/rollback refusal and include compatible backup/restore and menu-scope limitations with the release artifacts.
  - Done when normal mode exposes no health-control routes/declarations, future incapacitations use production tuning, and existing runtime regressions still pass.
  - _Boundary: Character Verification_
  - _Depends: 5.2_
  - _Requirements: 2.1, 3.2, 3.3, 7.1, 7.4, 8.4, 8.5, 8.6_

- [x] 6.5 Complete release quality gates and the final runnable demonstration
  - Run canonical formatting, full tests, vet, lint, race, runtime smoke, and character executable integration checks through devenv.
  - Confirm production dependency boundaries, migration compatibility, absence of unintended dependency/lock changes, and automated isolation from real Telegram.
  - Record actual commands/results and the final live-group demonstration, distinguishing outstanding manual evidence from completed automated checks.
  - Done when all prescribed checks and milestone evidence are recorded; otherwise identify failed/unrun gates and retain manual verification required instead of claiming feature completion.
  - _Boundary: Character Verification_
  - _Requirements: 1.1, 2.1, 3.5, 4.1, 5.3, 6.4, 6.5, 7.1, 7.4, 8.1, 8.2, 8.4, 8.5, 8.6_

## Planning Notes

- The creation and health service extensions are sequential because they share service/store ownership. Presentation may overlap creation persistence, and receipt persistence may overlap pure health rules, only within the marked peer groups.
- Each milestone's demonstration and reproduction artifacts are completion criteria of its verification task, following project steering; they are not separate deployment or documentation-only tasks.
- Live Telegram interaction is not performed during specification generation. Missing live evidence keeps the relevant milestone and feature verification incomplete.
- Task-plan review gate: PASS. Task-graph sanity review: PASS, independent review; the existing-character recovery path was clarified from a nonblocking review note.
- Final quick-spec sanity review: PASS, independent review of requirements, design, and tasks directly from disk.

## Implementation Notes

- Implemented with three parallel agents under the user's explicit authorization; shared service and composition edits stayed sequential, with independent review before recording completion.
- All 26 subtasks are complete. On 2026-10-27 the owner reported "It works well" after receiving live test instructions and explicitly requested "close all gates". Tasks 3.5, 5.4, and 6.5 are closed on that owner acceptance and passing automated evidence; no assistant-observed live results are claimed. See validation.md and manual-verification.md.
- Core product.md, tech.md, and structure.md steering files were absent; approved specs and existing development-workflow steering supplied project context.
- Row scan/iteration/close failure tests exposed a runtime-owned classification gap. The generic runtimecontract.StorageFailure marker preserves retries without introducing runtime-to-character imports; upstream runtime validation records revalidation.
- Runtime transport authorization always uses wall time; only character-domain time is controlled in health/recovery integration tests.
- Fake long-poll fixtures must read request bodies and have explicit shutdown signaling. Final fresh canonical checks passed after correcting an intermittent fixture shutdown hang; interrupted earlier runs are not passing evidence.
- Final canonical formatting, full tests, vet, lint (0 issues), race, production-binary smoke, and build all passed through devenv. No dependency, lock, pin, or migration-0001 changes were needed.
