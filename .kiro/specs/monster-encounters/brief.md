# Brief: monster-encounters

## Problem

The game needs to turn ordinary community activity into spontaneous shared events without encouraging message spam. It also needs special encounters that remain surprising and independent of chat activity.

## Current State

The bot does not observe chat activity, define monsters, schedule events, or manage encounter messages. There is no durable lifecycle for a spawn that crosses process restarts or Telegram delivery failures.

## Desired Outcome

Each registered group can receive configurable common and special monster encounters. Common spawn probability increases with genuine recent participation; special encounters use random scheduling independent of activity. Every encounter has one durable lifecycle and a public group message from which eligible users can join the battle.

## Approach

Track a privacy-minimized rolling activity score dominated by unique active users, with capped per-user message contribution. Evaluate spawn policy through durable scheduled jobs, select monsters from version-controlled definitions and loot references, and reconcile public Telegram messages through the runtime outbox.

## Scope

- **In**: Chat registration; minimal activity metadata; rolling weighted activity score; anti-spam per-user caps; common encounter probability; activity-independent special encounter scheduling; seeded monster definitions and attack profiles; one active-encounter policy per chat unless later requirements change it; durable encounter states; public spawn messages; expiration; restart and delivery reconciliation.
- **Out**: World events, cross-chat shared health, non-monster events, combat resolution, loot allocation, runtime content editing, guild encounters, and administrator dashboards.

## Boundary Candidates

- Privacy-minimized chat activity aggregation and scoring.
- Common and special spawn policies with injectable time and randomness.
- Monster/content catalog sourced from version-controlled seed data.
- Durable encounter lifecycle and public Telegram presentation.

## Out of Boundary

- A spawned encounter does not resolve player or monster actions; battle-system owns combat state.
- The activity subsystem does not store message text or reward raw message volume without caps.
- This spec does not coordinate across chats.

## Upstream / Downstream

- **Upstream**: telegram-runtime provides ordinary group updates, durable scheduling, persistence, and public delivery; character-system supplies eligibility checks for battle entry.
- **Downstream**: battle-system consumes the selected monster definition and encounter lifecycle, then reports terminal outcomes.

## Existing Spec Touchpoints

- **Extends**: None.
- **Adjacent**: telegram-runtime routes updates but does not score them; battle-system begins when a player first takes an action.

## Constraints

The bot must be able to receive ordinary messages, so deployment must make it a suitable group administrator or disable Bot Privacy Mode. Count only metadata necessary for scoring. Make random behavior testable through injected entropy. Store all deadlines and claimed jobs durably; do not rely on process timers alone.
