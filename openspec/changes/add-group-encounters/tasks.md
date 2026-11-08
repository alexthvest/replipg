# Tasks

## 1. Define content contracts and initial balance

- [ ] 1.1 Select explicit initial health, fixed damage, regeneration, spawn/activity timing, round limits, weights, and loot values; document them and a solo common encounter and cooperative unique encounter calculation in the design/README. Verify the unique example needs several baseline players without a party-size gate.
- [ ] 1.2 Add mob/event/settings values and validation under `internal/modules/encounters`; verify adjacent tests reject invalid ranges, unsupported categories/targeting, duplicate IDs, arithmetic overflow, and broken mob/item/locale references.
- [ ] 1.3 Add `assets/mobs/` and `assets/events/` schemas/settings and initial common/unique content, one file per definition, with matching English/Russian translations. Document file naming and settings in README; verify file/ID correspondence and complete CUE constraints.
- [ ] 1.4 Extend content bundle and CUE loading/watching atomically; verify tests cover invalid-reload retention, captured content independence, empty catalogs, old custom roots without either catalog, and directory deletion/recreation.

## 2. Add persistent health and passive recovery

- [ ] 2.1 Implement health values and elapsed-time recovery with explicit clock input; verify tests cover full-health caps, partial intervals, repeated reads, damage boundaries, backward clocks, and offline elapsed time.
- [ ] 2.2 Add the next ordered SQLite migration and health persistence, initializing existing and new characters at full health. Verify real SQLite migration/restart tests preserve identities, names, inventory snapshots, and established recovery settings.
- [ ] 2.3 Hydrate/project health and show current/max health in character profiles with both locales. Document persistence and recovery behavior; verify fake-transport profile tests and group-isolation tests.

## 3. Implement encounter state and round rules

- [ ] 3.1 Implement captured encounter definitions, announcement/open/terminal phases, participant state, and irreversible knockout flags. Verify state tests cover late participation, skipped rounds, zero-health entry rejection, and regeneration without same-encounter reentry.
- [ ] 3.2 Implement one accepted attack per character/round and fixed player damage behind a replaceable calculation boundary; verify duplicate, stale, foreign, terminal, and deadline-edge submissions do not mutate state.
- [ ] 3.3 Implement player-first resolution, ordered monster attack selection, and deterministic single/multiple/all target selection; verify no retaliation after killing damage, no repeated target in multi-target attacks, and exclusion of nonparticipants/knockouts.
- [ ] 3.4 Implement victory, immediate party defeat, ignored-first-round escape, and maximum-round escape. Document lifecycle and attack-only rules; verify terminal precedence and rejection of newcomers after party defeat.

## 4. Persist and coordinate combat and rewards

- [ ] 4.1 Add encounter, participant, submission, resolution, reward, schedule/activity, and delivery tables/queries with uniqueness constraints and compile-time port contracts; verify SQLite tests enforce one active encounter per group and one action/resolution/reward identity.
- [ ] 4.2 Add the encounter unit of work and application submission coordinator; verify a first valid attack atomically creates character/health/participation/submission, while rejected controls create nothing and failed writes roll back.
- [ ] 4.3 Add transactional resolution and the initial per-contributor reward policy using captured item snapshots and transaction-bound inventory ports. Verify equal individual loot includes knocked-out contributors, excludes nonparticipants, and preserves inventory stacking/individual identities and shop reservation behavior.
- [ ] 4.4 Persist mutation replay outcomes and delivery intent with combat changes; verify concurrent duplicate callbacks, repeated resolution, inventory overflow, partial reward failure, and restart retries cannot duplicate damage or loot.
- [ ] 4.5 Add encounter projections and post-commit inventory/health updates; verify hydration order, menu invalidation, fatal projection failure behavior, and compatibility with existing grant/shop flows. Document reward eligibility and its intended future replacement boundary.

## 5. Schedule active-group encounters

- [ ] 5.1 Record qualifying successful human gameplay interactions and implement weighted event selection with persisted random delay/seed; verify fake-clock/random tests cover activity expiry, no chat/background refresh, both categories, one active slot, and terminal-time delay calculation.
- [ ] 5.2 Add bounded owner-side due-work operations and recover stored schedules/open rounds; verify restart keeps future due times, inactivity creates no backlog, overdue rounds resolve once, and subsequent rounds receive a fresh full interval.
- [ ] 5.3 Document activity eligibility, spawn timing, and downtime semantics in README; verify documented behavior through fake-clock integration cases covering inactive and newly active groups.

## 6. Add group-visible controls and reliable delivery

- [ ] 6.1 Render localized monster health, round number/deadline, announced attack/targeting, and terminal results; add a shared Attack callback namespace with durable group/message/round validation. Verify fake Telegram tests allow different humans to attack and reject forged, expired, foreign, or knocked-out actions.
- [ ] 6.2 Implement durable announcement/update delivery and owner-side result binding outside network I/O; verify failures leave a new round closed, successful binding starts a full interval, and ambiguous sends create only one actionable message binding.
- [ ] 6.3 Implement per-encounter delivery ordering, bounded retries, replacement of missing messages, and isolation of unavailable groups; verify delayed revisions cannot overwrite current rounds and old message controls stop working after rebinding.
- [ ] 6.4 Wire the scheduler/delivery worker with owned cancellation, bounded jobs, and shutdown joins; verify startup failure, shutdown during a mutation/send, and worker termination tests leave no partial mutations or leaked workers. Document operational diagnostics and a disposable-group smoke procedure.

## 7. Verify the integrated change

- [ ] 7.1 Run end-to-end fake-clock/transport scenarios for common solo victory, unique cooperative victory, knockout loot, all-participant defeat, ignored spawn, round-limit escape, mid-fight reload, and restart with pending announcement/resolution; verify committed health and inventory outcomes against the specs.
- [ ] 7.2 Inside the repository devenv shell, format changed Go/CUE files with `gofmt -w` and `cue fmt`, then run `go test ./...`, `go vet ./...`, `go build ./...`, and `golangci-lint run ./...`. Run `go mod tidy` only if imports/dependencies change; preserve the declared Go version and unrelated working-tree changes. Record actual results.
- [ ] 7.3 Inside devenv, run `go test -race ./...` where supported, `cue vet -c=false ./...`, and complete validation with `cue vet -c ./assets/items ./assets/locales ./assets/mobs ./assets/events ./assets/shop`; record any environment-blocked checks accurately.
- [ ] 7.4 Run `openspec validate add-group-encounters --strict`, review implementation against every acceptance scenario, and record manual smoke results or explicitly state they were not run. Verify README includes compatible backup/rollback instructions and no implementation settings or secrets were placed in `.env.example` without documentation.

## Workflow follow-up

- Implementation begins only when requested through `$openspec-apply-change add-group-encounters`.
- Archive only on an explicit user request after implementation and verification are complete.
