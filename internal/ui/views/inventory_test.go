package views

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

func plain(text core.Text) string {
	var result strings.Builder
	for _, span := range text {
		result.WriteString(span.Text)
	}
	return result.String()
}

func TestInventoryPagesAndSavedInspection(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}

	owner := characters.Character{
		ID:    "owner",
		Group: -1,
		User:  2,
		Name:  "alexthvest",
	}
	var entries []inventory.Entry
	for i := range 13 {
		entries = append(entries, inventory.Entry{
			ID:        core.ID(fmt.Sprint(i)),
			Group:     -1,
			Character: owner.ID,
			Snapshot:  snapshot,
			Quantity:  1,
		})
	}

	for page, want := range []int{6, 6, 1} {
		view, visible, actual, total := Inventory(bundle.Locales(), "en", owner, entries, page)
		if len(visible) != want || actual != page || total != 3 {
			t.Fatal("wrong page size")
		}

		if view.Text[0].Text != "Inventory" ||
			view.Text[0].Style != core.Bold ||
			view.Text[1].Text != " • " ||
			view.Text[2].Text != "alexthvest" ||
			view.Text[2].Style != core.Code {
			t.Fatal("wrong title")
		}

		text := plain(view.Text)
		if strings.Contains(text, "Common") || strings.Contains(text, "wooden") || strings.Contains(text, "\n\n2.") {
			t.Fatal("list includes rarity, description or blank rows")
		}

		if !strings.Contains(text, "Inventory • alexthvest\n\n1. Stick (1)") {
			t.Fatal("wrong title spacing or item quantity")
		}

		for i := range visible {
			row := view.Text[3+i*2]
			quantity := view.Text[4+i*2]
			if !strings.Contains(row.Text, fmt.Sprintf("%d. Stick ", i+1)) || row.Style != "" {
				t.Fatal("item number and name must be plain text")
			}

			if quantity.Text != "(1)" || quantity.Style != core.Code {
				t.Fatal("parenthesized quantity must use code formatting")
			}
		}
	}

	coin, err := bundle.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	coinView, _, _, _ := Inventory(bundle.Locales(), "ru", owner, []inventory.Entry{{
		Snapshot: coin,
		Quantity: 100,
	}}, 0)
	if !strings.Contains(plain(coinView.Text), "\n\n1. Монета (100)") ||
		coinView.Text[3].Style != "" ||
		coinView.Text[4].Text != "(100)" ||
		coinView.Text[4].Style != core.Code {
		t.Fatal("wrong stack quantity formatting")
	}

	view, visible, _, _ := Inventory(bundle.Locales(), "en", owner, nil, 0)
	if len(visible) != 0 || !strings.Contains(plain(view.Text), "empty") {
		t.Fatal("missing empty state")
	}

	entry := entries[0]
	entry.Snapshot.English.Name = strings.Repeat("🎲", 128)
	entry.Snapshot.English.Description = strings.Repeat("🎲", 1000)
	inspection := Inspection(bundle.Locales(), "en", entry)
	if len(utf16.Encode([]rune(plain(inspection.Text)))) > 4096 {
		t.Fatal("inspection exceeds platform limit")
	}

	if !strings.Contains(plain(inspection.Text), entry.Snapshot.English.Description) {
		t.Fatal("inspection truncated saved text")
	}

	russian := Inspection(bundle.Locales(), "ru", entry)
	if !strings.Contains(plain(russian.Text), "Палка") || !strings.Contains(plain(russian.Text), "Обычный") {
		t.Fatal("saved Russian text or live rarity missing")
	}
}
