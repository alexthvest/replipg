# Repository Guidelines

## Project Structure

Keep entry points and startup wiring in `cmd/replipg/` and add
application packages under `internal/` as needed. Place Go tests beside their
source files.

Keep CUE item schemas and definitions in `assets/items/` and locale schemas and
translations in `assets/locales/`. Maintain behavior specifications in
`openspec/specs/` and proposals, tasks, and archives in `openspec/changes/`.

## Development Commands

Use the repository's devenv environment for Go and CUE commands. Preserve the Go
version declared in `go.mod`. Keep `devenv.lock` in version control and update
environment inputs deliberately.

Copy `.env.example` to `.env` only if `.env` does not exist, then enter
`devenv shell`. Run these commands inside that shell:

```sh
go run ./cmd/replipg          # Run locally.
go build ./cmd/replipg        # Build the executable.
go test ./...                # Run tests.
go vet ./...                 # Check Go code.
golangci-lint run ./...       # Run lint checks.
cue vet -c=false ./...        # Validate CUE constraints.
```

## Coding and Asset Conventions

Format Go with `gofmt -w <files>` and CUE with `cue fmt <files>`. Use idiomatic Go
names and cohesive packages. Prefer the standard library; justify dependencies
and run `go mod tidy` after import or dependency changes.

Pass dependencies explicitly, return contextual errors, and propagate caller
contexts. Give goroutines clear ownership and shutdown paths.

Use `package items` and `package locales` in their respective asset directories.
Use translation keys such as `item.coin.name` for item names and descriptions;
keep keys consistent across item data, locale schemas, and all translations.
Check complete assets with `cue vet -c ./assets/items ./assets/locales`.

## Testing Guidelines

Use standard Go `testing`, `*_test.go` files, and `TestXxx` functions. Add
deterministic tests for new behavior, regressions, and error paths. Run
`go test -race ./...` for concurrency changes when supported. Report checks run
and any blocked checks accurately.

## Changes and Pull Requests

Use concise, imperative subjects. Describe behavior changes and
validation in pull requests, linking relevant issues or OpenSpec changes.
Preserve unrelated working-tree changes.

Follow `openspec/config.yaml` and relevant local skills. Read planning artifacts
before implementation, update task checkboxes, and validate changed artifacts
with `openspec validate <change-name> --strict` or
`openspec validate --specs --strict`. Archive only when requested.

Never commit secrets or `.env`; keep `.env.example` limited to documented
placeholders. Use devenv dotenv loading only on trusted development machines
because it imports `.env` into the local Nix store.
