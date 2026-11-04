# Brief: localization

## Problem

English- and Russian-speaking groups need a consistent translated game interface. Current player-facing messages are largely hard-coded English, while the item catalog mixes Russian names with English descriptions. A group cannot currently choose its language.

## Current State

The Go modular monolith has implemented Telegram runtime, character, and inventory interfaces. Feature renderers own message text; composition registers command descriptions; the Telegram adapter publishes one default-language command menu for all groups. SQLite persists gameplay state, but there is no language preference or settings command.

Item definitions combine stable IDs and mechanics with display names and descriptions. Translation must preserve existing identities, ownership, quantities, equipment, effects, and starter-kit receipt. Character status, inventory categories, and equipment slots currently include machine values that also need translated presentation.

## Desired Outcome

Each group has one persistent language, English (`en`) or Russian (`ru`), applying to all players' personal interfaces and all public bot messages in that group. Everyone can inspect the setting through the ephemeral `/settings` menu command; only current group administrators can change it. Language choice survives restarts and is independent across groups, including when the same player participates in multiple groups.

All bot-authored player-facing text is available in both languages: command descriptions, settings, character summaries, inventory navigation and buttons, item names and descriptions, category and status labels, guidance, usage messages, and visible error responses. Development-only bot messages are included when their features are enabled. Future encounters and battles consume the same localization capability.

## Approach

Use embedded, version-controlled English and Russian translation catalogs with stable message keys and parameterized messages, using the Go standard library without a new localization dependency. Resolve the current group locale before rendering; keep locale selection separate from gameplay mechanics. Resolve translated item presentation by stable item ID.

Use one flat JSON string map per language, as subsequently selected by the user. Ordinary messages use dotted keys; plural variants append `.one`, `.few`, `.many`, or `.other`; item text uses `items.<stable-id>.name` and `items.<stable-id>.description`. Do not wrap entries in nested message/item objects or put metadata inside the translation map.

Store a validated group preference through existing persistence boundaries and expose settings through existing authenticated group interactions. Keep command names such as `/character`, `/inventory`, and `/settings` stable; translate their descriptions. Publish group-specific menus reflecting the saved language rather than relying on Telegram client-language selection.

Validate catalog completeness, required parameters, item translation coverage, and supported text limits before admitting gameplay traffic. Cover English and Russian grammatical forms where counts or durations appear. Language changes must never change machine identifiers or gameplay outcomes.

This approach was selected by the user over typed Go translation bundles and an external i18n library. A read-only independent viability review found integration constraints but no blocking dependency or platform issue; no new third-party dependency or license is proposed.

## Scope

- **In**: Embedded `en`/`ru` catalogs; group-language persistence and resolution; ephemeral `/settings`; administrator-authorized changes; translated current player-facing UI and content; group-specific command descriptions; retry/reconciliation of menu publication; completeness checks; tests and documented Telegram demonstrations.
- **Out**: Additional languages; per-player preferences; automatic language detection; runtime translation services; translator dashboards; player-authored text translation; operator log, health endpoint, source-code, and repository-documentation translation; implementation of encounters or battles.

## Boundary Candidates

- Shared translation catalogs, parameter handling, validation, and item presentation lookup.
- Group settings persistence and authorization, without creating a character or granting inventory.
- Locale-aware rendering in existing features through an approved shared interface or dependency direction.
- Telegram command-menu synchronization and recovery, while transport remains free of gameplay rules.

## Out of Boundary

- Character and inventory remain owners of their state, mechanics, eligibility, and idempotent operations.
- Telegram Runtime remains owner of transport, restricted delivery, retries, and lifecycle capabilities.
- Settings changes must not reset characters, recreate inventory, translate callback tokens, or alter item IDs, categories, slots, or numeric mechanics.
- Existing sent messages are not bulk-translated. The next authorized interaction renders in the current group language; proactive rewriting of historical messages is excluded.

## Upstream / Downstream

- **Upstream**: telegram-runtime supplies group/actor identity, ephemeral authorization, callbacks, persistent transactions, and delivery recovery. Existing character and inventory interfaces supply the current translation surfaces and stable content identities.
- **Downstream**: monster-encounters and battle-system must use the group language and shared localization interface for future public messages, controls, monster content, and loot presentation. Their gameplay implementation remains in their own specs.

## Existing Spec Touchpoints

- **Extends**: None as a separate existing-spec workstream; this new cross-cutting feature owns language behavior and the adaptations needed in existing interfaces.
- **Adjacent**: telegram-runtime command-menu and delivery capabilities; character-system rendering and ownership; inventory-equipment catalog validation, item presentation, ephemeral message editing, and stale controls. Preserve their gameplay and privacy contracts.

## Constraints

- Confirmed by the user: one language per group; English and Russian; embedded translation catalogs; everyone can view settings; only group administrators can change the language.
- Discovery default: English for groups without a saved selection. Do not derive group language from an individual player's Telegram client language.
- Preserve the single-replica Go/SQLite architecture, use additive compatible migrations, and follow pinned devenv tooling. No translation API or additional service is needed.
- Preserve current restricted-delivery behavior, callback actor/chat binding, expiry checks, invisible acknowledgement, and absence of public/private-chat fallback. Recheck administrator authorization for each settings mutation; reject unauthorized, unverifiable, stale, or malformed changes safely.
- Administrator lookup may require bot administrator privileges for a guaranteed result. Specify supported permission verification and deployment requirements during design; do not silently authorize when verification fails. Telegram documents this constraint at https://core.telegram.org/bots/api#getchatmember.
- Telegram menu language codes follow the Telegram user's language rather than a group preference. Use chat-scoped menus with translated descriptions and define ownership of competing scopes. Intentionally update the README restriction against per-chat menus. References: https://core.telegram.org/bots/api#determining-list-of-commands and https://core.telegram.org/bots/api#setmycommands.
- SQLite preference updates and Telegram menu publication cannot commit atomically. Define recoverable synchronization, truthful settings feedback, restart reconciliation, and refresh when the registered command set changes. Failed menu delivery must not silently lose the saved preference.
- Use a shared translation boundary or inject narrow interfaces; preserve character and inventory dependency direction.
- Preserve current item mechanics and retained definitions. Carry forward intentional catalog edits, reconcile bilingual wording, and validate translated names/descriptions against existing limits. Runtime fallback must never conceal missing shipped translations; define unsupported/corrupt locale handling during requirements/design.
- Future persisted delivery must retain a deterministic rendered result or sufficient locale information for replay; do not let retries change an already accepted gameplay outcome.
- Deliver runnable vertical milestones with deterministic automated checks and documented live Telegram demonstrations. Verify both languages, independent groups, authorization, restart persistence, existing-item compatibility, stale controls, and menu failure recovery.
