# Brief: telegram-runtime

## Problem

The community game has no executable bot foundation or durable application services. Every gameplay feature needs a reliable way to receive Telegram group interactions, send public and user-specific ephemeral responses, persist state, and recover work after a restart.

## Current State

The repository contains a minimal Go module and a `Hello World` entry point. Devenv enables Go tooling, but there is no pinned lock file, Telegram integration, configuration model, database, migration mechanism, scheduler, or delivery recovery.

## Desired Outcome

A single container can start safely, validate configuration, migrate and open SQLite, remove stale webhooks, consume Telegram updates by long polling, route commands and callbacks, send Bot API 10.3 ephemeral group messages, and resume durable scheduled and outbound work after restart.

## Approach

Build thin adapters around `github.com/go-telegram/bot` and a pinned no-CGO `modernc.org/sqlite` driver. Keep business domains independent of Telegram and SQL details. Use database-backed due timestamps and an outbox/reconciliation state instead of treating goroutine timers or Telegram sends as durable facts.

## Scope

- **In**: Devenv-pinned Go toolchain; application lifecycle and graceful shutdown; environment configuration; redacted logging; Telegram long polling; webhook cleanup; update routing; public and ephemeral message helpers; callback acknowledgements; SQLite setup and migrations; transaction helpers; durable job claiming; outbound delivery/reconciliation; retry and rate-limit handling; health/readiness signals.
- **Out**: Character rules, inventory semantics, activity scoring, encounter probability, combat rules, content administration, webhooks, multi-replica coordination, and PostgreSQL.

## Boundary Candidates

- Telegram transport adapter for updates, callback acknowledgement, public messages, and ephemeral messages.
- SQLite persistence and migration adapter with short transaction boundaries.
- Durable scheduler and Telegram delivery outbox/reconciliation worker.
- Application composition, configuration, observability, and shutdown lifecycle.

## Out of Boundary

- Gameplay packages must not depend directly on `go-telegram/bot` request or model types.
- This spec does not define player-facing game commands beyond transport-level routing contracts.
- It does not provide distributed locks or horizontal scaling.

## Upstream / Downstream

- **Upstream**: Telegram Bot API 10.3+, local persistent container volume, and the repository's devenv configuration.
- **Downstream**: Character, inventory, encounter, and battle specs consume its transport, persistence, transaction, and scheduling boundaries.

## Existing Spec Touchpoints

- **Extends**: None; this is a greenfield foundation.
- **Adjacent**: Gameplay specs own domain behavior and must not leak that behavior into runtime adapters.

## Constraints

Run one long-polling replica. Use Go 1.26 or newer, a `go-telegram/bot` version with Bot API 10.3 support, and a patched explicitly pinned `modernc.org/sqlite`/`modernc.org/libc` pair. Keep the database and WAL sidecars on one local persistent volume. Bound SQLite busy waits, make retries cancellation-aware, never hold SQL transactions across Telegram calls, and never log secrets or chat message bodies.
