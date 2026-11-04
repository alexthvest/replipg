> Historical note (2026-11-04): The custom checker in `internal/architecture` has been retired. References below to its source, allowlists, fixtures, commands, or results describe historical work and are no longer implementation or validation requirements. Package dependency guidance still applies.

> Split verification (2026-11-04): automated checks and independent reviews passed; live observations remain pending. Final scripts/check and scripts/smoke exited 0. Enable inventory probes only with a positive REPLIPG_OWNER_TELEGRAM_USER_ID and authorize numeric owner identity before all development behavior/output. Existing owner observations below are historical and do not prove this split. Repeat grant/heal as owner and reject non-owner/admin use; see ../item-system/manual-verification.md for /dev_item, disabled/config and zero-state checks. Preserve all original pending live gates.

# Inventory System Verification

## Evidence status

Live Telegram verification is **PARTIALLY OWNER-CONFIRMED**. The owner reported successful manual validation after checking personal inventory output, the equipped weapon bonus, and unchanged character base stats. No live Telegram messages were sent or directly observed by the agent. Automated tests use temporary storage and fake Telegram. Never use the workspace `data/` directory for fixtures.

The supplied observations confirm numbered personal inventory rendering, the equipped weapon marker and +1 attack bonus, an empty armor slot, and character base attack/defense remaining 10/5. The owner subsequently confirmed the checked behavior works as expected. This record intentionally omits usernames and raw personal response content. Other checklist scenarios lack individual reported outcomes, so the full milestone gates remain **MANUAL_VERIFY_REQUIRED**.

This document separates reproduction instructions from observed evidence. Automated and live milestone results must be recorded below after they actually run.

## Content and rollout

The embedded `internal/item/items.json` with separate inventory starter configuration defines Stick (+1 attack), Cloth armor (+1 defense), Healing potion (10 HP), and inert Coin. Each character receives one Stick, one Cloth armor, three potions and ten Coins on first authorized inventory opening. Equipment starts unequipped. Coins are ordinary stacked items with no spending or currency semantics.

Saved item categories and numeric mechanics are immutable. Display text may change; new mechanics require a new identity while preserving all old definitions. Changing the starter kit affects only characters whose starter entitlement remains pending. Startup catalog preparation registers definitions without granting items or creating inventory roots.

Migration 0003 is additive. Stop the single active replica before backing up the entire storage directory, including SQLite sidecars. A version-2 binary rejects a version-3 database. Roll forward with compatible catalog content, or restore a compatible stopped whole-directory backup with the matching binary. Restore external Telegram command-menu settings deliberately when reverting binaries. Catalog failure after migration leaves the schema upgrade applied and blocks update admission.

## Automated reproduction

From the repository root, use the pinned environment:

```sh
./scripts/check
./scripts/smoke
devenv shell -- go test -count=1 ./internal/inventory/...
devenv shell -- go test -count=1 -run TestInventory ./cmd/replipg
```

## Milestone 1: starter kit and personal inspection

Use a dedicated test bot and group with persistent local storage configured as described in README. Select `/inventory` from Telegram's ephemeral group command menu. A typed ordinary command has no incoming ephemeral authorization and must not initialize state or send a personal response.

1. Open inventory as a new actor. Confirm the compact personal owner header, numbered starter quantities, empty equipment slots and zero bonuses.
2. Open again in the same group and another group. Confirm no extra kit. Repeat with an existing damaged/incapacitated character; inspection must preserve its character state except due recovery through the character service.
3. Select numbered Coin and potion details. Confirm descriptions, owned quantities, Back only, and the potion's battle-only wording. Inspect weapon and armor details and navigate Back.
4. Restart the process and inspect again. Confirm the same quantities, assignments and starter entitlement.
5. Have another actor press a control and try the original actor's control in another group. Confirm no personal inventory disclosure or mutation. After five minutes, old controls must require reopening inventory.

Owner-reported live result: **personal inventory rendering confirmed**. Starter uniqueness, cross-group continuity, navigation and restart outcomes were not individually reported; the full gate remains **MANUAL_VERIFY_REQUIRED**.

## Milestone 2: equipment

### In-place navigation change

Open `/inventory`, note its message position, and select an item, Back, and any
available pagination buttons. Equip and Unequip equipment. Confirm every button
updates the same personal message, with current buttons and no additional
messages. Opening `/inventory` again creates a separate view. After five minutes,
press an old control and confirm that view shows reopen guidance with no buttons.
Check an unused stale equipment control after changing equipment in another view;
its original view must show reopen guidance and lose its buttons. Other actors
must still receive no inventory disclosure or mutation from foreign controls.

Live evidence for this change: **MANUAL_VERIFY_REQUIRED**. No live Telegram
messages were sent or observed by the agent for this change.

1. Equip the Stick from its detail view. Confirm the summary marker and +1 attack. Inspect `/character`; base attack remains unchanged.
2. Unequip and confirm zero attack bonus without losing ownership. Equip armor and confirm +1 defense; base defense remains unchanged.
3. With a second weapon supplied through the opt-in grant probe, replace the weapon. Confirm one assignment and unchanged total quantities.
4. Open two inventory views in different groups, apply one equipment change, then try an unused control from the older revision. Confirm rejection. Retry an already accepted control while it remains valid; it must show current state without reapplying the old assignment.
5. Restart and confirm assignments and quantities persist.

Owner-reported live result: **equipped weapon marker and +1 attack bonus confirmed; character base stats correctly unchanged**. Remaining equipment checklist outcomes were not individually reported; the full gate remains **MANUAL_VERIFY_REQUIRED**.

## Milestone 3: coordinated healing and disabled probes

Set `REPLIPG_DEV_INVENTORY=true` and, independently, `REPLIPG_DEV_CHARACTER=true` in the ignored local `.env`; restart. Never put credentials into tracked files or command arguments.

1. Select `/dev_inventory_grant coin 2` from the ephemeral menu; confirm only the requester receives two ordinary Coins.
2. Select `/dev_character_damage 20`, then `/dev_inventory_heal healing_potion`. Confirm ten HP restored and exactly one potion spent.
3. At full HP, request healing. Confirm no potion is spent. Defeat the actor with `/dev_character_defeat` and try before its recovery deadline; confirm incapacitation rejection without spending.
4. After the character recovery deadline, inspect/heal and confirm normalization and healing respect the same saved character. Exhaust the final potion and confirm its ownership row disappears without zero quantity.
5. Restart with both development flags false. Confirm probe menu entries disappear and stale development commands do not mutate state; normal `/inventory` and equipment remain available.
6. During all steps, confirm personal responses have no public or private-chat fallback. Reopen after rejected delivery to inspect committed state.

Live result: **individual healing and disabled-probe checklist outcomes not reported; MANUAL_VERIFY_REQUIRED**.

## Automated evidence

The following commands ran successfully against the final implementation on 2026-10-28. All exited 0. Tests used isolated temporary storage and fake Telegram; no live Telegram demonstration occurred.

| Command | Observed result |
|---|---|
| `./scripts/check` | Formatting clean; all 19 packages pass normal and race suites; vet passes; lint reports zero issues. Some package test results were cached, as printed by Go. |
| `./scripts/smoke` | Production executable boots, serves health, processes fake Telegram, commits state, and shuts down successfully. |
| `./scripts/build` | Production executable builds successfully. |
| `devenv shell -- go test -count=1 -run '^TestInventory' ./cmd/replipg` | Seven matching top-level inventory tests pass uncached, including actual executable inspection, pagination, equipment, healing, disabled probes, composition, and runtime privacy coverage. |
| `devenv shell -- go test -count=1 ./internal/architecture` | All three top-level architecture tests pass uncached. |

Independent reviewers approved the automated inspection/fault gate, equipment milestone tests, coordinated character/inventory action tests, executable healing lifecycle, concurrency, privacy, and final design/requirements alignment. Additional focused race checks passed for repeated concurrent healing and inventory controls. No failures or skips were reported in the final commands.

The actual executable scenarios establish starter uniqueness across actors/groups/restart, numbered navigation and pagination, owner-label fallback, Coin/potion read-only details, equipment replacement and later-state replay, coordinated healing and rejection without spending, expired controls across restart, and removal of disabled probe routes/menu entries. Runtime privacy tests separately reject restricted delivery after committed grants/healing and confirm state on subsequent inspection without fallback or duplicate effects.

Storage fault verification covers query, scan, iteration, close, contention, cancellation, receipt writes, savepoint cleanup, and outer commit. A discovered callback cleanup-classification issue was reproduced through the real dispatcher, repaired, and independently reviewed: storage failures produce no response, terminal update record, cursor advancement or partial effects, and the same update remains retryable.

These automated results close software verification only. Partial owner-reported live confirmation is recorded above; remaining checklist evidence leaves task gates 3.3, 4.5 and 7.4 unchecked. See `validation-report.md` for the feature-level assessment.
