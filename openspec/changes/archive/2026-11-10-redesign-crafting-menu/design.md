# Design

## Context

See proposal.md for motivation and specs/crafting-menus/spec.md for behavior. Existing crafting orchestration combines callbacks and rendering in `internal/app/crafting.go`. `CraftSelection` stores entry IDs and quantities and invalidates frozen confirmation on mutation. Selecting a material currently mutates it immediately. Session Actions already bind opaque callbacks to message, opener, and render-specific actions. The durable attempt retains allocations and output snapshots, while fulfillment discards the returned inventory grant IDs.

This spans views, sessions, application navigation, localization, and tests, so a design is useful. `add-ai-crafting` is implemented but unarchived; this change builds on it without editing its artifacts. The new focused menu capability refines that change's flow, retaining its quantity controls and all crafting domain invariants.

## Goals / Non-Goals

**Goals:** Model composition and draft independently; build exact Telegram button rows; share existing inventory navigation and ordered live delivery; reconstruct repeats from saved attempts and current ownership.

**Non-Goals:** New generation workers, durable menu sessions, recipe changes, new dependencies or changes to `cmd/replipg` startup. Existing durable fulfillment remains the only writer of crafting inventory changes.

## Decisions

### 1. Separate composition, browser, editor draft, review, and attempt screens

Retain runtime-owner ownership of session state. Add crafting-specific state for separate composition and browser pages, editor origin, and draft entry ID and quantity. Keep the associated immutable item snapshots in an application-owned map keyed by session token and entry ID; this preserves the repository boundary that menus cannot import feature modules. Pass those snapshots explicitly to views and clear the map on session expiry or transition to inventory. Keep the accepted attempt token distinct from the next composition. Mutation methods invalidate pending review tokens; draft stepping does not change the saved composition. Back restores origin and page. This is clearer than overloading generic `Session.Quantity` to remember unrelated page and draft state.

Keep snapshot data for selections whose inventory entry vanishes; they remain removable and visible. Revalidate on Save, review, final confirmation, and fulfillment. A refresh must not clamp a draft silently; it reports reduced availability until the user revises it. Step buttons calculate within current valid bounds, and unavailable ingredients offer removal or Back.

### 2. Build keyboard rows explicitly

Replace action-kind adjacency grouping in crafting rendering with an explicit row description bound through the existing session action generator. The composition renders numbered choices, optional pagination, Add / Clear, then Confirm / Close. Reuse six-entry pagination and preview limits. Do not build a generic keyboard framework or change inventory layout. No disabled button styling is required: invalid Confirm returns localized feedback.

```text
composition --Add--> browser --number--> editor draft
composition --number-----------------> editor draft
editor draft --Add/Save/Remove--------> composition
editor draft --Back------------------> origin screen
composition --Confirm--> review --final confirm--> attempt
attempt success --Repeat-------------> restored composition
attempt success --New craft----------> empty composition
attempt success --Use in crafting----> composition with output x1
attempt failure --Retry--------------> fresh review or shortage
```

### 3. Resolve repeat allocations without changing recipe identity

Restore saved attempt allocations in their original order. Prefer still-available original entries, then allocate matching complete snapshots across current entries in acquisition order, excluding sale reservations. Keep exact required totals; retain unresolved requested amounts as shortage rows if matching ownership is insufficient. Never substitute merely matching catalog IDs. Use overflow-safe aggregation consistent with recipe validation. Individual entries remain exact instance selections once resolved; consumed original instances can be replaced by other identical saved instances when preparing a new attempt.

Use in crafting resolves one available entry matching the saved outcome by the same complete-snapshot rule. The existing attempt lacks output grant IDs; snapshot matching avoids a database migration and is appropriate because matching stacks merge. It may select another identical owned instance rather than identify the historical instance uniquely. No item mutation occurs until final confirmation.

### 4. Reuse inventory navigation and close through session delivery

Inventory transitions the same bound menu to ordinary inventory navigation for the same actor, character, and group, with inventory dependencies. Clear stale crafting reviews and editor state on transition. Close first stages a final localized closed view without buttons, then retires the session after that view is delivered; reuse delivery lifecycle machinery or a small close-after-delivery flag. Removing the session before its final edit would prevent queued delivery from running. Failed final edits use bounded existing retry behavior; closing state rejects actions and background refresh cannot restore controls.

Closing does not call attempt cancellation. Durable work and shutdown ownership remain with the existing worker. No extra goroutines or external service calls are introduced. Pass caller contexts to reads, return contextual errors, and leave retriable persistence failures without partially committed composition edits.

### 5. Keep result details and language consistent

Render consumed ingredients from immutable attempt allocations rather than current inventory. Page long result ingredient lists separately from material browsing. All labels, feedback, empty states, shortage fields, and new controls use matching English/Russian flat keys. Saved item names and descriptions remain literal spans with bounded previews. Accepted pending attempts automatically update through existing inventory/attempt projection and session delivery.

### Project structure plan

```text
cmd/replipg/                              existing startup, unchanged
internal/ui/menus/crafting.go             composition IDs, draft and page state
internal/ui/menus/crafting_test.go        draft/commit/back, removal, quantity boundaries
internal/ui/menus/sessions.go             crafting state and close lifecycle if needed
internal/ui/menus/*_test.go               closing and stale callback coverage
internal/ui/views/crafting.go             composition, browser, editor, result and shortage text
internal/ui/views/crafting_test.go        pagination, saved snapshots, bilingual rendering
internal/app/crafting.go                  transitions, snapshot retention and validation
internal/app/crafting_views.go            explicit keyboard rows and screen rendering
internal/app/crafting_repeat.go           exact-snapshot repeat allocation
internal/app/crafting_menu_test.go        result actions, pages, refresh and close journeys
internal/app/crafting_repeat_test.go      allocation, reservations and snapshot isolation
internal/app/crafting_routes_test.go      complete menu journeys and result actions
internal/app/crafting_test.go             adapt existing helpers and review scenarios
internal/app/crafting_projection_test.go retain atomic result/projection regression coverage
internal/app/inventory.go                 reuse bound inventory navigation
internal/app/menu_updates.go              closed-menu lifecycle and refresh preservation if needed
internal/app/menu_updates_integration_test.go  close/refresh ordering regressions
internal/modules/localization/crafting.go register new required text keys
assets/locales/{schema,en,ru}.cue          matching localized controls and feedback
README.md                                revised crafting interaction
```

Domain rules, SQLite schemas, AI adapters, and CUE item definitions need no changes. Repeat-resolution logic belongs with menu preparation in application orchestration, not the AI domain. Add adjacent focused tests if extracted into a separate crafting helper file.

## Risks / Trade-offs

- Long selections exceed message limits -> page composition, review, result, and shortage details using bounded item previews.
- Inventory changes during a draft -> preserve user input, show shortage, and validate before every acceptance boundary.
- Stale accepted confirmation arrives after Repeat -> reuse its durable attempt only; never reinterpret it as authorizing the new composition.
- Close races background delivery -> publish a terminal desired revision and retire only after terminal delivery, rejecting callbacks immediately.
- Matching output by snapshot does not identify a unique historical instance -> use only currently available matching ownership; expose no historical-instance claim.
- Original AI crafting UI clauses remain in its unarchived delta -> explicitly retain this change's dependency and apply the focused menu contract after that baseline; archive only on request.

## Migration Plan

Implement on top of current crafting files and verify all existing crafting invariants. No database or configuration migration is required. Ephemeral sessions already expire across restart, so deploy with normal worker shutdown and durable attempt recovery. Rollback restores prior menu behavior without altering durable recipes or attempts. Do not archive or sync either change as part of implementation.
