# Bilingual inventory sharing milestone

This is the inventory reproduction companion to
[the sharing verification record](manual-verification.md). Task 4.3 supplies
production-composition automated evidence and runnable instructions. Task 4.4
is the separate mandatory human-client observation checkpoint. Passing fake
Telegram checks does not complete task 4.4 or establish feature-level live
readiness.

## Automated reproduction and observed results

From the repository root, without Telegram credentials:

```sh
devenv shell -- go test ./cmd/replipg -run TestSharingInventoryMilestone -count=1
devenv shell -- gofmt -l cmd/replipg/sharing_inventory_milestone_test.go
devenv shell -- go vet ./cmd/replipg
devenv shell -- golangci-lint run ./cmd/replipg/...
devenv shell -- go test -race ./cmd/replipg -run TestSharingInventoryMilestone -count=1
```

`TestSharingInventoryMilestone/en` and `/ru` construct actual `productionOptions`,
isolated migrated SQLite, the registered sharing ledger, outbox delivery worker,
sharing projector and group-menu worker. A local HTTP Telegram fake captures
ordinary `sendMessage`, restricted sends and `editEphemeralMessageText`; it
rejects unexpected methods. No real Telegram messages are sent.

The test opens inventory, selects starter item 4 (Stick / Палка), equips,
unequips and goes Back. Every response stays restricted to its actor; browsing
queues no public snapshot. It explicitly shares a page, blocks an immediate
second press with personal cooldown guidance, then deliberately shares it again.
Both public copies contain the exact original body and numeric-owner attribution
without navigation, equipment, Share buttons or a reply target.

The test simulates elapsed cooldown by updating only the isolated test database's
successful-view timestamp to 31 seconds ago. It does not sleep, add runtime clock
configuration, or claim to verify the exact boundary. Fake-clock service tests
(`TestShareRefreshCooldownAndRetry`) cover 29.999 seconds and exactly 30 seconds.

In both language directions, an administrator switches language through actual
settings controls. Fresh inventory has the current Share label. Sharing an old
item detail preserves its original body and attribution language; pending,
success and cooldown guidance use the current group language. The normal member
has no language controls and cannot apply another actor's control. Global and
localized group command declarations remain the three ephemeral commands
`character`, `inventory`, `settings`; settings has no Share. Captured character,
inventory, quantity, equipment and operation records stay unchanged by sharing
and language changes.

Observed automated results on 2026-10-28:

| Check | Result |
| --- | --- |
| Focused milestone command above | PASS, English and Russian |
| Formatting command above | PASS, no output |
| Command-package vet and lint above | PASS |
| Focused milestone race check above | PASS, English and Russian |
| Temporary Go overlay disabling the sharing cooldown guard | Expected FAIL in both languages: `double tap was not personally suppressed` |
| Initial test-fixture run | Failed because the fake omitted `editEphemeralMessageText`; corrected fake method/boolean reply, then passed |

The overlay sensitivity check changed only a temporary file outside the
repository. No production implementation or runtime flags changed for this
verification task. The initial fixture failure is not a behavioral RED claim.
Full repository gates and independent review are recorded by the parent workflow
separately.

## Live prerequisites

Use the test deployment setup in [README](../../../README.md), an isolated
Telegram test group, a dedicated test bot, one human administrator and one
ordinary human member. Give the bot administrator privileges for reliable
membership verification. Use a client that supports ephemeral menu commands
and callback source messages. A typed ordinary command has no incoming ephemeral
reply authorization: select `/inventory` and `/settings` from the group command
menu throughout these steps. Disable development flags for the baseline run.
Keep tokens, personal content, raw API bodies and group/user identifiers out of
shared evidence.

Build and start one replica with its ignored local `.env` and isolated local
SQLite directory, following README:

```sh
./scripts/build
./scripts/run
```

Use a clean starter inventory for predictable numbered item positions. Starter
item 4 is Stick in English / Палка in Russian. If the test inventory differs,
select the number displayed beside that item. Inventory controls keep their
existing five-minute lifetime; finish each flow within that time or reopen
`/inventory`. Share adds no separate expiry.

## English flow

1. As administrator, select `/settings`, then `English`. Confirm English settings
   and, after synchronization, English descriptions for `character`, `inventory`
   and `settings`. Names stay unchanged; all three commands remain ephemeral.
   Settings must have no Share button. As ordinary member, select `/settings`:
   read-only language information must have no change controls.
2. As the inventory owner, select `/inventory`. Verify the numbered page and
   `Share` are personal. Select item `4` (Stick), `Equip`, `Unequip`, then `Back`.
   Observe equipment changes only in the personal menu; no public snapshot may
   appear from any of these actions. Verify quantities are unchanged.
3. On the personal page, press `Share`. Expect a new ordinary public message in
   this same group containing the exact displayed page plus owner attribution.
   It must have no numbered, Back, Equip, Unequip, navigation or Share controls.
   The original personal menu remains usable. Expect personal pending guidance
   followed by `Shared in this group.` when delivery is confirmed.
4. Immediately press that same page's `Share` again. Expect only personal
   `You can share again in … seconds.` guidance and no second public copy.
   Wait at least 30 seconds from confirmed publication, then deliberately press
   `Share` on the same usable view again. Expect a second identical public copy;
   the first stays present and unchanged.
5. Reopen `/inventory`, select item `4`, then press its `Share`. Expect the exact
   Stick detail as an ordinary read-only message, with attribution, and no
   equipment controls. Neither sharing the detail nor resharing the page may
   change health, quantities or equipment.

## Russian flow

1. As administrator, select `/settings`, then `Русский`. Verify Russian settings
   and synchronized Russian command descriptions. As ordinary member, inspect
   `/settings`: language selection remains administrator owned. No Share appears
   on settings; development commands remain absent with their flags disabled.
2. Select `/inventory`; expect Russian item names and `Поделиться`. Select `4`
   (Палка), `Экипировать`, `Снять`, then `Назад`. Browsing and equipment operations
   stay personal and never create public snapshots.
3. Press `Поделиться` on the page. Expect its exact Russian body with Russian
   numeric-owner attribution as a normal group message without controls. Expect
   personal pending guidance and `Опубликовано в этой группе.` on confirmed
   success. Press again immediately: expect personal
   `Можно поделиться снова через … сек.` and no additional public copy.
4. Wait at least 30 seconds from confirmed publication; deliberately press the
   same `Поделиться` again before the source expires. Both copies must remain
   unchanged. Reopen `/inventory`, select `4`, and share the item detail. Confirm
   the same exact-content, attribution and control-free behavior.

## Language switch with an old displayed detail

Run this once from English to Russian and again from Russian to English:

1. Open `/inventory` and select item `4`. Leave that personal detail displayed
   without navigating or equipping again; record its language locally.
2. In a separate settings interaction, the administrator selects the other
   language (`Русский` or `English`). Do not interact with the old inventory
   controls while switching. Open a fresh `/inventory`: its Share label must
   now be `Поделиться` or `Share` in the new group language.
3. Press the old detail's original Share button. The public snapshot body and
   attribution must retain the old language. New pending/success guidance must
   use the current language. An immediate second press must give current-language
   cooldown guidance without a second public message. All earlier public copies
   remain unchanged after language switching or later equipment browsing.
4. Reopen settings as the ordinary member. They must still have no change
   controls; verify the language has not changed. Confirm command names/scopes
   and ephemeral visibility remain as before sharing.

## Live observation evidence — task 4.4

Status: **NOT PERFORMED**. This environment has no connected human Telegram
client. The agent has not deployed this flow, operated a client, sent real
messages or observed live outcomes. Prior localization/inventory confirmations
are not evidence for this new sharing milestone.

| Human-client observation | English | Russian |
| --- | --- | --- |
| Personal page/detail browsing and equipment changes | NOT PERFORMED | NOT PERFORMED |
| Explicit page/detail ordinary control-free publication | NOT PERFORMED | NOT PERFORMED |
| Personal double-tap cooldown and deliberate repost retaining copies | NOT PERFORMED | NOT PERFORMED |
| Old body/attribution language with current personal guidance | NOT PERFORMED | NOT PERFORMED |
| Administrator settings authority and unchanged command visibility | NOT PERFORMED | NOT PERFORMED |

After a human performs the flow, record the date, observed outcomes and any
failures here without credentials or personal content. Task 4.4 remains unchecked
until those actual observations are supplied.
