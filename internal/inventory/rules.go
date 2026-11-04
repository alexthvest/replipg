package inventory

import (
	"math"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
)

// equipmentRule validates an assignment without changing ownership or equipment.
// The caller handles request identity, revision checks, persistence and receipts.
func equipmentRule(snapshot contract.Snapshot, catalog Catalog, change contract.EquipmentChange) (bool, error) {
	if !change.Slot.Valid() {
		return false, contract.ErrInvalidInput
	}

	current := snapshot.Weapon
	if change.Slot == contract.SlotArmor {
		current = snapshot.Armor
	}
	if change.ItemID == "" {
		return current != "", nil
	}

	if err := change.ItemID.Validate(); err != nil {
		return false, inventoryItemError(err)
	}

	item, exists := catalog.Lookup(change.ItemID)
	if !exists {
		return false, contract.ErrInvalidInput
	}

	compatible := change.Slot == contract.SlotWeapon && item.Category == itemcontract.CategoryWeapon || change.Slot == contract.SlotArmor && item.Category == itemcontract.CategoryArmor
	if !compatible {
		return false, contract.ErrIncompatibleSlot
	}

	for _, owned := range snapshot.Items {
		if owned.ItemID == change.ItemID && owned.Quantity > 0 {
			return current != change.ItemID, nil
		}
	}
	return false, contract.ErrNotOwned
}

// EffectiveStats adds nonnegative equipment bonuses to base combat attributes.
// It rejects overflow and never changes persisted base attributes or health.
func EffectiveStats(baseAttack, baseDefense int64, modifiers contract.Modifiers) (contract.CombatStats, error) {
	if baseAttack < 0 || baseDefense < 0 || modifiers.Attack < 0 || modifiers.Defense < 0 {
		return contract.CombatStats{}, contract.ErrInvalidInput
	}

	if baseAttack > math.MaxInt64-modifiers.Attack || baseDefense > math.MaxInt64-modifiers.Defense {
		return contract.CombatStats{}, contract.ErrExhausted
	}

	return contract.CombatStats{Attack: baseAttack + modifiers.Attack, Defense: baseDefense + modifiers.Defense}, nil
}

// EffectiveStats exposes checked derivation through consumer-owned service ports.
// The pure package function supplies the same calculation without service state.
func (s *Service) EffectiveStats(baseAttack, baseDefense int64, modifiers contract.Modifiers) (contract.CombatStats, error) {
	return EffectiveStats(baseAttack, baseDefense, modifiers)
}
