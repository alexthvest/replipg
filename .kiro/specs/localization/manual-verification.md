# Localization verification

## Automated reproduction

From the repository root, run:

```sh
./scripts/check
./scripts/smoke
devenv shell -- go test -count=1 ./internal/localization ./internal/settings ./internal/persistence
devenv shell -- go test -count=1 -run Localization ./cmd/replipg
```

These commands use fake Telegram boundaries and do not require bot credentials.
Record actual command results separately from live observations.

## Live prerequisites

Use a dedicated Telegram test bot and two isolated test groups. Give the bot
administrator status: reliable `getChatMember` verification of other users requires
bot administrator privileges. Give one human administrator status and leave a
second human as an ordinary member. Configure the ignored local `.env` according
to README.md. Never include credentials, private replies, or raw API responses in
shared diagnostics or evidence. Run only one active replica using local SQLite
storage. Do not use the development database for production verification.

The application manages default-language `all_group_chats` commands plus each
managed group's default-language chat scope and its `en`/`ru` chat variants.
Per-member, administrator, and other-language scopes remain operator owned.
Remove competing overrides deliberately if a client shows another menu.

## Milestone 1: settings and characters

1. Start `./scripts/run`. Select ephemeral `/settings` from the menu as each
   human. A new group starts in English. Inspection alone creates no character
   or inventory. Ordinary typed `/settings` has no personal response.
2. The ordinary member sees read-only language information. The administrator
   sees `English` and `Русский` controls. Select Russian as the administrator:
   confirmation is Russian and reports pending menu synchronization until the
   worker succeeds. The complete group's command descriptions become Russian.
3. Select `/character`. Its notice, state, labels and recovery guidance are
   Russian. Inspect the second group: it remains English for the same human.
4. Stop with SIGTERM and restart against the same storage directory. Reopen
   settings and character: Russian remains selected. Losing actor administrator
   status prevents the next change. Removing bot privileges gives read-only
   verification-unavailable guidance; ordinary gameplay remains translated.
5. Reopen settings after five minutes: old controls give reopen guidance. Test
   another actor/group's controls: no selection changes. A failed personal
   confirmation has no public/private-chat fallback and does not undo a save.

## Milestone 2: inventory and enabled probes

1. Open `/inventory` in each group. Names, descriptions, category/effect labels,
   quantities, slot labels and controls use that group's language. Historical
   `cloth_armor` is displayed as leather armor; item IDs and mechanics persist.
2. Open an item, equip it, then change language using fresh settings. Interact
   with the previously opened inventory: the resulting edited view uses the
   current language and preserves quantities, ownership and equipped state.
   Already sent views remain unchanged until an accepted interaction edits them.
3. Enable development flags individually in ignored `.env` and restart. Check
   character damage/defeat, inventory grant/heal, and public/restricted smoke
   probes in both groups. Disable flags and restart: their routes and menu entries
   disappear. Smoke public receipts never echo incoming personal message text.

## Milestone 3: recovery and rollback

1. Temporarily remove the bot's ability to publish chat command menus. Change
   language while actor verification remains available: the saved language stays
   selected and settings reports pending synchronization. Restore permission:
   retry clears pending without another selection.
2. Restart while publication is pending and repeat with changed development
   flags. The newest complete enabled menu converges to the saved language.
3. Stop the single replica before backing up the entire SQLite directory,
   including sidecars. Migration 0004 is additive; a schema-3 binary refuses a
   schema-4 database. Roll forward or restore a matching stopped whole-directory
   backup and binary. Restore externally managed Telegram menus deliberately.

## Actual live evidence

Status: CONFIRMED by the user on 2026-10-28.

After receiving the live testing instructions, the user reported “All works
well” and explicitly requested “mark manual verification as confirmed”. This
user-reported confirmation closes manual milestone gates 3.8, 4.4 and 5.3.

The agent did not independently operate Telegram. No per-step logs, screenshots,
group identifiers or additional observations were supplied; the evidence is the
user's overall confirmation. Automated results remain recorded separately in
validation-report.md.
