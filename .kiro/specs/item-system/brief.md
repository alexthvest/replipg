# Brief: item-system

Extract immutable item identities, definitions, categories, effects and strict catalog loading from inventory into an independent pure Go boundary. Preserve Coin, Stick, cloth_armor and healing_potion IDs/mechanics and all localization text. Inventory owns starter configuration, ownership, equipment and transactions. Battle consumes both boundaries; localization consumes stable item IDs. No new gameplay, persistence, tuning or aliases are introduced.

The observable result is owner-only application-layer `/dev_item <item-id>` in ephemeral group interactions. Its runtime prerequisite is separate from the domain dependency graph. The user approved this requirements/design/tasks/implementation change by requesting “Implement the plan” on 2026-11-04.
