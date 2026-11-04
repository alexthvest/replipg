package inventory

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
)

const validCatalogJSON = `{"items":[
{"id":"stick","name":"Stick","description":"A stick.","category":"weapon","attack_bonus":1},
{"id":"cloth_armor","name":"Cloth armor","description":"Cloth.","category":"armor","defense_bonus":1},
{"id":"healing_potion","name":"Healing potion","description":"Heals.","category":"consumable","healing_hp":10,"effect":"heal_self_fixed_hp"},
{"id":"coin","name":"Coin","description":"An ordinary item.","category":"inert"}],
"starter":[{"item_id":"stick","quantity":1},{"item_id":"cloth_armor","quantity":1},{"item_id":"healing_potion","quantity":3}]}`

func TestCatalogEmbeddedAndCopies(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	items := catalog.Items()
	if len(items) < 4 {
		t.Fatalf("items = %v", items)
	}
	coin, ok := catalog.Lookup("coin")
	if !ok || strings.TrimSpace(coin.Name) == "" || coin.Category != contract.CategoryInert || coin.AttackBonus != 0 || coin.DefenseBonus != 0 || coin.HealingHP != 0 {
		t.Fatalf("coin = %+v, %v", coin, ok)
	}
	if _, ok := catalog.Lookup("absent"); ok {
		t.Fatal("unknown item found")
	}
	beforeItems, beforeStarter := catalog.Items(), catalog.Starter()
	items[0].Name = "changed"
	coin.Name = "changed"
	starter := catalog.Starter()
	starter[0].Quantity = 99
	if !reflect.DeepEqual(beforeItems, catalog.Items()) || !reflect.DeepEqual(beforeStarter, catalog.Starter()) {
		t.Fatal("returned slice mutated catalog")
	}
	again, err := LoadCatalog()
	if err != nil || !reflect.DeepEqual(again.Items(), beforeItems) {
		t.Fatalf("repeat load = %v, %v", again.Items(), err)
	}
}

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
		{"unknown field", `"id":"coin"`, `"id":"coin","secret":"sensitive"`},
		{"wrong key case", `"id":"coin"`, `"ID":"coin"`},
		{"duplicate key", `"id":"coin"`, `"id":"coin","id":"coin"`},
		{"duplicate escaped key", `"id":"coin"`, `"id":"coin","\u0069d":"coin"`},
		{"duplicate root", `"starter":`, `"items":[],"starter":`},
		{"empty name", `"name":"Coin"`, `"name":""`},
		{"blank name", `"name":"Coin"`, `"name":"  "`},
		{"long name", `"name":"Coin"`, `"name":"` + strings.Repeat("я", 81) + `"`},
		{"long description", `"description":"Cloth."`, `"description":"` + strings.Repeat("я", 401) + `"`},
		{"empty description", `"description":"Cloth."`, `"description":""`},
		{"control text", `"name":"Coin"`, `"name":"Coin\n"`},
		{"format text", `"name":"Coin"`, `"name":"Coin\u202e"`},
		{"undefined starter", `"item_id":"stick"`, `"item_id":"missing"`},
		{"duplicate starter", `"item_id":"cloth_armor"`, `"item_id":"stick"`},
		{"zero quantity", `"quantity":3`, `"quantity":0`},
		{"negative quantity", `"quantity":3`, `"quantity":-1`},
		{"starter unknown field", `"quantity":3`, `"quantity":3,"extra":1`},
		{"missing weapon starter", `"item_id":"stick"`, `"item_id":"coin"`},
		{"missing armor starter", `"item_id":"cloth_armor"`, `"item_id":"coin"`},
		{"missing healing starter", `"item_id":"healing_potion"`, `"item_id":"coin"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.Replace(validCatalogJSON, tc.old, tc.replacement, 1)
			if input == validCatalogJSON {
				t.Fatal("fixture did not change")
			}
			_, err := ParseCatalog([]byte(input))
			if !errors.Is(err, contract.ErrInvalidCatalog) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "damage") {
				t.Fatalf("unsafe error: %s", err)
			}
		})
	}
	for _, input := range []string{"", "null", "[]", "{}", `{"items":[],"starter":[]}`, validCatalogJSON + "{}", validCatalogJSON + " trailing"} {
		if _, err := ParseCatalog([]byte(input)); !errors.Is(err, contract.ErrInvalidCatalog) {
			t.Fatalf("malformed catalog error = %v", err)
		}
	}
}

func TestCatalogNumericAndUnicodeBounds(t *testing.T) {
	input := strings.Replace(validCatalogJSON, `"name":"Coin"`, `"name":"`+strings.Repeat("я", 80)+`"`, 1)
	input = strings.Replace(input, `"description":"Cloth."`, `"description":"`+strings.Repeat("🪙", 400)+`"`, 1)
	input = strings.Replace(input, `"attack_bonus":1`, `"attack_bonus":9223372036854775807`, 1)
	input = strings.Replace(input, `"quantity":3`, `"quantity":9223372036854775807`, 1)
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
	badUTF8 := strings.Replace(validCatalogJSON, "Stick", string([]byte{0xff}), 1)
	if _, err := ParseCatalog([]byte(badUTF8)); !errors.Is(err, contract.ErrInvalidCatalog) {
		t.Fatalf("invalid UTF-8: %v", err)
	}
}
