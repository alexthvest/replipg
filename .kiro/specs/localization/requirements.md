# Requirements Document

## Introduction

Localization gives each Replipg Telegram group a consistent English or Russian game interface. Today, player-facing messages are largely English while item names and descriptions mix languages. Each group will have one persistent language applying to every player's personal interface and all public bot messages. Players inspect this choice through an ephemeral `/settings` command; only group administrators may change it.

## Boundary Context

- **In scope**: English and Russian translations of all current bot-authored player-facing text, including item content, settings, command descriptions, personal and public messages, controls, guidance, errors, and enabled development interactions; persistent group-wide language selection; administrator authorization; command-menu synchronization; reproducible verification.
- **Out of scope**: Additional languages, per-player language preferences, automatic language detection, translation of player-authored content, operator logs or repository documentation, runtime translation services, translator dashboards, bulk rewriting of sent messages, and implementation of encounters or battles.
- **Adjacent expectations**: Existing features retain character and inventory behavior, actor ownership, restricted delivery, stale-control handling, and replay protection. This feature translates their presentation and adds group settings without changing gameplay. Future encounter and battle features must use the selected group language for their own presentation and content.
- **Confirmed direction**: Embedded, version-controlled English/Russian translation catalogs without an additional localization dependency. English is the default for a group without a saved selection. Detailed translation interfaces, persistence, permission verification, and menu synchronization mechanisms belong in design.

## Requirements

### Requirement 1: Group-Wide Language Selection

**Objective:** As a group member, I want the game to use one language for my group, so that everyone sees a consistent interface.

#### Acceptance Criteria

1. The Localization System shall support English (`en`) and Russian (`ru`) as the only selectable languages.
2. When a group has no saved language selection, the Localization System shall use English for that group's player-facing interface.
3. While a group has a saved language selection, the Localization System shall apply that language to every player's personal interface and all public bot messages in that group.
4. When the same player interacts in groups with different saved languages, the Localization System shall use each originating group's language independently.
5. The Localization System shall keep the group language independent of individual players' client-language settings.
6. When a language change is committed, the Localization System shall use the new language for interactions processed after that change, including subsequent authorized use of previously opened controls.
7. When a group language changes, the Localization System shall leave previously sent messages unchanged until an existing feature would otherwise update them through a new interaction.

### Requirement 2: Ephemeral Settings Inspection

**Objective:** As a group member, I want to inspect the group's language within the group, so that I can understand the current setting without starting a private conversation.

#### Acceptance Criteria

1. When an identified human group member invokes `/settings` through an authorized ephemeral menu command, the Localization System shall show a settings message in that group visible only to the requesting member.
2. When settings are displayed, the Localization System shall identify the current group language and the supported English and Russian choices.
3. When a non-administrator inspects settings, the Localization System shall show the current selection and explain that only group administrators can change it, without presenting an enabled language-change action.
4. When a verified group administrator inspects settings, the Localization System shall provide controls for selecting English or Russian.
5. When settings are inspected or the language is changed, the Localization System shall leave character creation, starter-kit entitlement, inventory, equipment, and health unchanged.
6. If settings lack valid restricted-response authorization or their delivery fails, the Localization System shall not send the settings content as a public group message or a private-chat message.
7. If a settings command lacks an identified human actor or supported group context, the Localization System shall leave group settings unchanged and shall not create a personal settings response.

### Requirement 3: Administrator-Authorized Changes

**Objective:** As a group administrator, I want to control the group's language, so that ordinary members cannot change the experience for everyone.

#### Acceptance Criteria

1. When a language-change request is processed, the Localization System shall verify that its requesting actor is currently an administrator or owner of the originating group before accepting the change.
2. If the actor is not currently a group administrator or owner, or that status cannot be verified, the Localization System shall reject the language change and preserve the saved language.
3. If permission verification cannot be completed and a restricted reply remains authorized, the Localization System shall explain that the language was not changed because administrator status could not be verified.
4. If a settings control is malformed, expired, stale, associated with another actor or group, or requests an unsupported language, the Localization System shall reject the requested change without altering group settings.
5. When an authorized change is committed and a restricted reply remains authorized, the Localization System shall confirm the selected group language to the requesting administrator in that selected language.
6. If a settings change cannot be saved, the Localization System shall preserve the preceding selection and shall not report that the requested language was saved.
7. If a change commits but its personal confirmation cannot be delivered, the Localization System shall retain the committed selection and show it on the next authorized settings inspection.
8. When rejected stale or expired settings controls still permit an authorized restricted response, the Localization System shall provide guidance to reopen `/settings` in the current group language.

### Requirement 4: Complete Interface Translation

**Objective:** As an English- or Russian-speaking player, I want every part of the bot interface in the selected language, so that I can play without interpreting untranslated text.

#### Acceptance Criteria

1. When rendering a player-facing response, the Localization System shall translate all bot-authored text into the current group language, including character summaries, inventory views, settings, creation notices, empty states, recovery guidance, usage messages, and visible error messages.
2. When rendering interactive controls, the Localization System shall translate their visible action and navigation labels into the current group language.
3. When displaying character states, item categories, equipment slots, effects, or equipment markers, the Localization System shall show translated labels rather than internal identifiers.
4. When a response includes dynamic values, the Localization System shall preserve the correct quantities, statistics, health values, durations, deadlines, and ownership references in both languages.
5. When text expresses counts or durations using language-dependent word forms, the Localization System shall use forms appropriate to the selected language and value.
6. When rendering player-provided names or usernames, the Localization System shall preserve their content and the existing presentation-safety rules rather than translate them.
7. Where development-only interactions are enabled, the Localization System shall translate their player-visible messages, buttons, usage guidance, and command descriptions into the group's language.
8. The Localization System shall retain stable command names, including `/character`, `/inventory`, and `/settings`, in both languages.

### Requirement 5: Translated Items and Gameplay Compatibility

**Objective:** As a player, I want item names and details in the group's language, so that I can understand my inventory without changing the items I own.

#### Acceptance Criteria

1. When an item appears in a player-facing view, the Localization System shall display its name and description in the group language wherever those fields are shown.
2. The Localization System shall provide English and Russian names and descriptions for every retained item definition, including items already owned by existing characters.
3. When a group language changes, the Localization System shall preserve item identities, owned quantities, equipped assignments, categories, effects, numeric mechanics, and starter-kit receipt.
4. When the same item is displayed in different languages, the Localization System shall preserve the meaning of its description and its displayed mechanical effects.
5. When existing inventory controls are used after a language change, the Localization System shall preserve their existing ownership, validity, and stale-control rules while rendering any resulting view in the current group language.
6. When an inventory or character operation is processed in either language, the Localization System shall preserve its existing gameplay outcome and replay protection.

### Requirement 6: Translated Command Menus

**Objective:** As a group member, I want command descriptions to match the group's language, so that I can discover the available actions.

#### Acceptance Criteria

1. When the application-managed command menu for a group is synchronized, the Localization System shall publish descriptions for the complete enabled command set in that group's language, including `/settings`.
2. When publishing translated command descriptions, the Localization System shall preserve command names and their ephemeral interaction behavior.
3. When a saved group language changes, the Localization System shall request synchronization of that group's managed command menu without changing another group's selected language or menu.
4. If command-menu publication fails after a language selection is saved, the Localization System shall retain that selection and preserve the pending menu synchronization for retry and restart recovery.
5. While a saved language selection has not yet been reflected in the command menu, the Localization System shall distinguish the saved selection from pending menu synchronization in authorized settings feedback.
6. When the runtime restarts or its enabled command set changes, the Localization System shall reconcile previously managed group menus with the saved group language and current enabled commands.
7. When command-menu synchronization succeeds, the Localization System shall clear the corresponding pending status without requiring the administrator to select the language again.
8. The Localization System shall document which command-menu scopes it manages and the operator steps needed to resolve competing overrides that would prevent the group's selected descriptions from appearing.

### Requirement 7: Persistence and Failure Consistency

**Objective:** As a group administrator, I want language settings to survive failures and restarts, so that the group retains its chosen interface.

#### Acceptance Criteria

1. When a language selection is successfully saved, the Localization System shall retain it across restarts using the same persistent storage.
2. When an accepted language-change request is replayed, the Localization System shall not repeat its mutation or overwrite a later accepted selection.
3. When different language-change requests affect the same group concurrently, the Localization System shall produce a result consistent with an ordering of accepted changes and synchronize its menu toward the resulting current selection.
4. If the saved language cannot be read, the Localization System shall report a distinguishable operational failure rather than treat the failure as an absent selection or overwrite the saved language with English.
5. If a stored selection is unsupported or invalid, the Localization System shall report a distinguishable operational failure and reject changes that would conceal or silently replace that invalid selection.
6. When a response delivery is retried after a language change, the Localization System shall preserve the language and gameplay result of that already prepared response.
7. The Localization System shall exclude private settings content, private response content, credentials, and personal message bodies from logs and operational diagnostics.

### Requirement 8: Translation Completeness and Presentation Validity

**Objective:** As an operator, I want incomplete or invalid translations detected before serving players, so that supported languages do not expose broken interfaces.

#### Acceptance Criteria

1. When preparing to accept player interactions, the Localization System shall validate that both languages cover all required player-facing messages, command descriptions, and retained item names and descriptions before accepting those interactions.
2. If required translations are missing, empty, duplicated, malformed, or inconsistent in required dynamic values, the Localization System shall reject the translation content and report an actionable diagnostic without accepting player interactions.
3. The Localization System shall not conceal missing required translations by showing raw translation keys, unresolved placeholders, or text from the other supported language.
4. When rendering messages, controls, command descriptions, and item text, the Localization System shall preserve valid Unicode and satisfy applicable presentation limits and formatting-safety rules in both languages.
5. When preparing to accept player interactions, the Localization System shall reject translated item names and descriptions that violate the existing item-content validation rules before accepting those interactions.

### Requirement 9: Reproducible Incremental Verification

**Objective:** As a maintainer, I want to verify localization in runnable increments, so that language support preserves current behavior and group privacy.

#### Acceptance Criteria

1. The Localization System shall support deterministic automated verification of English and Russian rendering, translation completeness, language-dependent word forms, existing-item compatibility, and unchanged gameplay outcomes without contacting real Telegram.
2. The Localization System shall support deterministic automated verification of independent group settings, administrator and non-administrator access, failed permission verification, stale controls, duplicate and concurrent changes, save failures, and restart persistence.
3. The Localization System shall support automated verification of command-menu publication failure, pending status, retry, restart reconciliation, and enabled-command changes without contacting real Telegram.
4. When an implementation milestone is delivered, the Localization System shall provide automated checks, a runnable group interaction, and documented commands and expected observations for a Telegram test-group demonstration.
5. The Localization System shall document the bot permissions required for administrator verification and the observable behavior when those permissions are unavailable.
