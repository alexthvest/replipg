package item

import (
	"errors"
	"math"
	"strings"
	"testing"

	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
)

const validCatalogJSON = `{"items":[{"id":"stick","category":"weapon","attack_bonus":1},{"id":"cloth_armor","category":"armor","defense_bonus":1},{"id":"healing_potion","category":"consumable","healing_hp":10,"effect":"heal_self_fixed_hp"},{"id":"coin","category":"inert"}]}`

func TestCatalogValidation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"duplicate ID", `"id":"coin"`, `"id":"stick"`},
		{"empty ID", `"id":"coin"`, `"id":""`},
		{"invalid ID", `"id":"coin"`, `"id":"bad id"`},
		{"long ID", `"id":"coin"`, `"id":"` + strings.Repeat("a", 65) + `"`},
		{"Unicode ID", `"id":"coin"`, `"id":"монета"`},
		{"category", `"category":"inert"`, `"category":"money"`},
		{"negative attack", `"attack_bonus":1`, `"attack_bonus":-1`},
		{"negative defense", `"defense_bonus":1`, `"defense_bonus":-1`},
		{"fraction", `"attack_bonus":1`, `"attack_bonus":1.5`},
		{"overflow", `"attack_bonus":1`, `"attack_bonus":9223372036854775808`},
		{"null number", `"attack_bonus":1`, `"attack_bonus":null`},
		{"zero healing", `"healing_hp":10`, `"healing_hp":0`},
		{"wrong effect", `"effect":"heal_self_fixed_hp"`, `"effect":"damage"`},
		{"missing effect", `,"effect":"heal_self_fixed_hp"`, ``},
		{"inert attack", `"category":"inert"`, `"category":"inert","attack_bonus":1`},
		{"inert effect", `"category":"inert"`, `"category":"inert","effect":"heal_self_fixed_hp"`},
		{"weapon defense", `"attack_bonus":1`, `"attack_bonus":1,"defense_bonus":1`},
		{"armor attack", `"defense_bonus":1`, `"defense_bonus":1,"attack_bonus":1`},
		{"consumable bonus", `"healing_hp":10`, `"healing_hp":10,"defense_bonus":1`},
		{"legacy display field", `"id":"coin"`, `"id":"coin","name":"Coin"`},
		{"unknown field", `"id":"coin"`, `"id":"coin","secret":"sensitive"`},
		{"wrong key case", `"id":"coin"`, `"ID":"coin"`},
		{"duplicate key", `"id":"coin"`, `"id":"coin","id":"coin"`},
		{"duplicate escaped key", `"id":"coin"`, `"id":"coin","\u0069d":"coin"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(validCatalogJSON, tc.old, tc.replacement, 1)

			if input == validCatalogJSON {
				t.Fatal("fixture did not change")
			}

			_, err := ParseCatalog([]byte(input))
			if !errors.Is(err, itemcontract.ErrInvalidCatalog) {
				t.Fatalf("error = %v", err)
			}

			if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "damage") {
				t.Fatalf("unsafe error: %s", err)
			}
		})
	}

	for _, input := range []string{"", "null", "[]", "{}", `{"items":[],"starter":[]}`, validCatalogJSON + "{}", validCatalogJSON + " trailing"} {
		if _, err := ParseCatalog([]byte(input)); !errors.Is(err, itemcontract.ErrInvalidCatalog) {
			t.Fatalf("malformed catalog error = %v", err)
		}
	}
}

func TestCatalogNumericAndUnicodeBounds(t *testing.T) {
	input := strings.Replace(validCatalogJSON, `"attack_bonus":1`, `"attack_bonus":9223372036854775807`, 1)

	catalog, err := ParseCatalog([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	stick, _ := catalog.Lookup("stick")

	if stick.AttackBonus != math.MaxInt64 {
		t.Fatalf("attack = %d", stick.AttackBonus)
	}

	for _, field := range []string{`"attack_bonus":1`, `"defense_bonus":1`} {
		zero := strings.Replace(validCatalogJSON, field, field[:strings.LastIndex(field, ":")+1]+"0", 1)
		if _, err := ParseCatalog([]byte(zero)); err != nil {
			t.Fatalf("zero bonus: %v", err)
		}
	}

	// Raw invalid UTF-8 must be rejected rather than silently replaced by JSON decoding.
	badUTF8 := strings.Replace(validCatalogJSON, "stick", string([]byte{0xff}), 1)
	if _, err := ParseCatalog([]byte(badUTF8)); !errors.Is(err, itemcontract.ErrInvalidCatalog) {
		t.Fatalf("invalid UTF-8: %v", err)
	}
}
