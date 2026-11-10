# Tasks

Sections 1–7 and the historical verification results record the completed initial
implementation. References there to destructive rejection, uncraftable variants,
and rejection caching describe the earlier behavior and are superseded by the
user’s success-only creative-generation revision in section 8. Current behavior
is defined by the updated proposal, design, and delta specification.

## 1. Integration baseline and recipe rules

- [x] 1.1 Re-read the current `add-item-components` artifacts and implementation, confirm material membership and snapshot compatibility are available, and preserve unrelated work. Verify the component and snapshot tests pass before adding crafting behavior; document any prerequisite still missing.
- [x] 1.2 Implement versioned, order-independent recipe identity under `internal/modules/crafting`. Verify adjacent tests cover quantity sensitivity, absolute quantities without ratio normalization, identical snapshots split across entries, equivalent individual instances, changed snapshot fields, collision-safe identity comparison, and exclusion of user/group/entry identities.
- [x] 1.3 Implement input and outcome validation with strict success/uncraftable variants and supported item components. Verify tests cover generated materials, composite and empty output components, legacy nonmaterial inputs, foreign entries, duplicate allocations, invalid/overflow quantities, missing bilingual fields, unknown components/properties, invalid rarity/mode, and excessive item text.

## 2. Durable recipes, attempts, and inventory effects

- [x] 2.1 Allocate the next unused SQLite migration and add recipes, attempt confirmations/results, and recoverable generation jobs. Verify migration and reopened-database tests preserve old ownership, canonical snapshots, shop records, and reservations and enforce one authoritative outcome and one attempt per confirmation.
- [x] 2.2 Implement recipe publication and job/attempt persistence with guarded state transitions and collision-safe canonical lookup. Verify tests cover simultaneous discovery, late conflicting outcomes, saved rejection reuse, claim expiry, stale tokens, and unchanged terminal outcomes after restart.
- [x] 2.3 Add transaction-bound exact ingredient consumption using inventory rules and existing available quantities. Verify SQLite tests cover partial stacks, whole entries, individual materials, duplicate references, shop-reserved units, and atomic rollback after injected failures; no reserved units can be consumed or their entry deleted.
- [x] 2.4 Implement atomic attempt fulfillment with one output on success and no output on rejection. Verify success and initial/repeated rejection each consume exactly the selected inputs, output stacking preserves complete snapshots, unavailable inventory consumes nothing, output failure rolls back inputs, and terminal attempt replay changes nothing.

## 3. Application coordination and recovery

- [x] 3.1 Add explicit repository, unit-of-work, and generator ports and coordinate confirmation, known-recipe resolution, and unknown-recipe jobs under `internal/application/crafting`. Verify deterministic fake-generator tests prove known successes and rejections bypass AI, many ingredient types are accepted, and concurrent attempts converge on one durable outcome.
- [x] 3.2 Implement confirmation idempotency independent of update IDs. Verify tests cover duplicate transport delivery, distinct button presses for one token, distinct new confirmations, foreign/expired sessions, and pending accepted attempts surviving menu expiry.
- [x] 3.3 Implement recoverable work claims and technical failure transitions. Verify tests with an injected clock cover interrupted generation, crash after outcome persistence but before fulfillment, pending attempt recovery, stale worker results, no automatic endless retries, and technical failure without cached rejection or material loss.

## 4. AI adapter and configuration

- [x] 4.1 Select a locally running inference runtime/model and document its local endpoint/API mapping to the existing generator contract, including optional authentication, concrete deadline, response/token bounds, and request-rate/resource limits. Verify the documented contract can express both complete generated definitions and explicit uncraftable outcomes without hosted inference; revisit the design before coding if it cannot.
- [x] 4.2 Implement the local model-service adapter using bounded context-aware HTTP and strict response decoding, with no hosted fallback. Verify local fake-server tests cover valid variants, optional authentication/service errors, local service unavailability, timeout, cancellation, oversized/trailing/ambiguous output, model refusal, malformed JSON, and prompt data containing instruction-like text. Verify only the configured local endpoint receives inference requests; automated tests require neither model weights nor real credentials.
- [x] 4.3 Add validated `REPLIPG_` configuration and documented placeholders to `.env.example`, with discovery enablement, local endpoint/model settings, and instructions for a separately operated local model service in README.md. Verify config tests cover disabled discovery, required local settings when enabled, optional authentication without a hosted API key, invalid bounds, and diagnostics that do not echo secrets; verify only definitions and quantities enter model requests and saved recipes remain usable when the local service is down.

- [x] 4.4 Add a separately operated Docker Compose local model service with persistent weights, a health-gated model pull, localhost binding, and overridable image/model/port settings. Document startup, inference checks, cleanup, and the pending bot integration. Validate default and overridden Compose configuration and this OpenSpec change; live model inference remains a manual check requiring downloaded weights.

## 5. Localized crafting interface

- [x] 5.1 Add matching flat English/Russian crafting keys, schema requirements, and Go locale validation. Verify missing keys reject candidate assets and both complete locale sets pass CUE validation; keep corresponding grouping and order without repeated language blocks.
- [x] 5.2 Implement opener-scoped paged material selection, accumulated selections, review, and single-use confirmation sessions, adapting the shop editor to one `1`, `-10`, `-1`, `+1`, `+10`, localized Max row for stackable materials. Extract configurable quantity-choice construction if needed without importing shop application rules or changing shop controls. Verify adjacent tests cover exact button order, reset, one/ten-unit steps, clamping and overflow-safe increments, Max using available units, sale reservations and zero availability, individual instance selection without quantity controls, separate Remove, preserved edits across Back/paging, more than two material types, nonmaterial filtering, stale/foreign callbacks, cancellation without mutations, and exact confirmation binding; retain shop limit/layout regression coverage if shared code changes.
- [x] 5.3 Implement localized pending, success, rejection, unavailable-inventory, disabled-service, and technical-failure views. Verify both languages, safe formatting, saved bilingual item text, and explicit material-loss warnings for unknown and already rejected recipes before confirmation.
- [x] 5.4 Route `/craft` and callbacks without changing ordinary inventory navigation. Document recursive material crafting, global quantity-sensitive outcomes, repeated rejection destruction, technical failures, and pending-inventory conflicts in README.md; verify route/view tests match the documented flow.

## 6. Runtime worker and projections

- [x] 6.1 Wire typed crafting owner operations and a single application-owned generation worker, leaving entry points thin. Verify deterministic lifecycle tests show slow AI does not block unrelated gameplay, provider calls occur outside SQL transactions/runtime ownership, disabled discovery still allows saved recipes, and shutdown cancels and joins the worker with durable recovery.
- [x] 6.2 Reconcile inventory projections and invalidate active views after committed crafting. Verify integration tests cover removed entries, partial stacks, output grants, technical failure and rejection feedback, projection/delivery failure recovery without repeated mutation, and current group language on later renders.

## 7. Integration verification

- [x] 7.1 In the repository devenv shell, copying `.env.example` only if `.env` does not exist, format changed Go/CUE files with `gofmt -w` and `cue fmt`. Run `go test ./...`, `go vet ./...`, `go build ./...`, `golangci-lint run ./...`, `go test -race ./...` where supported, `cue vet -c=false ./...`, and `cue vet -c ./assets/items ./assets/locales`; record actual results and blocked checks. Run `go mod tidy` if imports or dependencies change.
- [x] 7.2 Run `openspec validate add-ai-crafting --strict` and compare implementation with every acceptance scenario, including cross-group restart reuse, cached destructive rejections, recursive materials, shop races, and at-most-once recovery. Mark tasks complete only for implemented and verified behavior; archiving remains a separate user-requested action.

## Historical verification results (initial implementation; superseded rejection behavior)

- Preserved the pre-existing Compose, environment example, README, and planning
  edits. The existing `.env` was not copied, modified, or exposed. Component and
  SQLite snapshot baseline tests passed before crafting implementation.
- Used the repository devenv shell and its pinned Go toolchain. Formatted changed
  Go/CUE files and ran `go mod tidy`; no dependency or Go version changes resulted.
- Passed: `go vet ./...`, `go build ./...`, `golangci-lint run ./...` (zero issues),
  `cue vet -c=false ./...`, and `cue vet -c ./assets/items ./assets/locales`.
- Ran `go test ./...` and `go test -race ./...`. All crafting, adapter, persistence,
  menu, localization, runtime, and other package checks passed except the three
  pre-existing `internal/app` fixture tests already documented in the archived
  component change: `TestItemsOwnerOnlyNavigationAndLanguage`,
  `TestItemsButtonsGrantToOwnerAndProtectReplays`, and
  `TestShopReferenceLayoutAcrossBuySellAndOrders` (English/Russian subtests).
  Those expect additional catalog pages or missing `iron_sword` assets. Preserved
  their unrelated fixtures; neither full run reported a race.
- Passed focused crafting integration race checks, including nonblocking slow
  generation, cancellation/join at shutdown, confirmation replay, destructive
  cached rejection, technical failure without loss, active inventory updates,
  failed Telegram delivery followed by successful replay-safe delivery,
  projection reconstruction after an injected read failure without repeated
  consumption, and current-language pending/results after a group language change.
- Persistence tests verify migration from schema 4 without rewriting snapshots,
  shop orders, or reservations; collision-safe canonical lookup; shared outcomes
  across groups/restarts; competing discovery and stale claims; explicit retry;
  recursive and individual materials; reserved/partial stack consumption;
  output stacking; and rollback/recovery after an injected output failure.
- Local fake HTTP servers verify complete supported generated variants and
  technical failures without weights or credentials. Live Ollama inference and
  model quality remain manual deployment checks. Discovery defaults to disabled;
  saved outcomes remain available without the service.
- Passed `openspec validate add-ai-crafting --strict` and `git diff --check`.
  All acceptance scenarios were reviewed; the change remains unarchived.

## 8. Success-only creative generation revision

- [x] 8.1 Replace gameplay uncraftable decisions with a success-only generation contract in domain, application, local adapter, and prompt. Encourage imaginative, unusual, funny, and recognizable hybrid items without an RPG or fantasy theme restriction, including Fumo plus Neco-Arc producing Fumo-Neco-Arc. Verify valid unusual recipes generate one item; former uncraftable responses and other technical errors preserve ingredients and do not create a negative recipe.
- [x] 8.2 Add `006_crafting_success_only.sql` to retire cached uncraftable recipe outcomes and reset associated generation jobs to pending with stale claims cleared while preserving canonical recipe identities, successful definitions, inventory, reservations, and historical attempts. Verify an upgraded formerly rejected recipe is undiscovered for a new confirmation, saved successes remain reusable, and old terminal rejected confirmations cannot consume or grant again, and any already accepted pending attempts remain recoverable.
- [x] 8.3 Update matching English/Russian locale schemas, keys, views, and README to explain that successful crafting creates one item and consumes the selected materials, technical errors preserve ownership, and creative hybrid results are unrestricted by RPG themes. Remove new gameplay-rejection presentation and destructive-rejection warnings. Verify both languages, scoped confirmation, saved bilingual output, and current-language feedback.
- [x] 8.4 Format changed Go/CUE files in the repository devenv shell and run appropriate tests, race tests, vet, build, lint, and both CUE validation commands. Run `openspec validate add-ai-crafting --strict` and review all updated acceptance scenarios, including migration and replay safety. Record actual revision results and blocked or pre-existing failures separately from historical checks; do not archive.

## Success-only revision verification

- Replaced the model prompt and structured response schema with creative,
  success-only generation, including a Fumo/Neco-Arc hybrid example. Invalid
  legacy uncraftable responses now fail technically without consuming inputs.
- Migration 006 retires cached rejections and resets their work claims. Tests
  verify unchanged historical attempt bytes, ownership, and successful recipes,
  no repeated effects from old confirmations, and successful rediscovery.
- Updated matching bilingual review text and README; historical rejected attempt
  rendering remains accurate. Existing successful recipes retain their outputs.
- In devenv, passed adapter/domain/SQLite/UI/localization tests and crafting
  integration race tests, `go vet ./...`, `go build ./...`,
  `golangci-lint run ./...` (zero issues), `cue vet -c=false ./...`, and
  `cue vet -c ./assets/items ./assets/locales`. Formatted changed Go/CUE files.
- `go test ./...` still fails only the same three unrelated catalog fixture tests
  listed above. No new crafting failures or races were reported. Live local model
  inference and qualitative creativity remain manual checks requiring weights.
- Passed strict OpenSpec validation and `git diff --check`; no archive performed.

## Prompt grounding correction

- Removed the concrete character-hybrid example from the generation prompt after
  it biased a stick plus Neco-Arc result. The prompt now requires recognizable
  contributions from every supplied ingredient and excludes unrelated named
  characters, brands, or items as implied inputs. Updated README and design.
- At the user's request, backed up the local database and reset exactly the
  cached one-stick plus one-Neco-Arc recipe to undiscovered. Verified ownership,
  snapshots, historical attempts, reservations, and other recipes/jobs unchanged
  in the same transaction, restored the outcome immutability trigger, and passed
  SQLite integrity and foreign-key checks. This is a local maintenance operation,
  not a schema migration or a global cache reset.
- Passed adapter tests and vet in devenv. Restart the running bot before making
  the next new confirmation so generation uses the corrected prompt.

## NVIDIA GPU deployment update

- Enabled NVIDIA GPU device reservations for the Compose inference service and
  updated README/environment guidance. Default and overridden Compose settings
  passed `docker compose config --quiet`.
- Recreated the local Ollama service with GPU access and confirmed container
  health, NVIDIA RTX 4070 visibility, and CUDA detection. A real local
  `qwen3.5:9b` inference returned `OK`; `ollama ps` reported `100% GPU` with an
  8192-token context. Both downloaded models remained available.
- Passed strict OpenSpec validation and `git diff --check`. No Go source changed.

## 9. Expand the crafting material catalog

- [x] 9.1 Add sixteen stackable material definitions spanning wood, minerals, metal, textiles, and mechanical parts, with matching English/Russian names and grounded descriptions. Use existing catalog grants to acquire the new materials.
- [x] 9.2 Format and validate the complete CUE assets, run Go checks in devenv, and strictly validate this change. Record results and any existing failures.

Material catalog verification:

- Added sixteen stackable materials with matching English/Russian text. Existing
  catalog grants provide access; shop offers and existing definitions are unchanged.
- Stabilized the catalog navigation/reload tests with an explicit two-page fixture
  so additions to shipped assets cannot invalidate page-local button indexes.
- In devenv, formatted CUE and the changed Go test; catalog/crafting tests,
  `go vet ./...`, `go build ./...`, `golangci-lint run ./...` (zero issues),
  `cue vet -c=false ./...`, and complete item/locale CUE validation passed.
- Ran `go test ./...`: all packages passed except the existing English/Russian
  shop-layout cases, which require the missing `iron_sword` fixture definition.
- Strict change validation and `git diff --check` passed. No archive performed.

## Simple item generation refinement

- Updated the prompt to favor familiar items from ordinary materials, natural
  names of usually 1-4 words, and 1-2 short concrete description sentences.
  Creative hybrids remain allowed when the ingredients naturally support them;
  quantities and rarity do not imply grandeur or unsupported magical powers.
- Updated README, design, and the delta specification. Existing cached recipes
  are retained; restart the bot to apply the prompt to new discoveries.
- Adapter tests and vet passed in devenv. The first test run hit the existing
  timing-sensitive HTTP timeout assertion; the immediate rerun passed without
  any adapter logic or test changes.

## 10. Fresh progressive crafting review

- [x] 10.1 Remove previous experimental prompts, generated fixtures/results/reports, and the two old opt-in live-test scenarios while preserving ordinary tests and unrelated work. Keep one current review plan and runner.
- [x] 10.2 Generate three fresh quantity-aware progressive variants on current catalog inputs and newly generated intermediates. Compare stages, readiness, and property emergence; preserve every response and blocked step in a readable Russian report.
- [x] 10.3 Format and verify the new opt-in runner in devenv; run Go tests, vet, build, lint, complete CUE checks, and strict change validation. Document real results and limitations without switching production generation.

Current review results:

- Deleted the preceding experiment directories, generated snapshot fixtures,
  baseline prompt fixture, and `crafting_live_test.go` /
  `crafting_progression_live_test.go`. Replaced them with one current review
  directory and `crafting_review_test.go`; ordinary deterministic tests remain.
  Removed obsolete runner instructions and artifact references. Earlier detailed
  experiment notes were replaced by this current record at the user's request.
- Created A (staged processing), B (functional readiness), and C (property
  interaction), each with separate quantity budgets and the existing supported
  output contract. Eleven recipe steps per variant/seed include fresh textile
  and mechanism chains, 1/2/5 leather, abundant/scarce textile components, and
  unusual developed ingredients. Source definitions are captured from current
  CUE assets or unedited preceding outputs, never previous experimental fixtures.
- Initial requests exposed ingredient lists incorrectly inserted inside material
  markers. Clarified the shared marker contract and reran the complete suite;
  production validation was not relaxed and responses were not repaired.
- Final local `qwen3.5:9b` run: 66 accepted responses, zero rejected responses,
  zero blocked steps; 188.6 seconds. Temperature 0.8, context 8192, output limit
  2048, thinking disabled, seeds 17/42. Current JSONL and Russian report preserve
  every final response, including awkward wording and semantic defects.
- Report generation checked completeness, unique recipe/seed/prompt records,
  and exact deep equality of each reused generated snapshot and quantity.
  Quantity comparisons share an intermediate within each variant/seed, while
  cross-variant comparisons are of independently developed chains. The report
  states this distinction and does not equate structural validity with quality.
- Semantic weaknesses remain: premature finished objects, repeated nearly
  identical intermediates, ignored distinctive ingredients, unsupported
  properties, and uneven quantity effects. No candidate is automatically
  selected for production. Gameplay recipes and inventory were untouched.
- In devenv: formatted Go, tidy (no dependency changes), ordinary adapter tests,
  vet, build, lint (zero issues), both CUE checks, and strict change validation
  passed. Full `go test ./...` still fails only in the existing English/Russian
  shop-layout fixtures missing `iron_sword`. No production concurrency changed.
  Whitespace checks passed. Stopped test Ollama after generation; no archive.

## 11. Adopt selected variant A

After the equipment and fictional-material experiments, the user explicitly
selected this variant and requested production adoption with an English prompt.

- [x] 11.1 Use the exact reviewed A prompt for production generation, keeping transport, response validation, inference settings, one-item yield, and saved recipes unchanged. Align README and planning artifacts with staged processing and independent quantity budgets.
- [x] 11.2 Verify the production prompt matches the reviewed text, run Go tests, vet, build, lint, and strict change validation in devenv, and record results and any pre-existing failures without archiving.

Adoption results:

- Replaced only the production system prompt with the exact selected English A
  text. Byte comparisons matched both reviewed copies, including the trailing
  newline. No new qualitative wording or recipe-specific examples were added.
- Updated README, proposal, design, and the delta specification for staged
  processing, separate material budgets, continued crafting eligibility, and
  empty markers. Marked the review reports with the user's selection; their
  raw historical results and authored fictional fixtures remain unchanged.
- Existing response validation, inference settings, IDs, output count, and saved
  definitions are unchanged. Restart the bot for new discoveries to use A;
  successful known recipes do not regenerate. No game database writes occurred.
- In devenv: formatted Go; adapter tests, vet, build, lint (zero issues), both
  CUE checks, and strict change validation passed. Full `go test ./...` passed
  all packages except the pre-existing English/Russian shop-layout cases missing
  `iron_sword`. No imports, dependencies, or concurrent behavior changed, so no
  tidy or race rerun was needed. Whitespace checks passed; no archive.

## 12. Fictional material experiment

- [x] 12.1 Add isolated, validated fictional material fixtures with explicit properties and limitations; preserve the exact reviewed A prompt and production catalog.
- [x] 12.2 Generate equipment-oriented chains and 1/2/5-panel quantity cases with two seeds, reusing actual generated snapshots. Separately test two explicitly authored prepared parts without substituting them for generated intermediates.
- [x] 12.3 Save every result in a readable Russian report, verify intermediate identity, run appropriate Go/CUE/OpenSpec checks, and record semantic limits accurately.

Fictional experiment results:

- Added six handwritten fictional base materials and two separately labelled
  prepared-part controls under `experiments/fictional-materials/`. New fixture
  support is confined to the opt-in review runner; CUE assets, gameplay storage,
  inference settings, and production prompt remain unchanged. A is byte-for-byte
  identical to the selected reviewed text.
- Generated 24 main results (78.2 seconds) plus two prepared-part controls
  (6.3 seconds), using local qwen3.5:9b, temperature 0.8, context 8192, output
  limit 2048, thinking disabled, seeds 17/42. All 26 passed production response
  validation. The report preserves both languages in raw records and unedited
  Russian names/descriptions in readable tables. All generated-source references
  were verified for deep snapshot equality and matching quantities.
- Observed protective panels, an elbow guard, and shoulder armor. Raw-material
  weapon chains remained supports or reinforcement parts. The prepared insert,
  grip, and resin yielded a complete cutter in one seed, but the other still
  demanded fastening despite supplied resin. Quantities were inconsistently
  reflected; one five-panel input was described as four panels. These are
  exploratory outcomes, not a guarantee of useful equipment or material costs.
- The runner accepts exactly one catalog, fixture, or previous-result source per
  input. Fixture snapshots must validate and be material eligible. Separate
  experiment directories preserve the previous user-reviewed A/B/C collection.
- In devenv: formatted Go, tidy (no dependency changes), adapter tests, vet,
  build, lint (zero issues), complete CUE validation, and strict change validation
  passed. Full Go tests still fail only in existing English/Russian shop-layout
  fixture cases missing `iron_sword`. No production concurrency changed. Test
  Ollama was stopped after generation; no archive or production adoption.

## Archive verification (2026-11-10)

- Synced the AI crafting delta to `openspec/specs/ai-crafting/spec.md` and
  verified every requirement and scenario before archiving. Archived the
  completed menu redesign alongside this change and synced its focused spec.
- Updated the opt-in review runner and README experiment paths to the archive.
- Verified an isolated snapshot of the exact staged crafting changes, excluding
  unrelated widget menus, Telegram dependency upgrades, and permission changes.
  In the repository devenv shell, build, vet, lint (zero issues), and both CUE
  validation commands passed. Full ordinary and race test runs failed only in
  the existing English/Russian shop-layout cases missing `iron_sword`; every
  other test passed and no races were reported. No live inference was run.
- Strict validation passed for both changes before archive and for main specs
  after sync. All 36 tracked tasks were complete at archive.
