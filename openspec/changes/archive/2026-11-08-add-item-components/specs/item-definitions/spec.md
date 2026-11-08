## ADDED Requirements

### Requirement: Items compose descriptive marker components
An item definition SHALL support a `components` object containing any combination of `weapon`, `consumable`, and `material`. Each present component MUST have an empty object value. An omitted or empty `components` object SHALL mean no components. Components MUST NOT imply rarity, ownership mode, or gameplay actions.

#### Scenario: Multiple classifications
- **WHEN** an item declares `components: {consumable: {}, material: {}}`
- **THEN** its captured classification contains both components
- **AND** its explicit rarity and ownership mode remain authoritative

#### Scenario: Unclassified item
- **WHEN** an item omits components or declares `components: {}`
- **THEN** it is valid and captures an empty component set

### Requirement: Component validation rejects unsupported content
Validation SHALL reject unknown component types, non-object component containers or values, null values, and nonempty marker properties. Invalid reload candidates MUST preserve the entire previous active bundle and log diagnostics. Invalid required startup assets MUST prevent startup.

#### Scenario: Invalid component declarations
- **WHEN** a candidate declares an unknown component, `components: []`, `components: null`, `weapon: true`, `weapon: null`, or `weapon: {damage: 10}`
- **THEN** validation rejects the candidate
- **AND** a running process retains its previous active bundle

### Requirement: Components are captured as immutable item data
Every grant SHALL capture components with all other snapshot data from one validated bundle. Later component edits or definition deletion MUST NOT change existing snapshots. Valid component edits SHALL be reloadable for continuously active IDs without changing their ownership modes.

#### Scenario: Change classification after a grant
- **WHEN** a material item is granted and its active definition later adds consumable
- **THEN** the owned item remains material only, including after restart
- **AND** new grants capture both components

#### Scenario: Publication during capture
- **WHEN** a component edit is published during a grant
- **THEN** the saved components, rarity, mode, and bilingual item text all come from one validated bundle

#### Scenario: Remove the definition
- **WHEN** an item definition is deleted after granting a classified item
- **THEN** the saved components remain available without consulting the deleted definition

### Requirement: Legacy snapshots have empty components
Saved snapshots without a components field SHALL remain readable and represent an empty component set. Loading them MUST NOT infer components from current assets, change their durable identities, or rewrite historical grant and shop records.

#### Scenario: Restart with old ownership and historical records
- **WHEN** the process loads pre-component inventory snapshots, saved grant results, or shop orders
- **THEN** their snapshots remain usable with empty components and their original saved item data
- **AND** a currently classified catalog definition does not reclassify those snapshots
