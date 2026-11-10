# Proposal

## Why

Crafting currently opens the material browser, immediately adds an ingredient when inspected, and offers only Refresh after completion. Players need a clear composition screen, deliberate quantity edits, and useful next actions after crafting within Telegram's numbered inline-button interface.

## What Changes

### Confirmed requirements

- Open `/craft` on the selected composition, with up to six numbered ingredient buttons and separate pagination.
- Place Add / Clear in one row, followed by Confirm / Close in another.
- Add opens an inventory-style material browser with six numbered choices per page; selecting a material opens quantity editing. Composition numbers open that same editor.
- Commit edits explicitly with Add or Save; Back discards the pending edit. Offer a separate Remove action for existing ingredients.
- Keep a final composition review before submitting a durable attempt.
- Provide pending, success, technical-error, and shortage screens. Success offers Repeat, New craft, Inventory, Close, and Use in crafting when the result is a material. Repeat and retry require renewed confirmation.

### Implementation defaults

- Number positions locally on each page. Six is a page size, not a recipe limit.
- Keep Confirm visible for an empty or invalid composition, but acknowledge it with actionable feedback without accepting an attempt; Telegram has no disabled callback button in the current adapter.
- Keep selected quantities unchanged when availability decreases and display shortages. Clear resets the composition immediately; Close ends the menu without cancelling an accepted attempt.
- Restore repeats against current ownership using identical saved snapshots and exact quantities. Missing individual instances may be replaced only by currently owned instances with identical snapshots; insufficient materials stay visibly unresolved.
- Use in crafting selects one currently available result matching the saved output. It does not grant another item or submit a new attempt.

### Non-goals and unresolved decisions

No changes to AI prompts, recipe identities, yield, consumption, reservation rules, or generation workers; no recipe encyclopedia or Mini App. No unresolved product decisions are required to implement the defaults above.

## Capabilities

### New Capabilities

- `crafting-menus`: Telegram composition, material selection, draft quantity editing, confirmation, and post-attempt navigation.

### Modified Capabilities

None. `ai-crafting` currently exists only in the completed but unarchived `add-ai-crafting` change. This change depends on that implementation and refines its UI contract: drafting replaces immediate selection mutation, and composition becomes the entry screen. Its exact quantity controls and explicit confirmation requirements remain applicable. General inventory and live menu contracts remain applicable without changes.

## Impact

Update crafting orchestration and inventory navigation under `internal/app`, ephemeral session state under `internal/ui/menus`, crafting views under `internal/ui/views`, localization keys and English/Russian assets, adjacent tests, and README usage. Reuse durable attempts, snapshots, inventory reads, and existing ordered menu delivery. No new dependencies, persistence migrations, configuration, or startup wiring are expected. Preserve the existing working-tree changes and leave other OpenSpec changes untouched.
