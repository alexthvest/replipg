package contract_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/item/contract"
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

	if contract.Category("").Valid() || contract.Category("currency").Valid() || contract.EffectKind("damage").Valid() {
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
		{"effect", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP, HealingHP: math.MaxInt64}, true},
		{"unknown effect", contract.EffectDefinition{ItemID: "potion", Kind: "heal_other", HealingHP: 1}, false},
		{"zero healing", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP}, false},
		{"negative healing", contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP, HealingHP: -1}, false},
		{"missing effect item", contract.EffectDefinition{Kind: contract.EffectHealSelfFixedHP, HealingHP: 1}, false},
	} {
		t.Run(tt.name, func(t *testing.T) { checkValidation(t, tt.value.Validate(), tt.valid) })
	}
}
