# crafting-menus Specification

## Purpose

Let players compose ingredient quantities deliberately through Telegram inline menus and continue safely from pending and completed crafting attempts.

## Requirements

### Requirement: Crafting opens on a paged composition
The `/craft` command SHALL open an empty composition. Each page SHALL show up to six selected ingredients with exact quantities and one row of matching page-local numbered buttons. Pagination SHALL use a separate row. The final two rows SHALL be Add / Clear and Confirm / Close, in that order, localized to the group language. Six SHALL NOT limit total ingredient types.

#### Scenario: Empty and populated compositions
- **WHEN** a player opens crafting and later adds seven ingredients
- **THEN** the initial screen explains that the composition is empty, with no numbered buttons
- **AND** the populated composition has pages of six and one with the specified action rows

### Requirement: Adding materials uses an inventory-style browser
Add SHALL open eligible available materials in inventory acquisition order, six per page with page-local numbered buttons and separate pagination. Selecting a number SHALL open quantity editing without adding the material yet. Selecting an existing ingredient SHALL edit its quantity without creating a duplicate. Back SHALL preserve the composition and return to it.

#### Scenario: Browse without selecting
- **WHEN** a player browses several pages and returns to the composition
- **THEN** ingredients and quantities remain unchanged

#### Scenario: Material already selected
- **WHEN** a player selects an existing ingredient from the material browser
- **THEN** its current quantity is offered for editing rather than a second composition entry

### Requirement: Quantity editing has explicit draft commit
Composition numbers SHALL open the same editor as material selection. Stackable drafts SHALL offer `1`, `-10`, `-1`, `+1`, `+10`, and localized Max in one row, clamped to available positive quantities. Add or Save SHALL commit and return to the composition. Back SHALL discard draft changes and restore the originating page. Remove SHALL be offered only for existing ingredients. Individual materials SHALL use exact instances and no quantity controls.

#### Scenario: Abandon a quantity edit
- **WHEN** a player changes an existing quantity and presses Back
- **THEN** the saved composition retains the previous quantity and no ownership changes

#### Scenario: Save and remove
- **WHEN** a player saves a revised quantity or removes an existing ingredient
- **THEN** only that ingredient changes and the composition returns on a valid page

#### Scenario: Add an individual material
- **WHEN** a player opens an individual material and presses Add
- **THEN** that exact instance is selected once with quantity one

### Requirement: Review is required for each new attempt
Confirm SHALL open a paged review of the complete valid composition explaining one-item yield, consumption on success, and preservation on technical error. Final confirmation SHALL submit at most one attempt. Empty or invalid compositions SHALL produce feedback without submission. Any composition mutation SHALL invalidate an unsubmitted confirmation.

#### Scenario: Repeated confirmation delivery
- **WHEN** the same final confirmation is delivered twice
- **THEN** it resolves the same durable attempt without duplicate consumption or output

#### Scenario: Empty composition
- **WHEN** a player presses Confirm without ingredients
- **THEN** the menu asks them to add materials and creates no attempt

### Requirement: Shortages preserve the requested composition
Availability changes SHALL NOT silently reduce selected quantities or remove missing ingredients. Composition and shortage feedback SHALL identify required and available quantities, including zero for missing entries. Players SHALL be able to edit or remove every invalid selection. A shortage screen SHALL offer Change composition / Clear, then Close. Sale reservations SHALL remain unavailable.

#### Scenario: Ingredient disappears
- **WHEN** another operation consumes a selected ingredient before confirmation
- **THEN** its saved name and requested amount remain visible with zero availability and removal remains possible
- **AND** confirming creates no invalid attempt

### Requirement: Clear and Close have distinct effects
Clear SHALL reset the composition and pending editor or review state and return to an empty composition. Close SHALL end the active menu and remove its controls. Neither action SHALL consume inventory. Closing an accepted pending attempt SHALL NOT cancel or repeat it, and closed menus SHALL receive no subsequent automatic edits.

#### Scenario: Close during generation
- **WHEN** a player closes a pending craft
- **THEN** the accepted durable attempt continues independently and its result remains available through inventory
- **AND** its old menu controls cannot perform another action

### Requirement: Pending and failure screens offer appropriate actions
Pending SHALL display the submitted composition, explain that materials are not reserved, and offer Refresh / Close. Technical failure SHALL state that materials were preserved and offer Retry on its own row followed by Change composition / Close. Disabled discovery SHALL explain the limitation and offer Change composition / Close. Retry SHALL open a fresh review rather than immediately submit.

#### Scenario: Retry a technical failure
- **WHEN** a player presses Retry after technical failure
- **THEN** current availability is checked and a fresh review or shortage screen is shown
- **AND** no attempt starts until renewed final confirmation

#### Scenario: Pending craft completes
- **WHEN** an accepted attempt completes while its menu remains active
- **THEN** that same message automatically shows the result in the current group language

### Requirement: Success supports repeating and starting another composition
Success SHALL show the saved output name, description, quantity one, and consumed ingredients, paged when necessary. Its actions SHALL include Repeat / New craft followed by Inventory / Close. Repeat SHALL restore the original exact saved ingredient definitions and requested quantities against current ownership and return to composition without submitting. New craft SHALL open an empty composition. Inventory SHALL open the player's current-group inventory.

#### Scenario: Repeat after ingredient consumption
- **WHEN** a player presses Repeat and insufficient matching ingredients remain
- **THEN** the requested composition is restored with visible shortages without silently reducing quantities or substituting different snapshots

#### Scenario: Result links to inventory
- **WHEN** the player presses Inventory
- **THEN** the menu shows their current-group inventory with ordinary inventory navigation and opener controls

### Requirement: Material results can seed another craft
A material result SHALL offer Use in crafting on a row before the other success actions. This action SHALL open a new composition with one currently available owned entry matching the saved output snapshot. It SHALL NOT grant or consume items. Nonmaterial results SHALL omit the action. Missing or fully reserved outputs SHALL produce feedback without selecting unavailable material.

#### Scenario: Reuse a material result
- **WHEN** a player presses Use in crafting with an available matching output
- **THEN** a new composition contains that material with quantity one, allowing additional ingredients

#### Scenario: Output was sold
- **WHEN** the output is no longer available when Use in crafting is pressed
- **THEN** the menu explains its unavailability and no inventory mutation or new attempt occurs

### Requirement: Crafting navigation preserves menu ownership and delivery rules
Every screen SHALL validate opener, group, message, active session, and current ownership before acting. Superseded numbered controls SHALL be rejected. Automatic refresh SHALL preserve valid screen, pages, and draft values while updating availability and language. Long compositions and saved names SHALL remain within Telegram message limits through pagination and bounded previews.

#### Scenario: Stale or foreign numbered control
- **WHEN** another player or a superseded page callback selects a number
- **THEN** no draft, composition, or inventory changes

#### Scenario: Refresh while editing
- **WHEN** inventory or group language changes during quantity editing
- **THEN** the editor retains its draft and origin page, reports current availability, and uses current localized labels
