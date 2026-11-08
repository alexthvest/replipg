# Localization

## Purpose

Provide a consistent English or Russian experience per group, with language selection inside settings and automatic validated translation updates.

## ADDED Requirements

### Requirement: Language belongs to the group
The system SHALL support English and Russian, default new groups to English, and persist one language choice per group. All newly rendered bot messages, menu text, buttons, and rarity labels SHALL use the current group language. User-authored character names MUST remain unchanged.

#### Scenario: Default language
- **WHEN** the bot first renders an interface for a group without a saved language
- **THEN** it uses English regardless of individual users' Telegram language preferences

#### Scenario: Independent persistent settings
- **WHEN** group A selects Russian and the bot restarts
- **THEN** group A continues to use Russian and group B retains its own language

### Requirement: Settings exposes language buttons
The `/settings` command SHALL open a group message with English and Russian selection buttons and a visible indication of the currently selected language. Only the bot owner and current group's game admins SHALL be able to change the selection.

#### Scenario: Open settings
- **WHEN** a user invokes `/settings` in a supported group
- **THEN** the bot renders the menu in that group's language with both choices and the current choice marked

#### Scenario: Unauthorized selection
- **WHEN** a user without current group privileges presses a language button
- **THEN** the saved language and menu selection remain unchanged
- **AND** the bot acknowledges the callback without posting a language-change notification message

### Requirement: Language selection updates the existing menu
An authorized language selection SHALL persist the group language and edit the originating settings message so its text and selected-button state reflect the saved language. The bot MUST acknowledge the callback and MUST NOT post a separate language-change notification message.

#### Scenario: Switch to Russian
- **WHEN** an authorized user presses Russian on the current group's settings menu
- **THEN** the group language is saved as Russian and the same message is edited into Russian with Russian marked as selected
- **AND** no new language-change notification message is sent

#### Scenario: Select the current language
- **WHEN** an authorized user selects the already active language
- **THEN** the callback is acknowledged, the current selection remains correct, and no additional chat message is sent

#### Scenario: Persistence fails
- **WHEN** saving a requested language fails
- **THEN** the previous language and selected state remain authoritative
- **AND** the callback reports failure without posting a separate language-change message

#### Scenario: Menu edit fails after saving
- **WHEN** the language is saved but Telegram refuses the originating message edit
- **THEN** the saved language remains authoritative and the failure is logged
- **AND** reopening `/settings` shows the saved selection without a separate language-change notification

### Requirement: Settings callbacks stay within their group
The system SHALL validate settings callbacks against the originating group, message, recognized language, and active menu session before changing language. Invalid or expired callbacks MUST NOT change settings.

#### Scenario: Forged or stale settings callback
- **WHEN** a callback references a foreign group or message, an unsupported language, or an expired session
- **THEN** the callback is rejected and no group's language changes

### Requirement: Locale files reload automatically
The system SHALL watch locale asset files and automatically validate a complete candidate set of items and translations before atomically publishing it. A failed candidate MUST leave the last valid set active and produce a diagnostic. Reload MUST require neither restart nor a command.

#### Scenario: Valid translation edit
- **WHEN** English or Russian locale files change and the complete candidate asset set is valid
- **THEN** newly rendered messages and menus use the updated translations
- **AND** previously sent messages need not be edited until their next interaction

#### Scenario: Invalid or incomplete locale edit
- **WHEN** a candidate has invalid syntax, a missing required locale or key, or an unresolved item translation reference
- **THEN** the active items and translations remain unchanged and validation errors are logged

#### Scenario: Concurrent render during publication
- **WHEN** assets reload while a response is being rendered
- **THEN** that response uses one consistent asset version rather than a mixture of old and new assets

### Requirement: Both supported languages are complete
The system SHALL require usable English and Russian translations for all required interface keys, rarity labels, and active item name and description references. User-authored and asset-authored text MUST be rendered safely without unintended Telegram formatting.

#### Scenario: Translation parity violation
- **WHEN** a candidate contains a required key only in English
- **THEN** validation rejects the candidate rather than exposing raw keys or silently mixing interface languages

#### Scenario: Formatting characters in text
- **WHEN** rendered text contains Telegram formatting characters
- **THEN** they appear as intended text without creating unintended formatting or breaking the response
