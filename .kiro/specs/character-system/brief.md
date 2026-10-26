# Brief: character-system

## Problem

Community members need a durable in-game identity before they can collect items or join monster battles. The MVP must make entry immediate without forcing players through class selection or a private bot chat.

## Current State

No player or character model exists. Telegram users are not registered and there is no persistent health, battle eligibility, or defeat state.

## Desired Outcome

A member can create and inspect exactly one generic character from the group through ephemeral commands and messages. The character has stable combat attributes and persistent health, can be temporarily incapacitated after defeat, and automatically becomes playable again when recovery expires.

## Approach

Represent the Telegram user identity separately from a generic character record, enforcing one character per user. Keep initial attributes configuration-backed and avoid classes, point allocation, or leveling. Model incapacitation as a persisted deadline so recovery works without a fragile timer.

## Scope

- **In**: Idempotent character creation; ephemeral group command flow; character summary; generic starting combat attributes; current and maximum health; active and temporarily incapacitated states; persisted recovery deadline; battle eligibility queries; health mutation contracts.
- **Out**: Classes, races, skills, XP, levels, stat allocation, cosmetic customization, permanent death, currency, achievements, and multiple characters per user.

## Boundary Candidates

- Telegram-facing character command handlers that produce ephemeral responses.
- Character aggregate and state-transition rules independent of Telegram.
- Character repository and atomic health/status mutations.

## Out of Boundary

- Equipment-derived modifiers belong to inventory-equipment.
- Battle damage and defeat orchestration belong to battle-system.
- Telegram update plumbing and ephemeral delivery belong to telegram-runtime.

## Upstream / Downstream

- **Upstream**: telegram-runtime provides identity-bearing update routing, ephemeral replies, persistence, and clock/scheduling abstractions.
- **Downstream**: inventory-equipment attaches owned items to a character; monster-encounters and battle-system check eligibility and mutate health/status.

## Existing Spec Touchpoints

- **Extends**: None.
- **Adjacent**: inventory-equipment calculates equipped modifiers; battle-system decides when damage causes incapacitation.

## Constraints

Use Telegram numeric user IDs as external identities but internal stable IDs for domain relations. Creation and recovery must be idempotent under repeated updates. Do not require private-chat interaction or collect unnecessary profile data.
