# Tasks

## 1. Integration baseline and recipe rules

- [ ] 1.1 Re-read the current `add-item-components` artifacts and implementation, confirm material membership and snapshot compatibility are available, and preserve unrelated work. Verify the component and snapshot tests pass before adding crafting behavior; document any prerequisite still missing.
- [ ] 1.2 Implement versioned, order-independent recipe identity under `internal/modules/crafting`. Verify adjacent tests cover quantity sensitivity, absolute quantities without ratio normalization, identical snapshots split across entries, equivalent individual instances, changed snapshot fields, collision-safe identity comparison, and exclusion of user/group/entry identities.
- [ ] 1.3 Implement input and outcome validation with strict success/uncraftable variants and supported item components. Verify tests cover generated materials, composite and empty output components, legacy nonmaterial inputs, foreign entries, duplicate allocations, invalid/overflow quantities, missing bilingual fields, unknown components/properties, invalid rarity/mode, and excessive item text.

## 2. Durable recipes, attempts, and inventory effects

- [ ] 2.1 Allocate the next unused SQLite migration and add recipes, attempt confirmations/results, and recoverable generation jobs. Verify migration and reopened-database tests preserve old ownership, canonical snapshots, shop records, and reservations and enforce one authoritative outcome and one attempt per confirmation.
- [ ] 2.2 Implement recipe publication and job/attempt persistence with guarded state transitions and collision-safe canonical lookup. Verify tests cover simultaneous discovery, late conflicting outcomes, saved rejection reuse, claim expiry, stale tokens, and unchanged terminal outcomes after restart.
- [ ] 2.3 Add transaction-bound exact ingredient consumption using inventory rules and existing available quantities. Verify SQLite tests cover partial stacks, whole entries, individual materials, duplicate references, shop-reserved units, and atomic rollback after injected failures; no reserved units can be consumed or their entry deleted.
- [ ] 2.4 Implement atomic attempt fulfillment with one output on success and no output on rejection. Verify success and initial/repeated rejection each consume exactly the selected inputs, output stacking preserves complete snapshots, unavailable inventory consumes nothing, output failure rolls back inputs, and terminal attempt replay changes nothing.

## 3. Application coordination and recovery

- [ ] 3.1 Add explicit repository, unit-of-work, and generator ports and coordinate confirmation, known-recipe resolution, and unknown-recipe jobs under `internal/application/crafting`. Verify deterministic fake-generator tests prove known successes and rejections bypass AI, many ingredient types are accepted, and concurrent attempts converge on one durable outcome.
- [ ] 3.2 Implement confirmation idempotency independent of update IDs. Verify tests cover duplicate transport delivery, distinct button presses for one token, distinct new confirmations, foreign/expired sessions, and pending accepted attempts surviving menu expiry.
- [ ] 3.3 Implement recoverable work claims and technical failure transitions. Verify tests with an injected clock cover interrupted generation, crash after outcome persistence but before fulfillment, pending attempt recovery, stale worker results, no automatic endless retries, and technical failure without cached rejection or material loss.

## 4. AI adapter and configuration

- [ ] 4.1 Select a locally running inference runtime/model and document its local endpoint/API mapping to the existing generator contract, including optional authentication, concrete deadline, response/token bounds, and request-rate/resource limits. Verify the documented contract can express both complete generated definitions and explicit uncraftable outcomes without hosted inference; revisit the design before coding if it cannot.
- [ ] 4.2 Implement the local model-service adapter using bounded context-aware HTTP and strict response decoding, with no hosted fallback. Verify local fake-server tests cover valid variants, optional authentication/service errors, local service unavailability, timeout, cancellation, oversized/trailing/ambiguous output, model refusal, malformed JSON, and prompt data containing instruction-like text. Verify only the configured local endpoint receives inference requests; automated tests require neither model weights nor real credentials.
- [ ] 4.3 Add validated `REPLIPG_` configuration and documented placeholders to `.env.example`, with discovery enablement, local endpoint/model settings, and instructions for a separately operated local model service in README.md. Verify config tests cover disabled discovery, required local settings when enabled, optional authentication without a hosted API key, invalid bounds, and diagnostics that do not echo secrets; verify only definitions and quantities enter model requests and saved recipes remain usable when the local service is down.

## 5. Localized crafting interface

- [ ] 5.1 Add matching flat English/Russian crafting keys, schema requirements, and Go locale validation. Verify missing keys reject candidate assets and both complete locale sets pass CUE validation; keep corresponding grouping and order without repeated language blocks.
- [ ] 5.2 Implement opener-scoped paged material selection, accumulated selections, review, and single-use confirmation sessions, adapting the shop editor to one `1`, `-10`, `-1`, `+1`, `+10`, localized Max row for stackable materials. Extract configurable quantity-choice construction if needed without importing shop application rules or changing shop controls. Verify adjacent tests cover exact button order, reset, one/ten-unit steps, clamping and overflow-safe increments, Max using available units, sale reservations and zero availability, individual instance selection without quantity controls, separate Remove, preserved edits across Back/paging, more than two material types, nonmaterial filtering, stale/foreign callbacks, cancellation without mutations, and exact confirmation binding; retain shop limit/layout regression coverage if shared code changes.
- [ ] 5.3 Implement localized pending, success, rejection, unavailable-inventory, disabled-service, and technical-failure views. Verify both languages, safe formatting, saved bilingual item text, and explicit material-loss warnings for unknown and already rejected recipes before confirmation.
- [ ] 5.4 Route `/craft` and callbacks without changing ordinary inventory navigation. Document recursive material crafting, global quantity-sensitive outcomes, repeated rejection destruction, technical failures, and pending-inventory conflicts in README.md; verify route/view tests match the documented flow.

## 6. Runtime worker and projections

- [ ] 6.1 Wire typed crafting owner operations and a single application-owned generation worker, leaving entry points thin. Verify deterministic lifecycle tests show slow AI does not block unrelated gameplay, provider calls occur outside SQL transactions/runtime ownership, disabled discovery still allows saved recipes, and shutdown cancels and joins the worker with durable recovery.
- [ ] 6.2 Reconcile inventory projections and invalidate active views after committed crafting. Verify integration tests cover removed entries, partial stacks, output grants, technical failure and rejection feedback, projection/delivery failure recovery without repeated mutation, and current group language on later renders.

## 7. Integration verification

- [ ] 7.1 In the repository devenv shell, copying `.env.example` only if `.env` does not exist, format changed Go/CUE files with `gofmt -w` and `cue fmt`. Run `go test ./...`, `go vet ./...`, `go build ./...`, `golangci-lint run ./...`, `go test -race ./...` where supported, `cue vet -c=false ./...`, and `cue vet -c ./assets/items ./assets/locales`; record actual results and blocked checks. Run `go mod tidy` if imports or dependencies change.
- [ ] 7.2 Run `openspec validate add-ai-crafting --strict` and compare implementation with every acceptance scenario, including cross-group restart reuse, cached destructive rejections, recursive materials, shop races, and at-most-once recovery. Mark tasks complete only for implemented and verified behavior; archiving remains a separate user-requested action.
