package inventory

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
)

func TestEquipmentRuleSlotCategories(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	for _, slot := range []contract.Slot{contract.SlotWeapon, contract.SlotArmor} {
		for _, item := range catalog.Items() {
			t.Run(string(slot)+"/"+string(item.Category), func(t *testing.T) {
				snapshot := contract.Snapshot{Items: []contract.OwnedItem{{ItemID: item.ID, Quantity: 1}}}
				before := snapshot
				before.Items = append([]contract.OwnedItem(nil), snapshot.Items...)
				changed, err := equipmentRule(snapshot, catalog, contract.EquipmentChange{Slot: slot, ItemID: item.ID})
				compatible := slot == contract.SlotWeapon && item.Category == contract.CategoryWeapon || slot == contract.SlotArmor && item.Category == contract.CategoryArmor
				if compatible {
					if err != nil || !changed {
						t.Fatalf("changed=%v err=%v", changed, err)
					}
				} else if !errors.Is(err, contract.ErrIncompatibleSlot) || changed {
					t.Fatalf("changed=%v err=%v", changed, err)
				}

				if !reflect.DeepEqual(snapshot, before) {
					t.Fatal("rule mutated ownership or assignments")
				}
			})
		}
	}
}

func TestEquipmentRuleTransitions(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name                string
		slot                contract.Slot
		item, weapon, armor contract.ItemID
		quantity            int64
		changed             bool
		want                error
	}{
		{name: "equip one owned copy", slot: contract.SlotWeapon, item: "stick", quantity: 1, changed: true},
		{name: "replace weapon", slot: contract.SlotWeapon, item: "stick", weapon: "old_weapon", quantity: 1, changed: true},
		{name: "replace armor", slot: contract.SlotArmor, item: "cloth_armor", armor: "old_armor", quantity: 1, changed: true},
		{name: "already equipped weapon", slot: contract.SlotWeapon, item: "stick", weapon: "stick", quantity: 1},
		{name: "already equipped armor", slot: contract.SlotArmor, item: "cloth_armor", armor: "cloth_armor", quantity: 1},
		{name: "unequip weapon", slot: contract.SlotWeapon, weapon: "stick", changed: true},
		{name: "unequip armor", slot: contract.SlotArmor, armor: "cloth_armor", changed: true},
		{name: "empty weapon", slot: contract.SlotWeapon},
		{name: "empty armor", slot: contract.SlotArmor},
		{name: "unowned", slot: contract.SlotWeapon, item: "stick", want: contract.ErrNotOwned},
		{name: "negative ownership", slot: contract.SlotWeapon, item: "stick", quantity: -1, want: contract.ErrNotOwned},
		{name: "invalid slot", slot: "head", item: "stick", quantity: 1, want: contract.ErrInvalidInput},
		{name: "invalid empty slot", slot: "head", want: contract.ErrInvalidInput},
		{name: "unknown definition", slot: contract.SlotWeapon, item: "absent", quantity: 1, want: contract.ErrInvalidInput},
		{name: "invalid identity", slot: contract.SlotWeapon, item: "bad id", quantity: 1, want: contract.ErrInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := contract.Snapshot{Weapon: tt.weapon, Armor: tt.armor, Items: []contract.OwnedItem{{ItemID: tt.item, Quantity: tt.quantity}}, Modifiers: contract.Modifiers{Attack: 8, Defense: 9}}
			before := snapshot
			before.Items = append([]contract.OwnedItem(nil), snapshot.Items...)
			changed, err := equipmentRule(snapshot, catalog, contract.EquipmentChange{Slot: tt.slot, ItemID: tt.item})
			if changed != tt.changed || !errors.Is(err, tt.want) {
				t.Fatalf("changed=%v err=%v; want %v %v", changed, err, tt.changed, tt.want)
			}

			if !reflect.DeepEqual(snapshot, before) {
				t.Fatal("rule mutated snapshot")
			}
		})
	}
}

func TestEffectiveStats(t *testing.T) {
	for _, tt := range []struct {
		name            string
		attack, defense int64
		modifiers       contract.Modifiers
		want            contract.CombatStats
		err             error
	}{
		{name: "zero bonuses", attack: 10, defense: 5, want: contract.CombatStats{Attack: 10, Defense: 5}},
		{name: "add bonuses", attack: 10, defense: 5, modifiers: contract.Modifiers{Attack: 3, Defense: 2}, want: contract.CombatStats{Attack: 13, Defense: 7}},
		{name: "all zero"},
		{name: "exact limit", attack: math.MaxInt64 - 1, defense: math.MaxInt64 - 2, modifiers: contract.Modifiers{Attack: 1, Defense: 2}, want: contract.CombatStats{Attack: math.MaxInt64, Defense: math.MaxInt64}},
		{name: "attack overflow", attack: math.MaxInt64, modifiers: contract.Modifiers{Attack: 1}, err: contract.ErrExhausted},
		{name: "defense overflow", defense: math.MaxInt64, modifiers: contract.Modifiers{Defense: 1}, err: contract.ErrExhausted},
		{name: "negative attack", attack: -1, err: contract.ErrInvalidInput},
		{name: "negative defense", defense: -1, err: contract.ErrInvalidInput},
		{name: "negative attack bonus", modifiers: contract.Modifiers{Attack: -1}, err: contract.ErrInvalidInput},
		{name: "negative defense bonus", modifiers: contract.Modifiers{Defense: -1}, err: contract.ErrInvalidInput},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EffectiveStats(tt.attack, tt.defense, tt.modifiers)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Fatalf("got %+v %v; want %+v %v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestEffectiveStatsServicePort(t *testing.T) {
	var port interface {
		EffectiveStats(int64, int64, contract.Modifiers) (contract.CombatStats, error)
	} = &Service{}

	stats, err := port.EffectiveStats(10, 5, contract.Modifiers{Attack: 2, Defense: 3})
	if err != nil || stats.Attack != 12 || stats.Defense != 8 {
		t.Fatalf("consumer service port %+v %v", stats, err)
	}
}
