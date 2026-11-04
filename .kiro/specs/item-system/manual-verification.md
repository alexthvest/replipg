# Item System Manual Verification

Status: **MANUAL_VERIFY_REQUIRED**. No live Telegram observations have been performed for this split.

Automated status on 2026-11-04: independent extraction/operator reviews approved; `./scripts/check` and `./scripts/smoke` exited 0. Executable fake-Telegram tests verify item categories, owner and administrator/non-owner rejection, disabled mode, zero feature-state writes and ordinary non-owner inventory access. These results do not replace the live steps below.

Use a dedicated test bot, group/supergroup and local persistent volume. Set REPLIPG_DEV_INVENTORY=true and REPLIPG_OWNER_TELEGRAM_USER_ID to the positive numeric owner ID. Existing runtime/localization prerequisites must be configured. Select ephemeral group commands; ordinary typed commands lack incoming ephemeral authorization. Do not use the live workspace database for automated fixtures.

1. As owner invoke `/dev_item healing_potion`, `/dev_item stick`, `/dev_item cloth_armor` and `/dev_item coin`. Expect localized name, stable ID, category and respectively 10 HP, +1 attack, +1 defense or no usable mechanics. No character, starter grant, ownership, equipment, health, receipt or control changes occur.
2. Invoke `/dev_item`, extra arguments and an unknown ID. Expect safe localized usage/unknown guidance, no mutation and no public/private fallback.
3. As a non-owner, including a group administrator, attempt all three development commands. Expect no development behavior/output or mutation. Numeric owner identity alone authorizes development use.
4. As owner use `/dev_inventory_grant coin 1` and `/dev_inventory_heal healing_potion` on your own character. Grant changes only your quantity once per accepted update; healing uses one potion and heals at most 10 HP, rejecting full health/incapacitation without spending. Existing separately gated character probes can prepare health; their behavior is unchanged. No foreign target is accepted.
5. Open `/inventory`, equip and navigate. Expect existing quantities, assignments, callback binding/expiry and in-place ephemeral behavior. Restart and confirm continuity. Switch group language and confirm translated probe/item UI with identical mechanics.
6. Disable REPLIPG_DEV_INVENTORY and restart. All three inventory/item development routes/menu entries must disappear; normal inventory and independent runtime/character modes remain available.
7. In an isolated deployment, missing, zero, negative or malformed owner configuration with inventory development enabled must fail before storage/network activity.

Record actual owner/non-owner output and unchanged-state evidence separately. Automated fake-Telegram checks do not satisfy this live gate; inventory's historical incomplete gates remain incomplete.
