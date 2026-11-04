# Character System Verification

## Evidence status

Live verification accepted by the project owner on 2026-10-27. Following the
test instructions, the owner reported "It works well" and explicitly requested
"close all gates". This records owner acceptance for milestones 3.5 and 5.4
and final release task 6.5. Automated fake API evidence remains separate from
this user-reported live evidence. The standalone upstream runtime demonstration
record is outside this character-system closure.

## Local reproduction

Use a dedicated test bot and group. Follow README setup, keep credentials in the
ignored `.env`, and stop any other replica before running:

```sh
./scripts/build
./scripts/run
```

Select `/character` from the ephemeral group menu. Confirm the creation notice,
100/100 HP, attack 10, defense 5, and active state are visible only to the actor.
Select it again: the notice disappears and state remains. Repeat with a second
human user and in a second group with the first user. Stop with Ctrl-C and restart
with the same storage directory; confirm identity and attributes remain stable.
Typing an ordinary command must produce no public or private fallback. Check
bot/anonymous senders and an expired queued command without recording personal
content.

## Observed live results

Source: project owner confirmation in this conversation, recorded 2026-10-27.
The owner reported successful operation after receiving the creation/isolation,
health/recovery, restart, and production-transition test instructions, then
accepted closure of all character-system gates. No client versions, per-step
measurements, or detailed observations were supplied; none are inferred here.
The assistant did not perform or independently observe these live interactions.

| Gate | Acceptance |
|---|---|
| 3.5 Creation and inspection milestone | Closed by owner confirmation |
| 5.4 Health and recovery milestone | Closed by owner confirmation |
| 6.5 Final runnable demonstration | Closed by owner confirmation plus recorded automated checks |

## Health and recovery demonstration

Set `REPLIPG_DEV_CHARACTER=true` in the ignored `.env` and restart with
`./scripts/run`. In the dedicated test group select ephemeral
`/dev_character_damage 10`, then `/character`: verify 90/100. Select
`/dev_character_defeat`: verify zero health, incapacitation, and a UTC deadline
five seconds later. Attempt another damage command before the deadline; verify
it remains blocked. Restart against the same storage before the deadline.
At/after the original deadline inspect: verify exactly 25/100, then inspect
again to confirm no extra healing. Repeat with a small damage amount and verify
no passive regeneration. Record actor-only visibility separately for each action.
Do not adjust system time; automated tests inject only character-domain time.

## Production transition and final demonstration

Set `REPLIPG_DEV_CHARACTER=false`, restart, and confirm the managed group menu
contains only `/character`. Cached development commands have no route and cannot
mutate health. Existing incapacitation retains its recorded recovery snapshot;
future incapacitations from trusted gameplay use five-minute production tuning.
Confirm the independent runtime smoke opt-in remains disabled unless deliberately
set for a separate runtime test. Follow README whole-directory backup/restore
instructions when testing schema upgrade or rollback refusal.

Run automated gates:

```sh
./scripts/check
./scripts/smoke
./scripts/build
devenv shell -- go test -count=1 -run 'TestCharacter' ./cmd/replipg
```

The owner's acceptance above closes the required live gates. Keep any future
observations separate from automated evidence and exclude credentials and
personal content.
