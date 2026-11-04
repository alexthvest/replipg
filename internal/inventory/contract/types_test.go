package contract_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
)

func TestIdentityValidation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value interface{ Validate() error }
		valid bool
	}{
		{"item", contract.ItemID("AZaz_09-"), true},
		{"max item", contract.ItemID(strings.Repeat("x", 64)), true},
		{"empty item", contract.ItemID(""), false},
		{"long item", contract.ItemID(strings.Repeat("x", 65)), false},
		{"unicode item", contract.ItemID("é"), false},
		{"space item", contract.ItemID("a b"), false},
		{"punctuation item", contract.ItemID("a/b"), false},
		{"control item", contract.ItemID("a\x00"), false},
		{"operation", contract.OperationID("battle:reward:character"), true},
		{"max operation", contract.OperationID(strings.Repeat("x", 128)), true},
		{"empty operation", contract.OperationID(""), false},
		{"long operation", contract.OperationID(strings.Repeat("x", 129)), false},
		{"operation byte length", contract.OperationID(strings.Repeat("é", 65)), false},
	} {
		t.Run(tt.name, func(t *testing.T) { checkValidation(t, tt.value.Validate(), tt.valid) })
	}
}

func checkValidation(t *testing.T, err error, valid bool) {
	t.Helper()
	if valid {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var domain *contract.DomainError
	if !errors.Is(err, contract.ErrInvalidInput) || !errors.As(err, &domain) {
		t.Fatalf("expected inspectable invalid input, got %v", err)
	}
}

func TestClosedValues(t *testing.T) {
	for _, c := range []contract.Category{contract.CategoryConsumable, contract.CategoryWeapon, contract.CategoryArmor, contract.CategoryInert} {
		if !c.Valid() {
			t.Errorf("category %q rejected", c)
		}
	}
	for _, s := range []contract.Slot{contract.SlotWeapon, contract.SlotArmor} {
		if !s.Valid() {
			t.Errorf("slot %q rejected", s)
		}
	}
	if contract.Category("").Valid() || contract.Category("currency").Valid() || contract.Slot("inert").Valid() || contract.EffectKind("damage").Valid() {
		t.Fatal("unsupported value accepted")
	}
	if !contract.EffectHealSelfFixedHP.Valid() {
		t.Fatal("self healing rejected")
	}
}

func TestRequestValidation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value interface{ Validate() error }
		valid bool
	}{
		{"grant", contract.GrantRequest{OperationID: "op", CharacterID: "id", Items: []contract.GrantLine{{ItemID: "coin", Quantity: math.MaxInt64}}}, true},
		{"duplicates allowed for service aggregation", contract.GrantRequest{OperationID: "op", CharacterID: "id", Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "coin", Quantity: 2}}}, true},
		{"missing operation", contract.GrantRequest{CharacterID: "id", Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}}}, false},
		{"missing character", contract.GrantRequest{OperationID: "op", Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}}}, false},
		{"empty grant", contract.GrantRequest{OperationID: "op", CharacterID: "id"}, false},
		{"zero quantity", contract.GrantLine{ItemID: "coin", Quantity: 0}, false},
		{"negative quantity", contract.GrantLine{ItemID: "coin", Quantity: -1}, false},
		{"missing grant item", contract.GrantLine{Quantity: 1}, false},
		{"invalid trailing line", contract.GrantRequest{OperationID: "op", CharacterID: "id", Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "bad!", Quantity: 1}}}, false},
		{"equip", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: contract.SlotWeapon, ItemID: "stick", ExpectedRevision: math.MaxInt64}, true},
		{"unequip", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: contract.SlotArmor, ExpectedRevision: 1}, true},
		{"invalid slot", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: "inert", ExpectedRevision: 1}, false},
		{"invalid equipment item", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: contract.SlotWeapon, ItemID: "bad!", ExpectedRevision: 1}, false},
		{"zero revision", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: contract.SlotWeapon}, false},
		{"negative revision", contract.EquipmentChange{OperationID: "op", CharacterID: "id", Slot: contract.SlotWeapon, ExpectedRevision: -1}, false},
		{"missing equip target", contract.EquipmentChange{OperationID: "op", Slot: contract.SlotWeapon, ExpectedRevision: 1}, false},
		{"consume", contract.ConsumeRequest{OperationID: "op", CharacterID: "id", ItemID: "potion"}, true},
		{"missing consume item", contract.ConsumeRequest{OperationID: "op", CharacterID: "id"}, false},
		{"missing consume operation", contract.ConsumeRequest{CharacterID: "id", ItemID: "potion"}, false},
		{"effect", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP, HealingHP: math.MaxInt64}, true},
		{"unknown effect", contract.EffectDefinition{ItemID: "potion", Kind: "heal_other", HealingHP: 1}, false},
		{"zero healing", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP}, false},
		{"negative healing", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP, HealingHP: -1}, false},
		{"missing effect item", contract.EffectDefinition{Kind: contract.EffectHealSelfFixedHP, HealingHP: 1}, false},
	} {
		t.Run(tt.name, func(t *testing.T) { checkValidation(t, tt.value.Validate(), tt.valid) })
	}
}

func TestResultValues(t *testing.T) {
	current := contract.Snapshot{CharacterID: charactercontract.ID("internal"), Revision: 4, Items: []contract.OwnedItem{{ItemID: "coin", Quantity: 5}}, Weapon: "stick", Modifiers: contract.Modifiers{Attack: 2}}
	replay := contract.MutationResult{Applied: false, AppliedRevision: 2, Current: current}
	result := contract.ConsumeResult{Mutation: replay, Character: charactercontract.Snapshot{ID: current.CharacterID, Health: 100}, HealingHP: 25}
	opened := contract.OpenResult{CharacterCreated: true, StarterGranted: true, Current: current}
	if result.Mutation.Applied || result.Mutation.AppliedRevision != 2 || result.Mutation.Current.Revision != 4 || result.HealingHP != 25 || !opened.StarterGranted {
		t.Fatal("result lost replay or initialization values")
	}
	if (contract.Snapshot{CharacterID: "internal"}).Revision != 0 {
		t.Fatal("uninitialized snapshot must allow revision zero")
	}
}
