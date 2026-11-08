# Spec Delta

## MODIFIED Requirements

### Requirement: Locale files reload automatically
The system SHALL watch locale asset files and automatically validate a complete candidate set of items, shop offers, and translations before atomically publishing it. A failed candidate MUST leave the last valid set active and produce a diagnostic. Successful publication SHALL automatically refresh affected active menus. Reload MUST require neither restart nor a command.

#### Scenario: Valid translation edit
- **WHEN** English or Russian locale files change and the complete candidate asset set is valid
- **THEN** newly rendered messages and menus use the updated translations
- **AND** affected active menu messages update without waiting for their next interaction
- **AND** immutable owned item text remains based on its saved snapshot

#### Scenario: Invalid or incomplete locale edit
- **WHEN** a candidate has invalid syntax, a missing required locale or key, or an unresolved item translation reference
- **THEN** the active items, shop offers, and translations remain unchanged and validation errors are logged

#### Scenario: Concurrent render during publication
- **WHEN** assets reload while a response is being rendered
- **THEN** that response uses one consistent asset version rather than a mixture of old and new assets
