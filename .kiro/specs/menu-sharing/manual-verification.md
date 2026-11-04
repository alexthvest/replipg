# Menu sharing verification

## Scope and evidence status

This is the character milestone reproduction record and sharing evidence index.
Task 3.3 covers automated verification and these runnable instructions. Task 3.4
is a separate mandatory live character checkpoint. No live sharing observation
has been performed or supplied in this run; fake Telegram is not live evidence.
Do not transfer earlier character, inventory or localization acceptance to sharing.

| Check | Evidence | Status |
|---|---|---|
| Production character journey, task 3.3 | `TestSharingCharacterMilestone` with isolated SQLite, real production registration, Telegram callback decoder and local fake HTTP | Automated PASS, 2026-10-28 |
| Exact before/at/after 30-second policy | `TestShareRefreshCooldownAndRetry` and sharing projector tests using injected time | Existing automated coverage; rerun with commands below |
| Character client visibility, no username, double tap, retained copies | Human Telegram client in dedicated group, task 3.4 | LIVE NOT PERFORMED |
| Long-lived character source without new Share expiry | Human Telegram client, same displayed source after more than five minutes | LIVE NOT PERFORMED |
| Bilingual inventory milestone | [Inventory reproduction and evidence](inventory-manual-verification.md), tasks 4.3 and 4.4 | See separate record; no live result inferred |
| Repository and recovery/retention gates | Final task 5.3 and its recorded command output | Separate final verification; not implied by this focused test |

The character production test sends no real Telegram messages. It verifies
private open for a user without a username, explicit ordinary public sends,
blocked distinct double taps while pending, cooldown guidance after success,
later character damage and browsing without publication, excluded settings and
development menus, a deliberate second share of the original body, unchanged
gameplay on Share, and failed personal view delivery without a public fallback.
The fake rejects edit/delete methods, and exactly two ordinary sends remain.

Production composition uses real time. The test deterministically makes the
pending projection due and ages the isolated persisted successful-share time
by 30 seconds instead of sleeping. It also ages the character view's creation
time by 24 hours while retaining the same source capability. This verifies
eligibility and lack of a creation-age expiry through production composition;
the precise millisecond boundary is verified by the separate injected-clock
tests, not by a wall-clock timing claim.

## Automated reproduction

No bot credential is needed and no external Telegram service is contacted:

```sh
devenv shell -- go test ./cmd/replipg -run TestSharingCharacterMilestone -count=1
devenv shell -- go test ./internal/sharing -run 'TestShareRefreshCooldownAndRetry|TestProjector' -count=1
```

Expected: passing tests. The production test uses temporary storage and a local
HTTP fake; it does not use the deployment database or alter `.env`.
For repository gates use the established workflows, recording their results
separately rather than assuming success from this focused command:

```sh
./scripts/check
./scripts/smoke
```

## Isolated live setup

Use a dedicated test bot, a dedicated Telegram group, and a human client that
supports the existing ephemeral commands and callback source messages. Follow
[README setup](../../../README.md) with the ignored `.env`. Set the storage path
to a new absolute directory, keep credentials local, and leave
`REPLIPG_DEV_SMOKE=false` so transport probes cannot add unrelated public posts.
Use a human account without a Telegram username for the attribution check.
Do not run a second replica with the same bot or storage.

After creating the ignored environment file as described in README:

```sh
devenv shell
mkdir -p -- "$REPLIPG_STORAGE_DIR"
chmod 700 -- "$REPLIPG_STORAGE_DIR"
exit
./scripts/build
./scripts/run
```

In another terminal, confirm startup readiness:

```sh
./scripts/probe readyz
```

Expected: HTTP 200. Keep health access private and apply the existing bot group
access setup. Do not paste credentials, raw updates, source bodies, usernames or
personal screenshots into this evidence record. Record only safe observations
and timings. Stop with Ctrl-C after verification.

## Character client journey and expected observations

1. Select ephemeral `/character` from the group command menu. Confirm that only
   the requesting actor sees the summary and Share control. Have a second human
   confirm no public snapshot appeared. An ordinary typed `/character` has no
   restricted authorization and must not create a public fallback.
2. Press Share. Confirm a new ordinary group message contains the displayed
   summary plus numeric owner attribution, including for the no-username actor.
   Confirm it has no Share, navigation or other interactive buttons and no
   ephemeral receiver restriction. Pending/success guidance is personal.
3. Immediately press the same source's Share again. Confirm no extra public
   copy and personal pending or remaining-seconds guidance. Check from a second
   human client as well as the actor's client.
4. Wait at least 30 seconds after the confirmed first public post, then press
   Share on the same displayed source. Confirm a second ordinary copy appears
   with the same body and attribution. Both earlier and later copies remain;
   neither is edited, replaced or deleted. Do not use client timing to claim
   an exact millisecond boundary; automated tests establish that boundary.
5. Open `/character` again and inspect `/settings`. Neither action publishes a
   snapshot; settings has no Share control. In an isolated test deployment,
   optionally enable `REPLIPG_DEV_CHARACTER=true` locally and restart. Select
   `/dev_character_damage 1`, then `/character`: the development response has no
   Share, and the newly inspected character has changed HP. Existing public
   copies remain unchanged. Once eligible, sharing the original visible source
   still copies its original displayed values and does not change gameplay.
   Restore the development flag to false and restart after this check.
6. Keep a usable character summary open for more than five minutes. Press its
   Share after any cooldown. It should remain shareable without reopening;
   sharing adds no separate character timer. A source the client no longer
   provides as accessible cannot establish this check: record it as unverified.
7. Personal delivery rejection has deterministic fake coverage above. If the
   live environment cannot safely reproduce unsupported personal delivery,
   explicitly leave that live check unperformed. Never infer fallback behavior
   from a successful response, or intentionally expose a private body publicly
   to simulate failure.

Inventory source controls retain their own five-minute expiry. Follow the
[separate bilingual inventory journey](inventory-manual-verification.md) rather
than applying the long-lived character expectation to inventory.

## Live observation record

Status: **LIVE NOT PERFORMED**. No human Telegram client is connected to this
execution environment. Task 3.4 remains unchecked until an operator supplies
actual observations; task 4.4 has its separate evidence record. Automated task
3.3 completion does not close either live checkpoint or authorize feature-level
GO.

| Field | Actual observation |
|---|---|
| Date and client version | Not recorded |
| Personal visibility from actor and second human client | Not performed |
| No-username attribution and ordinary button-free public copy | Not performed |
| Immediate double tap and cooldown guidance | Not performed |
| Deliberate repost after at least 30 seconds; both copies retained | Not performed |
| Changed gameplay/new browsing leaves old copies unchanged | Not performed |
| Same usable character source after more than five minutes | Not performed |
| Unsupported/rejected personal delivery without public fallback | Not performed live; automated coverage only |

Replace unperformed entries only with actual observations, keeping any
unsupported client behavior or incomplete checks explicit.
