// Package contract defines inventory values shared with gameplay consumers.
// Consumers declare the service interfaces they need. Mutation results remain
// tentative until the caller's transaction commits.
package contract

import charactercontract "github.com/alexthvest/replipg/internal/character/contract"

// OperationID identifies one mutation, namespaced by its caller and target.
type OperationID string

// ItemID is a stable catalog identity, never a display name or instance ID.
type ItemID string

// Slot identifies one of the two equipment assignments.
type Slot string

const (
	SlotWeapon Slot = "weapon"
	SlotArmor  Slot = "armor"
)

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

// GrantLine describes positive whole copies selected by the caller.
type GrantLine struct {
	ItemID   ItemID
	Quantity int64
}

// GrantRequest grants resolved reward lines, without selecting loot.
type GrantRequest struct {
	OperationID OperationID
	CharacterID charactercontract.ID
	Items       []GrantLine
}

// EquipmentChange replaces an assignment; an empty ItemID means unequip.
// ExpectedRevision is part of operation replay equality.
type EquipmentChange struct {
	OperationID      OperationID
	CharacterID      charactercontract.ID
	Slot             Slot
	ItemID           ItemID
	ExpectedRevision int64
}

// ConsumeRequest spends one healing item after trusted gameplay authorization.
type ConsumeRequest struct {
	OperationID OperationID
	CharacterID charactercontract.ID
	ItemID      ItemID
}

// Modifiers contains equipment bonuses, separate from character base stats.
type Modifiers struct{ Attack, Defense int64 }

// OwnedItem includes equipped copies in its total positive quantity.
type OwnedItem struct {
	ItemID   ItemID
	Quantity int64
}

// Snapshot contains current ownership and assignments. Revision zero represents
// an existing character whose inventory has not been initialized.
type Snapshot struct {
	CharacterID   charactercontract.ID
	Revision      int64
	Items         []OwnedItem
	Weapon, Armor ItemID
	Modifiers     Modifiers
}

// MutationResult distinguishes new application from matching replay. A replay
// retains the original AppliedRevision while Current may include later changes.
type MutationResult struct {
	Applied         bool
	AppliedRevision int64
	Current         Snapshot
}

// OpenResult reports character creation and one-time starter entitlement.
type OpenResult struct {
	CharacterCreated, StarterGranted bool
	Current                          Snapshot
}

// ConsumeResult reports configured healing, which may exceed actual HP restored.
type ConsumeResult struct {
	Mutation  MutationResult
	Character charactercontract.Snapshot
	HealingHP int64
}

// EffectDefinition exposes an item's configured effect without battle policy.
type EffectDefinition struct {
	ItemID    ItemID
	Kind      EffectKind
	HealingHP int64
}

// CombatStats contains derived attack and defense, never persisted base values.
type CombatStats struct{ Attack, Defense int64 }

// Validate checks the operation identity's byte length without exposing it.
func (id OperationID) Validate() error {
	if len(id) == 0 || len(id) > 128 {
		return invalid("operation_id")
	}
	return nil
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

// Valid reports whether the equipment slot is supported.
func (s Slot) Valid() bool { return (s == SlotWeapon || s == SlotArmor) }

// Valid reports whether the category is supported; inert items have no effect.
func (c Category) Valid() bool {
	return (c == CategoryConsumable || c == CategoryWeapon || c == CategoryArmor || c == CategoryInert)
}

// Valid reports whether the fixed self-healing effect is supported.
func (k EffectKind) Valid() bool { return k == EffectHealSelfFixedHP }

func invalid(field string) error { return &DomainError{Kind: ErrorInvalidInput, Field: field} }

func validateTarget(operation OperationID, character charactercontract.ID) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	if err := character.Validate(); err != nil {
		return invalid("character_id")
	}
	return nil
}

// Validate checks positive whole quantities; catalog existence is service-owned.
func (l GrantLine) Validate() error {
	if err := l.ItemID.Validate(); err != nil {
		return err
	}
	if l.Quantity <= 0 {
		return invalid("quantity")
	}
	return nil
}

// Validate checks request shape. The service aggregates duplicate reward lines
// with checked arithmetic and validates all definitions before applying changes.
func (r GrantRequest) Validate() error {
	if err := validateTarget(r.OperationID, r.CharacterID); err != nil {
		return err
	}
	if len(r.Items) == 0 {
		return invalid("items")
	}
	for _, line := range r.Items {
		if err := line.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Validate requires a supported slot and a positive initialized revision.
func (r EquipmentChange) Validate() error {
	if err := validateTarget(r.OperationID, r.CharacterID); err != nil {
		return err
	}
	if !r.Slot.Valid() {
		return invalid("slot")
	}
	if r.ExpectedRevision < 1 {
		return invalid("expected_revision")
	}
	if r.ItemID != "" {
		return r.ItemID.Validate()
	}
	return nil
}

// Validate checks identity shape, leaving ownership and eligibility to service.
func (r ConsumeRequest) Validate() error {
	if err := validateTarget(r.OperationID, r.CharacterID); err != nil {
		return err
	}
	return r.ItemID.Validate()
}

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
