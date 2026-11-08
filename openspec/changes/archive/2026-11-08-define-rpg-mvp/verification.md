# Implementation verification

Owner catalog buttons follow-up (2026-11-08): catalog rows now match inventory
spacing and plain page-local numbering, with parenthesized IDs in code. Numbered
buttons atomically grant one selected item to the owner using the existing grant
coordinator and shared projection helper. Fake Telegram integration verifies
owner character creation, stacking, separate individual entries, distinct presses,
duplicate update replay, group isolation, unauthorized and stale callbacks, and
definition deletion. The reload test explicitly consumes the startup rescan
before waiting for the deletion publication and passed ten race-enabled runs.
All Go tests, race tests, vet, build, lint (zero issues), CUE checks, strict
OpenSpec validation, and diff whitespace checks passed; formatting and module
tidying also ran inside devenv. No real Telegram smoke run was performed.

Owner catalog follow-up (2026-11-08): added `/items` with six-entry pages,
sorted active IDs, localized names, owner-only execution/navigation, and no
character or ownership writes. Fake Telegram tests cover regular-user and game
admin denial, foreign/stale callbacks, language changes, and two-group isolation.
View tests cover sorted detached IDs, 13 entries across three pages, catalog
replacement with an empty set, page clamping, and Telegram text limits.
Re-ran gofmt, cue fmt, go mod tidy, go test ./..., go test -race ./...,
go vet ./..., go build ./..., golangci-lint run ./... (zero issues), both CUE
validation commands, strict OpenSpec validation, and git diff --check; all passed
inside devenv where applicable. No real Telegram smoke run was performed.

Date: 2026-11-08. Change: `define-rpg-mvp` (`spec-driven`).

The module retains `go 1.26`, as requested. The devenv shell supplied Go 1.26.10
on linux/amd64. Application environment settings consistently use `REPLIPG_`,
including `REPLIPG_BOT_TOKEN` and `REPLIPG_BOT_OWNER_ID`. Existing local dotenv
values were not printed or committed.

## Checks run

Go and CUE commands ran inside the repository's `devenv shell`:

| Check | Outcome |
| --- | --- |
| `gofmt -w` on changed Go files | Passed |
| `cue fmt` on changed asset files | Passed |
| `go mod tidy` | Passed; retained `go 1.26` |
| `go test ./...` | Passed |
| `go test -race ./...` | Passed |
| `go vet ./...` | Passed |
| `go build ./...` | Passed |
| `go build ./cmd/replipg` | Passed |
| `golangci-lint run ./...` | Passed; zero issues |
| `cue vet -c=false ./...` | Passed |
| `cue vet -c ./assets/items ./assets/locales` | Passed |
| `openspec validate define-rpg-mvp --strict` | Passed |
| `git diff --check` | Passed |

Pinned direct modules were checked with `go list -m -json -retracted`; none were
reported as retracted. Inspection of `go list -deps github.com/yohamta/donburi`
found no rendering/Ebitengine dependency. `devenv.lock` remains tracked and was
not changed. No automated check remains blocked.

## Behavioral evidence

- Configuration tests cover prefixed names, defaults, invalid input, and
  diagnostics without credential values. Startup tests verify storage unwind
  after asset and network readiness failures.
- Runtime tests cover typed registration, frozen registries, ordered hydration,
  missing dependencies, scoped projections, cancellation, queue draining, and
  fail-stop after a committed projection error.
- SQLite tests cover all three migrations, foreign keys, uniqueness and quantity
  constraints, newer-schema rejection, migration rollback, persisted identities,
  concurrent creation, partial-grant rollback, cancelled commits, overflow, exact
  snapshot reuse, and forced digest collisions.
- Session/view tests cover expiry, eviction, token collisions, message/group/user
  binding, stale action references, six-row pages, literal Unicode formatting,
  and Telegram text/callback limits. A local HTTP fake verifies actual Telegram
  multipart requests, UTF-16 entities, empty keyboards, acknowledgements, and
  bounded retries without contacting Telegram.
- Fake Telegram application tests cover all seven capabilities across multiple
  users and groups, settings revocation and races, commit/edit/send failures,
  persisted restarts, protected inventory inspection/navigation, grant replay,
  atomic live reload, invalid-candidate retention, definition deletion, and saved
  English/Russian snapshots. A coordinated real-SQLite runtime test verifies
  shutdown during a grant commits fully or rolls back fully.
- Go import metadata tests enforce feature, runtime, menu, and coordinator
  boundaries. Compile-time assertions verify SQLite's feature and transaction
  port implementations. README documents registration and adding a future feature
  without a central runtime gameplay branch.

## Manual verification and follow-up

A real Telegram group smoke run was not performed. README contains the two-group
manual procedure and backup, upgrade, and rollback steps. No real bot messages
were sent by automated checks.

The change remains available for review and has not been archived. Archive only
on an explicit user request.
