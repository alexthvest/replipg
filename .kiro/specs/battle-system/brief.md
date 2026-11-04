# Brief: battle-system

## Problem

Spawned monsters need a fast cooperative loop that works naturally in a busy Telegram group. Multiple users may press inline buttons nearly simultaneously, so combat must remain fair and consistent despite duplicate, stale, or concurrent callbacks.

## Current State

There is no battle state, action model, cooldown enforcement, damage resolution, monster behavior, reward calculation, or defeat recovery integration.

## Desired Outcome

Any eligible player can join a spawned encounter by taking an action. Each accepted player action immediately advances the battle, after which the monster performs a configured single- or multi-target attack. Players can attack, use an item, or escape; per-player cooldowns prevent one person from monopolizing the fight. Victory grants every non-escaped qualifying participant random loot, with better odds based on meaningful contribution. Defeated characters become temporarily incapacitated.

## Approach

Implement combat as an explicit persisted state machine with a monotonically increasing battle version. Process each callback as one short atomic transition: authenticate the actor, reject stale or duplicate actions, enforce cooldown and eligibility, apply the player action, apply the monster response when still active, update contribution, and commit the next version. Render public battle updates outside the transaction and reconcile delivery through telegram-runtime.

## Scope

- **In**: Automatic participation on first action; Attack, Use Item, and Escape inline actions; immediate step advancement; per-player action cooldown; character and equipment-derived damage; bounded randomness; monster single- and multi-target attacks; persistent player health; victory, defeat, escape, and encounter expiration outcomes; temporary incapacitation; meaningful-contribution tracking; minimum eligibility threshold; random loot for every eligible non-escaped participant; contribution-weighted loot quality or odds; public battle state rendering; callback acknowledgement; idempotency and concurrency tests.
- **Out**: Turn voting, fixed turn order, defend action, classes, skills, targeting choices, PvP, XP and levels, permanent death, world battles, guild mechanics, trading, and currencies.

## Boundary Candidates

- Pure combat rules/state machine with injectable randomness and clock.
- Atomic battle repository transition using battle version and action identity.
- Player cooldown, participation, escape, and contribution accounting.
- Monster behavior selection for configured single- and multi-target attacks.
- Outcome service coordinating character incapacitation and inventory loot grants.
- Public Telegram battle renderer and compact callback encoding.

## Out of Boundary

- Encounter probability and monster selection belong to monster-encounters.
- Base character state belongs to character-system; item definitions/effects belong to item-system; ownership and atomic consumption belong to inventory-system.
- Telegram retries, flood control, and delivery reconciliation belong to telegram-runtime.

## Upstream / Downstream

- **Upstream**: character-system supplies health and eligibility; item-system supplies definitions/effects; inventory-system supplies modifiers, item consumption, and grants; monster-encounters supplies the encounter and monster definition; telegram-runtime supplies callback routing, atomic persistence support, and delivery reconciliation.
- **Downstream**: Future progression, achievements, guilds, and world-event specs can consume battle outcomes without changing the core transition model.

## Existing Spec Touchpoints

- **Extends**: None.
- **Adjacent**: It closes monster-encounters but does not own spawn policy; it coordinates atomic calls to character and inventory boundaries without bypassing their invariants.

## Constraints

Telegram callback payloads must stay within 64 bytes and contain opaque identifiers only. Always answer callback queries promptly. Validate user, chat, encounter, and expected version server-side. Keep SQL transactions short, never include Telegram HTTP calls inside them, use bounded busy handling, and make duplicate delivery harmless. Coalesce visible battle edits and honor Telegram flood-control retry hints.
