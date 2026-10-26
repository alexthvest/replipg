# Project Guidelines

This file defines repository-wide instructions for AI coding agents. Follow these rules when reviewing, planning, implementing, testing, or documenting changes. Prefer existing project conventions over the examples in this document; do not reorganize a working codebase merely to match a suggested layout.

## 1. Working Principles

- First inspect `README.md`, `go.mod`, `devenv.nix`, `devenv.yaml`, build scripts, CI configuration, existing code, tests, and any applicable nested `AGENTS.md` files.
- Understand the affected package and its callers before editing. Prefer small, focused changes and preserve backward compatibility unless a breaking change is approved.
- Do not introduce dependencies, frameworks, patterns, or infrastructure without a documented reason.
- Explain significant assumptions and surface conflicting requirements before making irreversible decisions.
- Do not modify generated files by hand. Use the appropriate generator, and include generated changes when required.
- Never commit secrets, credentials, tokens, or personal data. Do not log sensitive values.
- Do not claim checks passed unless they were actually run. Report commands run, results, and any checks you could not run.

## 2. Development Environment

- Use the repository's devenv configuration as the canonical development environment. Enter it with `devenv shell`, or run a single command with `devenv shell -- <command>`.
- Keep shared packages, language runtimes, and environment settings in `devenv.nix`; keep input declarations in `devenv.yaml`.
- The environment currently provides Go, `gopls`, and `golangci-lint`. Prefer these pinned tools over host-installed alternatives.
- Run `devenv update` when changing inputs in `devenv.yaml`, and include the resulting `devenv.lock` change when present. Do not hand-edit generated devenv state.
- Keep machine-specific overrides in `devenv.local.nix` or `devenv.local.yaml`; these files are intentionally ignored. Store secrets only in ignored local environment files, never in the shared devenv configuration.

## 3. Repository and Architecture

- Respect the existing package boundaries, module path, public API, and dependency direction.
- Prefer idiomatic Go and the standard library unless there is a concrete reason to use an external dependency.
- Keep packages cohesive and dependencies explicit; avoid import cycles, package-level mutable state, and unnecessary abstraction.
- Use `internal/` for implementation details that should not be imported by external modules, if it fits the existing repository.
- Keep `cmd/` entry points thin when the repository follows that pattern; place reusable application logic in packages.
- Use interfaces where consumers need substitution or where they clarify a boundary, not simply for every concrete type.
- Maintain clear separation between transport, business logic, and persistence when those layers exist; do not introduce them into a small project without need.

## 4. Go Coding Standards

- Run `gofmt` on modified Go files; use `goimports` if already part of the project workflow.
- Use meaningful, concise names and follow Go naming conventions (`MixedCaps`, initialisms such as `ID`, `URL`, `HTTP`).
- Return errors rather than panic for expected failures. Wrap errors with context using `%w` when callers may need to inspect them.
- Use `errors.Is` and `errors.As` for error inspection; avoid comparing error strings.
- Pass `context.Context` as the first argument for request-scoped or cancelable operations. Do not store contexts in structs unless there is a compelling, documented reason.
- Keep functions readable and focused. Prefer straightforward code over clever abstractions.
- Avoid speculative configuration and premature optimization. Measure performance-sensitive changes where practical.
- Add Go documentation comments for exported declarations where it improves the public API and when required by existing linting conventions.
- Use structured logging where the project already supports it, and avoid logging credentials or customer data.

## 5. Concurrency and Resource Safety

- Make goroutine ownership, cancellation, and shutdown explicit. Do not start background goroutines without a clear lifecycle.
- Protect shared mutable state using appropriate synchronization or ownership rules. Avoid races and unnecessary locks.
- Do not copy values containing mutexes or similar synchronization primitives.
- Close acquired resources when no longer needed, handle close errors where relevant, and avoid leaking timers, connections, and goroutines.
- For network operations, propagate cancellation and configure timeouts at the correct boundary; avoid hidden infinite waits.

## 6. APIs, Data, and Security

- Validate untrusted inputs at boundaries and use typed representations where feasible.
- Preserve API contracts: response status codes, payload shapes, error semantics, and compatibility requirements.
- Use parameterized database queries; never concatenate untrusted input into SQL.
- Keep migrations backward-compatible where possible, and describe rollout or rollback requirements for schema changes.
- Apply least privilege and follow existing authentication and authorization mechanisms.
- Never introduce secret defaults or insecure production fallbacks. Put configuration in the project's established configuration system.

## 7. Tests and Quality Gates

- Add or update tests for behavior changes, regressions, error paths, and meaningful edge cases.
- Prefer table-driven unit tests when they increase clarity. Use `t.Run` for named cases and `t.Parallel` only when tests are safe to run concurrently.
- Keep tests deterministic; avoid real network calls, nondeterministic timing, or external services in unit tests.
- Use integration tests for behavior that depends on databases, external APIs, or multiple packages, following project conventions.
- Before marking work complete, run applicable checks from the repository root (or explain why they could not run):

  ```sh
  devenv shell -- gofmt -l <modified-go-files>  # Must produce no output
  devenv shell -- go test ./...
  devenv shell -- go vet ./...
  devenv shell -- golangci-lint run
  devenv shell -- go test -race ./...           # When concurrency is affected or practical
  ```

- If the repository already defines `make`, `task`, CI scripts, linters, or narrower test commands, follow those as the primary project workflow. Do not assume the example commands cover generated code, build tags, or environment-specific integration tests.
- If changing dependencies, inspect `go.mod` and `go.sum`; use `go mod tidy` only when appropriate and review its diff.

## 8. Scope, Reviews, and Delivery

- Keep changes aligned with the requested behavior and approved design. Avoid unrelated refactors.
- Update README, API documentation, examples, and configuration documentation when externally visible behavior changes.
- For changes affecting public interfaces, persistence, authentication, deployment, or concurrency, note compatibility and operational risks.
- In the final summary, include: what changed, why, files or packages affected, checks performed, and known risks or follow-ups.


# Agentic SDLC and Spec-Driven Development

Kiro-style Spec-Driven Development on an agentic SDLC

## Project Memory
Project memory keeps persistent guidance (steering, specs notes, component docs) so Codex honors your standards each run. Treat it as the long-lived source of truth for patterns, conventions, and decisions.

- Use `.kiro/steering/` for project-wide policies: architecture principles, naming schemes, security constraints, tech stack decisions, api standards, etc.
- Use local `AGENTS.md` files for feature or library context (e.g. `src/lib/payments/AGENTS.md`): describe domain assumptions, API contracts, or testing conventions specific to that folder. Codex auto-loads these when working in the matching path.
- Specs notes stay with each spec (under `.kiro/specs/`) to guide specification-level workflows.

## Project Context

### Paths
- Steering: `.kiro/steering/`
- Specs: `.kiro/specs/`

### Steering vs Specification

**Steering** (`.kiro/steering/`) - Guide AI with project-wide rules and context
**Specs** (`.kiro/specs/`) - Formalize development process for individual features

### Active Specifications
- Check `.kiro/specs/` for active specifications
- Use `$kiro-spec-status [feature-name]` to check progress

## Development Guidelines
- Think in English, generate responses in English. All Markdown content written to project files (e.g., requirements.md, design.md, tasks.md, research.md, validation reports) MUST be written in the target language configured for this specification (see spec.json.language).

## Minimal Workflow
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

## Skills Structure
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

## Subagents

Current Codex releases enable subagents by default. Use the available tools when the user, project rules, or this skill's workflow calls for delegation; no experimental feature flag is required. An administrator or user can disable subagents by setting `enabled = false` under `[agents]` in Codex configuration.

Use a fresh context for each independent implementer or reviewer, passing the task-relevant inputs explicitly. If delegation is unavailable, follow the skill's inline fallback and identify the review as inline. Skill discovery alone does not prove subagent execution or independent review.

## Development Rules
- 3-phase approval workflow: Requirements → Design → Tasks → Implementation
- Human review required each phase; use `-y` only for intentional fast-track
- Keep steering current and verify alignment with `$kiro-spec-status`
- Follow the user's instructions precisely, and within that scope act autonomously: gather the necessary context and complete the requested work end-to-end in this run, asking questions only when essential information is missing or the instructions are critically ambiguous.

## Steering Configuration
- For spec and implementation work, load the core steering files below from `.kiro/steering/`. Reuse current context rather than rereading unchanged files.
- Load additional steering only when required by project rules or relevant to the task.
- Default files: `product.md`, `tech.md`, `structure.md`
- Custom files are supported (managed via `$kiro-steering-custom`)
