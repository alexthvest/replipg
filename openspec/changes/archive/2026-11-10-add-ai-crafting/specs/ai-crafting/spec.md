# AI Crafting Spec Delta

## Purpose

Let players discover imaginative new items by combining owned materials, sharing permanent quantity-sensitive successful recipes across every group. Generated items may be unusual, funny, surreal, or hybrid creations without an RPG or fantasy theme restriction. Every resolved craft creates one item; technical failures and inventory conflicts preserve ownership.

## ADDED Requirements

### Requirement: Only saved material components permit ingredients
Crafting SHALL accept only owned inventory entries whose saved components include `material`. Other supported components MUST NOT disqualify a material. Generated materials SHALL be eligible for subsequent crafting under the same rules. Live catalog edits MUST NOT change eligibility of an already owned item.

#### Scenario: Composite material
- **WHEN** a player selects an owned item containing both weapon and material components
- **THEN** it is eligible as an ingredient

#### Scenario: Generated material used again
- **WHEN** a crafted item has a saved material component
- **THEN** its owner can select it for another recipe

#### Scenario: Unclassified or changed catalog item
- **WHEN** an owned snapshot lacks material, even if its live definition now contains material
- **THEN** crafting rejects it as an ingredient without consuming items

### Requirement: Recipes preserve exact ingredient quantities
A recipe SHALL identify a nonempty, order-independent combination of complete saved ingredient definitions and exact positive integer quantities. Identical definitions SHALL be aggregated across selected entries. Group, player, inventory entry, and instance identities MUST NOT distinguish recipes. Different saved definitions or quantities SHALL distinguish recipes. There SHALL be no fixed gameplay limit on the number of distinct ingredient types.

#### Scenario: Reordered and split selections
- **WHEN** two crafts use the same saved definitions and total quantities in a different order or split across different stacks or individual instances
- **THEN** they identify the same recipe

#### Scenario: Quantities matter
- **WHEN** one craft uses two sticks and one coin and another uses one stick and one coin
- **THEN** they identify different recipes

#### Scenario: Item versions differ
- **WHEN** ingredients share a catalog ID but differ in saved text, components, rarity, or mode
- **THEN** substituting one saved version for the other identifies a different recipe

#### Scenario: Many ingredient types
- **WHEN** a player selects more than two distinct material types with available positive quantities
- **THEN** the selection is accepted without an ingredient-type count restriction

### Requirement: Unknown recipes use a locally running model
Unknown recipe discovery SHALL use a locally running AI model through a configured local inference service. The system MUST NOT fall back to hosted inference. An unavailable local service SHALL cause a technical failure without ingredient consumption or a saved negative recipe. Known recipes SHALL remain usable without the model service.

#### Scenario: Local generation
- **WHEN** a valid unknown recipe requires an AI decision
- **THEN** the request is sent to the configured local model service
- **AND** no hosted inference service is used

#### Scenario: Local model unavailable
- **WHEN** the local model service cannot be reached
- **THEN** unknown discovery reports technical failure without consuming ingredients or saving a negative recipe
- **AND** known recipes still resolve from their saved outcomes without a hosted fallback

### Requirement: First discovery establishes a permanent global outcome
An unknown recipe SHALL obtain a new generated item definition. Gameplay uncraftable outcomes MUST NOT be accepted or cached. The first durably saved valid successful definition SHALL be authoritative globally and survive restart. Known recipes MUST reuse that definition without a new AI decision. Concurrent discovery MUST NOT establish conflicting outcomes.

#### Scenario: Another group repeats a discovery
- **WHEN** group B crafts the same recipe previously discovered in group A
- **THEN** it uses the same saved successful definition without calling AI again

#### Scenario: Concurrent first discoveries
- **WHEN** two players confirm the same unknown recipe concurrently
- **THEN** both resolved attempts use the single durable authoritative outcome
- **AND** an alternate late AI response cannot replace it

### Requirement: Generation encourages unrestricted creative combinations
Generation SHALL allow imaginative combinations and recognizable hybrids of the submitted materials without restricting results to conventional RPG or fantasy objects. Incompatible, implausible, or unusual ingredient combinations MUST NOT produce a gameplay rejection. A valid completed generation SHALL always define an item.

#### Scenario: Creative hybrid
- **WHEN** eligible Fumo and Neco-Arc materials are combined
- **THEN** generation may create a hybrid such as Fumo-Neco-Arc with bilingual text preserving the combined idea
- **AND** it does not reject the combination for lacking a conventional RPG use

#### Scenario: Unusual ingredients
- **WHEN** a valid material combination has no realistic or thematic crafting explanation
- **THEN** the generator invents a creative item instead of declaring the recipe uncraftable

### Requirement: Generation favors emergent concepts with grounded descriptions
The generation prompt SHALL favor a new use, mechanism, behavior, or identity arising from ingredient interactions over literal attachments, decoration, or joined names alone. Every ingredient SHALL matter to the central concept. It SHALL request short, natural names and one or two concrete description sentences showing the interaction without elaborate lore or unsupported powers.

#### Scenario: Ordinary materials produce a simple item
- **WHEN** players combine ordinary materials without special properties
- **THEN** the generation prompt favors a coherent item whose function emerges from the materials working together, with a short name and brief concrete description
- **AND** it does not require magical effects, elaborate lore, or a separate ability for every ingredient
- **AND** quantities and rarity do not automatically imply greater size, strength, or magical effects

#### Scenario: Unusual ingredients contribute their traits
- **WHEN** submitted ingredient descriptions include distinctive character or behavioral traits
- **THEN** the generation prompt encourages those traits to interact in the resulting identity or behavior
- **AND** joining ingredient names or attaching one ingredient to another is not sufficient by itself

### Requirement: Generation uses quantities as material budgets
The generation prompt SHALL treat quantities as independent material budgets that influence feasible construction, coverage, and result identity. Surplus of one material SHALL NOT substitute for another missing material. When a complete item needs more material, the prompt SHALL favor a smaller result or intermediate instead of rejection. Quantity SHALL NOT automatically increase rarity, power, or output count.

#### Scenario: More covering material supports a larger item
- **WHEN** the same prepared components are combined with different amounts of covering material
- **THEN** the prompt asks the model to consider whether a small component, small finished item, or larger finished item fits those amounts
- **AND** quantities remain part of the exact recipe identity
- **AND** illustrative amounts such as two leather for handwear and five for trousers do not become universal hard-coded recipes

#### Scenario: One material remains scarce
- **WHEN** a recipe contains abundant lining but little leather
- **THEN** the prompt does not treat the extra lining as additional leather
- **AND** it requests a coherent result fitting both material budgets without a gameplay rejection

### Requirement: Generation favors meaningful crafting stages
The English generation prompt SHALL request a meaningful next step from raw substances through processed materials, components, and subassemblies toward complete objects. It SHALL discourage skipped functions, unchanged renamed inputs, and redundant intermediates when the supplied functions already suffice. It SHALL request material eligibility for continued crafting and empty component markers.

#### Scenario: Basic substances advance
- **WHEN** a new recipe combines basic substances
- **THEN** the prompt favors an appropriate processed material or component rather than an unsupported sophisticated device

#### Scenario: Prepared parts can complete an object
- **WHEN** prepared ingredients already provide the functions of a coherent complete object
- **THEN** the prompt asks for that next-stage object instead of another nearly identical intermediate
- **AND** the amount of each material still limits feasible construction

### Requirement: Generated definitions support item composition and persistence
A successful AI decision SHALL define one new item with a stable system-assigned identity, usable English and Russian names and descriptions, a supported rarity, an explicit ownership mode, and any supported component combination including none. Invalid components or fields MUST be rejected. The saved result MUST remain unchanged by later AI settings or catalog reloads and remain usable without a project-file definition.

#### Scenario: AI chooses multiple components
- **WHEN** a valid AI success defines a consumable material
- **THEN** both components are preserved in the saved result and all future crafts of that recipe

#### Scenario: Invalid generated definition
- **WHEN** AI success lacks a required translation, uses unsupported rarity, or declares unknown components or component properties
- **THEN** no recipe outcome is accepted and no ingredients are consumed

#### Scenario: Restart or reload
- **WHEN** a successful recipe and its crafted ownership survive a restart or item asset reload
- **THEN** subsequent crafts and inventory inspection retain the complete saved generated definition

### Requirement: Every resolved craft consumes its submitted materials
Every resolved craft SHALL consume exactly the confirmed ingredient quantities and grant exactly one item using the saved successful definition. There SHALL be no resolved gameplay failure that consumes materials without an item. Consumption, output grant, and the attempt's successful terminal result MUST commit atomically and MUST NOT alter other groups' or players' ownership.

#### Scenario: Successful craft
- **WHEN** a player resolves a recipe with a saved success outcome
- **THEN** the submitted ingredients disappear or decrease by the confirmed amounts and one result item is granted in that player's group inventory

#### Scenario: Repeated successful recipe
- **WHEN** a later distinct confirmation repeats a successfully discovered recipe
- **THEN** it consumes its own submitted quantities and grants one item with the same saved definition
- **AND** it does not call AI again

#### Scenario: Transaction fails
- **WHEN** persistence fails during ingredient removal or output grant
- **THEN** no partial consumption, output, or terminal attempt result commits
- **AND** a previously saved recipe outcome remains available for recovery

### Requirement: Confirmation is scoped and idempotent
Each confirmation SHALL be bound to the acting player, originating group and message, active session, exact selected entries, and quantities. One confirmation MUST authorize at most one attempt; duplicate transport delivery, repeated presses of that confirmation, worker retries, and restart recovery MUST NOT consume or grant twice. A new intentional attempt SHALL require a new confirmation.

#### Scenario: Duplicate confirmation and recovery
- **WHEN** a confirmation is pressed twice, redelivered, or its pending attempt is recovered after restart
- **THEN** it resolves at most once and reuses its durable result

#### Scenario: Foreign or expired confirmation
- **WHEN** another player or a foreign group/message uses a confirmation, or a new submission uses an expired session
- **THEN** no attempt or inventory mutation is created
- **AND** expiry of a menu does not cancel an already accepted durable attempt

### Requirement: Unavailable inventory cannot be consumed
Crafting SHALL validate ingredient ownership, saved material eligibility, positive quantities, and availability at confirmation and fulfillment. Sale-reserved quantities MUST remain unavailable. If inventory changes while discovery is pending and selected quantities become unavailable, the attempt SHALL fail without consumption or output; any saved global recipe outcome SHALL remain reusable.

#### Scenario: Reserved units
- **WHEN** a stack contains ten units with three reserved for a shop sale
- **THEN** crafting can consume at most seven and preserves the sale reservation

#### Scenario: Concurrent use of the same materials
- **WHEN** another craft or sale leaves fewer available units than a pending attempt requires
- **THEN** that attempt fails without partial consumption or an output grant

#### Scenario: Invalid quantity or ownership
- **WHEN** a submission references foreign entries, empty ingredients, zero, negative, fractional, overflowing, or unavailable quantities
- **THEN** it is rejected without an AI request or inventory mutation

### Requirement: Technical failures preserve ingredients
Provider timeouts, transport failures, refusals, malformed responses, invalid generated data, cancellation, and unsuccessful model decisions SHALL consume no ingredients and MUST NOT create a saved negative recipe. Technical failure feedback MUST permit a later intentional retry. An old uncraftable response SHALL be treated as invalid output rather than a gameplay outcome.

#### Scenario: Service unavailable
- **WHEN** an unknown recipe cannot obtain a valid generated item because of a timeout or invalid response
- **THEN** the attempt reports technical failure with unchanged ownership and no negative recipe

#### Scenario: Model returns uncraftable
- **WHEN** a model returns a former explicit uncraftable variant instead of a complete generated item
- **THEN** the response is a technical failure without ingredient consumption or cached rejection
- **AND** a later new confirmation may retry the recipe

### Requirement: Former cached rejections become undiscovered recipes
An additive migration SHALL clear cached uncraftable outcomes and reset their jobs to pending without stale claims. It MUST preserve inventory, reservations, canonical identities, successful recipes, and historical attempts. Historical rejected confirmations MUST remain terminal without consumption or retroactive grants. Already accepted pending attempts MUST remain recoverable.

#### Scenario: Previously rejected recipe is confirmed again
- **WHEN** a database containing a cached uncraftable recipe is upgraded and a player submits a new confirmation for that combination
- **THEN** the recipe requires fresh item discovery and can produce a successful saved definition
- **AND** no negative cached outcome bypasses generation

#### Scenario: Upgrade preserves historical ownership and discoveries
- **WHEN** the compatibility migration upgrades a database containing successful recipes and completed rejected attempts
- **THEN** successful recipes retain their exact saved definitions and inventory and reservations remain unchanged
- **AND** replaying an old rejected confirmation preserves its historical result without any inventory mutation

### Requirement: Crafting has a localized selection and confirmation flow
The `/craft` command SHALL open an opener-controlled material menu with paged selection, quantity editing, review, and explicit confirmation in the group language. Review MUST explain that successful crafting consumes the selected materials to create one item and that technical errors preserve materials. Feedback SHALL distinguish pending, success, unavailable inventory, disabled service, and technical failure states. New gameplay rejection feedback MUST NOT be offered.

#### Scenario: Review before crafting
- **WHEN** a player reviews an eligible material combination
- **THEN** the review explains the selected ingredient consumption and one-item result before explicit confirmation
- **AND** merely browsing, editing, or cancelling the selection changes no ownership

#### Scenario: Pending generation and language change
- **WHEN** discovery is pending and the group changes language before the next render
- **THEN** the attempt remains durable and subsequent feedback uses the current group language
- **AND** successful output has saved item text in both supported languages

#### Scenario: Crafted output visible in inventory
- **WHEN** an attempt commits success
- **THEN** active inventory views reflect consumed ingredients and any granted output
- **AND** ordinary inventory navigation and inspection remain read-only

### Requirement: Material quantities use reset and stepped controls
Stackable material editing SHALL show `1`, `-10`, `-1`, `+1`, `+10`, and localized Max in one row in that order. Reset SHALL select one, step buttons SHALL adjust by their labeled amounts within one and current availability, and Max SHALL select the available quantity. Individual materials SHALL select one exact instance without quantity controls. Removing an ingredient SHALL be a separate action; other selections MUST remain intact.

#### Scenario: Edit a stackable material
- **WHEN** a player edits a stack with eight owned units and three sale-reserved units
- **THEN** the editor offers `1`, `-10`, `-1`, `+1`, `+10`, and localized Max in one row in that order
- **AND** reset selects one, Max selects five, decrements stop at one, and increments stop at five
- **AND** editing changes the existing message without consuming items

#### Scenario: Ten-unit steps
- **WHEN** a stack has thirty available units and the selected quantity is fifteen
- **THEN** `-10` selects five and `+10` from fifteen selects twenty-five
- **AND** `-1` and `+1` change the selected quantity by one within the available range
- **AND** step calculation cannot overflow the supported integer range

#### Scenario: Remove and revisit materials
- **WHEN** a player removes one selected material and returns to the material picker
- **THEN** only that material is removed from the selection and all other quantities and page selections remain intact

#### Scenario: Edit an individual material
- **WHEN** a player selects an individual material instance
- **THEN** the selection refers to that exact instance with quantity one and no quantity controls
- **AND** additional eligible instances can be selected separately

#### Scenario: Availability disappears
- **WHEN** a selected material's available quantity falls to zero before review
- **THEN** the editor reports unavailability and requires removing or revising the invalid selection before confirmation
- **AND** it never silently increases the selection beyond availability

### Requirement: AI work does not block unrelated gameplay
Pending AI generation SHALL allow unrelated group interactions to continue. Requests SHALL have bounded execution and response size, and shutdown SHALL cancel and join owned AI work while retaining accepted durable attempts for recovery. Disabled or unconfigured AI discovery SHALL report unavailability for unknown recipes without consuming materials; known saved recipes SHALL remain craftable.

#### Scenario: Slow discovery
- **WHEN** one recipe's AI request is slow
- **THEN** unrelated Telegram gameplay interactions can still be processed

#### Scenario: Restart during discovery
- **WHEN** shutdown interrupts discovery before an outcome is durably saved
- **THEN** no materials are lost and the accepted pending attempt can resume after restart

#### Scenario: AI disabled after discoveries
- **WHEN** AI discovery is disabled and players submit a known and an unknown recipe
- **THEN** the known recipe resolves using its saved outcome and the unknown recipe reports unavailability without consuming ingredients
