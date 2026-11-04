// Package contract defines immutable item identities and mechanics.
package contract

// ItemID is a stable catalog identity, never a display name or instance ID.
type ItemID string

// Category defines the closed set of supported item mechanics.
type Category string

const (
	CategoryConsumable Category = "consumable"
	CategoryWeapon     Category = "weapon"
	CategoryArmor      Category = "armor"
	CategoryInert      Category = "inert"
)

// EffectKind identifies a closed, trusted consumable effect.
type EffectKind string

const EffectHealSelfFixedHP EffectKind = "heal_self_fixed_hp"

// EffectDefinition exposes an item's configured effect without battle policy.
type EffectDefinition struct {
	ItemID    ItemID
	Kind      EffectKind
	HealingHP int64
}

// Validate requires 1–64 ASCII letters, digits, underscores or hyphens.
func (id ItemID) Validate() error {
	if len(id) == 0 || len(id) > 64 {
		return invalid("item_id")
	}

	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return invalid("item_id")
		}
	}
	return nil
}

// Valid reports whether the category is supported; inert items have no effect.
func (c Category) Valid() bool {
	return (c == CategoryConsumable || c == CategoryWeapon || c == CategoryArmor || c == CategoryInert)
}

// Valid reports whether the fixed self-healing effect is supported.
func (k EffectKind) Valid() bool { return k == EffectHealSelfFixedHP }

// Validate accepts only positive fixed self-healing effects.
func (e EffectDefinition) Validate() error {
	if err := e.ItemID.Validate(); err != nil {
		return err
	}

	if !e.Kind.Valid() {
		return invalid("effect_kind")
	}

	if e.HealingHP <= 0 {
		return invalid("healing_hp")
	}

	return nil
}

// Definition describes fungible copies and keeps mechanics separate from
// character base attributes. Consumables always use fixed self-healing.
type Definition struct {
	ID           ItemID     `json:"id"`
	Category     Category   `json:"category"`
	AttackBonus  int64      `json:"attack_bonus"`
	DefenseBonus int64      `json:"defense_bonus"`
	Effect       EffectKind `json:"effect,omitempty"`
	HealingHP    int64      `json:"healing_hp"`
}

func invalid(field string) error { return &DomainError{Kind: ErrorInvalidInput, Field: field} }

// Validate checks item identity and the closed category/effect mechanics.
func (d Definition) Validate() error {
	if err := d.ID.Validate(); err != nil {
		return err
	}

	if !validMechanics(d) {
		return invalid("mechanics")
	}

	return nil
}

// EffectDefinition returns the configured consumable mechanics.
func (d Definition) EffectDefinition() (EffectDefinition, error) {
	if err := d.Validate(); err != nil {
		return EffectDefinition{}, err
	}

	if d.Category != CategoryConsumable {
		return EffectDefinition{}, &DomainError{Kind: ErrorNotUsable}
	}

	return EffectDefinition{ItemID: d.ID, Kind: d.Effect, HealingHP: d.HealingHP}, nil
}

func validMechanics(item Definition) bool {
	switch item.Category {
	case CategoryConsumable:
		return item.HealingHP > 0 && item.AttackBonus == 0 && item.DefenseBonus == 0 && item.Effect == EffectHealSelfFixedHP
	case CategoryWeapon:
		return item.AttackBonus >= 0 && item.DefenseBonus == 0 && item.HealingHP == 0 && item.Effect == ""
	case CategoryArmor:
		return item.DefenseBonus >= 0 && item.AttackBonus == 0 && item.HealingHP == 0 && item.Effect == ""
	case CategoryInert:
		return item.AttackBonus == 0 && item.DefenseBonus == 0 && item.HealingHP == 0 && item.Effect == ""
	default:
		return false
	}
}
