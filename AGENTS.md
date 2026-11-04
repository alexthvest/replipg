# Repository Agent Instructions

This file separates Go implementation rules from Kiro specification and workflow rules. Apply the Go rules to code and toolchain work, and follow the specification workflow for spec-driven development.

## Documentation Audience

- README files must contain only information intended for human readers. Keep agent rules and instructions in `AGENTS.md`.
- Do not update README files when adding, changing, or removing agent rules; update `AGENTS.md` for those changes.

## Go Development Rules

### Development Environment
- Run Go, formatting, linting, tests, and builds inside `devenv shell`. Use the toolchain and tools declared in `devenv.nix` and the inputs in `devenv.yaml`.
- Start an interactive session with `devenv shell`, then run the commands below from the Go module root. For a single command from outside the shell, use `devenv shell -- go test ./...` (substitute the required command).
- Go is enabled through `languages.go.enable`; `gopls` and `golangci-lint` are provided by devenv. Add required development tools to `devenv.nix` instead of relying on global installs or ad hoc `go install` commands.
- Keep environment changes in the devenv configuration. Do not edit generated `.devenv/` files or commit local environment files or secrets; dotenv loading is enabled.
- The Go module is `github.com/alexthvest/replipg`; run commands from the repository root.

### Code Organization and Conventions
- Follow existing package boundaries. Use `cmd/<binary>/` for executable entry points and `internal/` for private application packages; keep entry points thin.
- Prefer small, cohesive packages, idiomatic Go names, and explicit dependencies. Define interfaces where they are consumed when a concrete use case needs one; avoid speculative abstractions and mutable global state.
- Use the standard library when practical. Add dependencies deliberately, keep `go.mod` and `go.sum` consistent, and run `go mod tidy` inside devenv after dependency changes. Do not upgrade unrelated dependencies or the Go version incidentally.
- Return errors with useful context, wrap underlying errors with `%w` when callers need to inspect them, and use `errors.Is` or `errors.As` for matching. Handle errors explicitly, including cleanup errors where relevant; reserve panics for unrecoverable programming errors.
- Pass `context.Context` as the first argument for operations that support cancellation; propagate cancellation and deadlines. Give goroutines a clear owner and termination path, and close resources promptly.
- Keep secrets out of source code, logs, and test fixtures. Validate input at application boundaries and use parameterized SQL for database queries.

### Code Formatting and Spacing

- Run `golangci-lint fmt ./...` inside `devenv shell` to apply gofmt and goimports. Let these tools control indentation, alignment, and import formatting; use the spacing rules below for logical separation that the formatters do not infer.
- Use a single blank line between top-level declarations, including functions, methods, types, and separate `const` or `var` blocks. Keep documentation comments directly attached to the declaration they describe.
- Inside functions, separate distinct logical steps with one blank line, such as validation, preparation, execution, and result handling. Keep statements that perform one cohesive step together; do not insert a blank line after every statement.
- Keep an operation and its immediate error check together without a blank line. Keep resource acquisition, its error check, and the associated `defer` in the same logical group; separate subsequent work with a blank line.
- Separate a completed guard clause or other control-flow block from the next independent step with a blank line. A final return may stay attached to the computation whose result it directly returns; separate it when it begins a distinct result-handling step.
- Do not add blank lines immediately after an opening brace or before a closing brace. Use at most one consecutive blank line to separate code groups.
- Keep related fields in structs and entries in composite literals together. Use a blank line only when it identifies a meaningful group; keep field comments next to their fields.
- Keep imports in a single parenthesized block when there are multiple imports, with standard-library imports separated from other imports by a blank line. Let goimports sort imports and remove unused entries.
- Keep comments directly above the code they explain, with any separating blank line before the comment. In tests, use blank lines to distinguish setup, execution, and assertions when these are separate steps.

### Formatting, Validation, and Tests
- Treat `.golangci.yml` as the source of truth for lint and formatting rules. It enables gofmt and goimports, includes tests in linting, and requires every `//nolint` directive to name the linter and explain the suppression. Fix the cause before adding a suppression.
- Run these commands inside `devenv shell` from the module root:

  ```sh
  golangci-lint fmt ./...
  golangci-lint config verify
  golangci-lint run ./...
  go test ./...
  go build ./...
  ```

- `scripts/check` runs the non-mutating validation set, including formatting verification and race tests. Report checks that could not run and why; do not claim tests or builds passed without running them.
- Add focused regression tests for bug fixes and tests for new behavior. Use standard `testing` conventions, table-driven tests when they clarify multiple cases, and deterministic fixtures. Keep tests beside the code in `*_test.go` files; use `t.TempDir` and `t.Cleanup` for temporary resources.
- Run focused tests while iterating, then the applicable checks above before declaring implementation complete. For concurrency changes, also run `go test -race ./...` when the devenv toolchain and platform support it. Document any required services or integration-test setup and report skipped coverage explicitly.

## Specification and Workflow Rules

Kiro-style Spec-Driven Development on an agentic SDLC.

### Project Memory
Project memory keeps persistent guidance (steering, specs notes, component docs) so Codex honors your standards each run. Treat it as the long-lived source of truth for patterns, conventions, and decisions.

- Use `.kiro/steering/` for project-wide policies: architecture principles, naming schemes, security constraints, tech stack decisions, api standards, etc.
- Use local `AGENTS.md` files for feature or library context (e.g. `src/lib/payments/AGENTS.md`): describe domain assumptions, API contracts, or testing conventions specific to that folder. Codex auto-loads these when working in the matching path.
- Specs notes stay with each spec (under `.kiro/specs/`) to guide specification-level workflows.

### Project Context

#### Paths
- Steering: `.kiro/steering/`
- Specs: `.kiro/specs/`

#### Steering vs Specification

**Steering** (`.kiro/steering/`) - Guide AI with project-wide rules and context
**Specs** (`.kiro/specs/`) - Formalize development process for individual features

#### Active Specifications
- Check `.kiro/specs/` for active specifications
- Use `$kiro-spec-status [feature-name]` to check progress

### Specification Language
- Think in English, generate responses in English. All Markdown content written to project files (e.g., requirements.md, design.md, tasks.md, research.md, validation reports) MUST be written in the target language configured for this specification (see spec.json.language).

### Minimal Workflow
- Phase 0 (optional): `$kiro-steering`, `$kiro-steering-custom`
- Discovery: `$kiro-discovery "idea"` — determines action path, writes brief.md + roadmap.md for multi-spec projects
- Phase 1 (Specification):
  - Single spec: `$kiro-spec-quick {feature} [--auto]` or step by step:
    - `$kiro-spec-init "description"`
    - `$kiro-spec-requirements {feature}`
    - `$kiro-validate-gap {feature}` (optional: for existing codebase)
    - `$kiro-spec-design {feature} [-y]`
    - `$kiro-validate-design {feature}` (optional: design review)
    - `$kiro-spec-tasks {feature} [-y]`
  - Multi-spec: `$kiro-spec-batch` — creates all specs from roadmap.md in parallel by dependency wave
- Phase 2 (Implementation): `$kiro-impl {feature} [tasks] [--review required|inline|off]`
  - Without task numbers: autonomous mode (subagent per task + independent review + final validation)
  - With task numbers: manual mode (selected tasks in main context, still reviewer-gated before completion)
  - `--review off` skips task-local review; use it intentionally and keep `$kiro-validate-impl {feature}` as the final quality gate
  - `$kiro-validate-impl {feature}` (standalone re-validation)
- Progress check: `$kiro-spec-status {feature}` (use anytime)

### Skills Structure
Skills are located in `.agents/skills/kiro-*/SKILL.md`
- Each skill is a directory with a `SKILL.md` file
- Use `/skills` to inspect currently available skills
- Invoke a skill directly with `$kiro-<skill-name>`
- `kiro-review` — task-local adversarial review protocol used by reviewer subagents
- `kiro-debug` — root-cause-first debug protocol used by debugger subagents
- `kiro-verify-completion` — fresh-evidence gate before success or completion claims
- Use skills explicitly requested by the user and skills relevant to the task's domain, including design, accessibility, and UX.
- Select skills from their descriptions or metadata first, then read only the selected skills and the references needed for the task.
- Follow explicit host and project rules and retain required workflow checks. Do not skip relevant skills just because the task is small.

### Subagents

Current Codex releases enable subagents by default. Use the available tools when the user, project rules, or this skill's workflow calls for delegation; no experimental feature flag is required. An administrator or user can disable subagents by setting `enabled = false` under `[agents]` in Codex configuration.

Use a fresh context for each independent implementer or reviewer, passing the task-relevant inputs explicitly. If delegation is unavailable, follow the skill's inline fallback and identify the review as inline. Skill discovery alone does not prove subagent execution or independent review.

### Workflow and Approval Rules
- 3-phase approval workflow: Requirements → Design → Tasks → Implementation
- Human review required each phase; use `-y` only for intentional fast-track
- Keep steering current and verify alignment with `$kiro-spec-status`
- Follow the user's instructions precisely, and within that scope act autonomously: gather the necessary context and complete the requested work end-to-end in this run, asking questions only when essential information is missing or the instructions are critically ambiguous.

### Visible Results and Operator Development Commands
- User-approved exception for `character-system`: use the player-facing `/character` command and Refresh for the visible demonstration instead of a separate development command or diagnostic profile. Verify internal identity and duplicate prevention with automated database assertions. Document syntax, prerequisites, expected output, state changes, and self-only access for owner and non-owner accounts; retain ephemeral group presentation and foreign/expired callback rejection. This exception does not change other features or existing runtime development commands.
- Every feature specification must deliver an observable result and include development commands that let the operator exercise and inspect that result. Internal packages and automated tests alone do not satisfy this requirement.
- Requirements must describe the observable result and operator-only access. Design must specify exact command syntax, prerequisites, expected output, and any state changes. Tasks must include command implementation, documentation, and verification of both successful operator use and rejected non-operator use.
- In Telegram, authorize every development command and its associated callbacks server-side using the configured owner's Telegram user ID. Group administrators, usernames, command visibility, and client-provided identity claims do not confer access. Reject unauthorized requests before running development behavior or returning development output.
- Development interactions stay in groups/supergroups and use ephemeral personal responses. They must follow the same privacy, callback validation, expiry, and reopening rules as other personal interactions.
- Keep domain packages independent of Telegram. For domain-only specs, place development command integration in the application/transport layer and record the runtime prerequisite for the visible demonstration separately from domain dependencies.
- Before claiming a spec is implemented, verify its documented development commands and record the visible result and access-control evidence. Report any live Telegram verification that could not be performed.

### Steering Configuration
- For spec and implementation work, load the steering files below from `.kiro/steering/`. Reuse current context rather than rereading unchanged files.
- Load additional steering only when required by project rules or relevant to the task.
- Default files: `roadmap.md`, `development-workflow.md`. Product scope and architecture context live in the roadmap and feature specifications.
- Custom files are supported (managed via `$kiro-steering-custom`)
