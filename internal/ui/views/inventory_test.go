package views

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
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

		if !strings.Contains(text, "Inventory • alexthvest\n\n1. Stick\n") {
			t.Fatal("wrong title spacing or item name")
		}

		if strings.Contains(text, "(1)") {
			t.Fatal("individual items must not show a quantity")
		}

		for i := range visible {
			row := view.Text[3+i]
			if !strings.HasSuffix(row.Text, fmt.Sprintf("%d. Stick", i+1)) || row.Style != "" {
				t.Fatal("item number and name must be plain text")
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
		coinView.Text[5].Text != "(100)" ||
		coinView.Text[5].Style != core.Code {
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

func TestInspectionShowsReservationsWithoutChangingListOwnership(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}
	entry := inventory.Entry{
		ID:        "reserved-stack",
		Group:     -1,
		Character: "owner",
		Snapshot:  snapshot,
		Quantity:  10,
		Reserved:  3,
	}
	for _, language := range []string{"en", "ru"} {
		text := plain(Inspection(bundle.Locales(), language, entry).Text)
		for _, field := range []struct {
			key   string
			value string
		}{
			{
				key:   "menu.item.quantity",
				value: "10",
			},
			{
				key:   "menu.item.reserved",
				value: "3",
			},
			{
				key:   "menu.item.available",
				value: "7",
			},
		} {
			if !strings.Contains(text, bundle.Locales().Text(language, field.key)+": "+field.value) {
				t.Fatalf("missing %s in %s inspection: %s", field.key, language, text)
			}
		}
	}
	owner := characters.Character{
		ID:   "owner",
		Name: "Owner",
	}
	list, _, _, _ := Inventory(bundle.Locales(), "en", owner, []inventory.Entry{entry}, 0)
	if !strings.Contains(plain(list.Text), "Coin (10)") || strings.Contains(plain(list.Text), "(7)") {
		t.Fatalf("list no longer shows owned quantity: %s", plain(list.Text))
	}
}

func TestReservedInstanceInspectionShowsOwnedAndAvailable(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}
	entry := inventory.Entry{
		ID:        "reserved-instance",
		Group:     -1,
		Character: "owner",
		Snapshot:  snapshot,
		Quantity:  1,
		Reserved:  1,
	}
	text := plain(Inspection(bundle.Locales(), "en", entry).Text)
	for _, field := range []struct {
		key   string
		value string
	}{
		{
			key:   "menu.item.quantity",
			value: "1",
		},
		{
			key:   "menu.item.reserved",
			value: "1",
		},
		{
			key:   "menu.item.available",
			value: "0",
		},
		{
			key:   "menu.item.instance",
			value: string(entry.ID),
		},
	} {
		if !strings.Contains(text, bundle.Locales().Text("en", field.key)+": "+field.value) {
			t.Fatalf("missing owned/reserved/available instance detail: %s", text)
		}
	}
}

func TestInspectionUsesSavedComponentsAndCurrentLabels(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}

	saved, err := bundle.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	saved.Components = items.Components{
		Weapon:     true,
		Material:   true,
		Consumable: true,
	}

	// This catalog has no item definitions: inspection cannot consult live items.
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../../assets")); err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join(root, "items", "*.cue"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range paths {
		if filepath.Base(path) != "schema.cue" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := os.RemoveAll(filepath.Join(root, "shop")); err != nil {
		t.Fatal(err)
	}

	localePath := filepath.Join(root, "locales", "en.cue")
	data, err := os.ReadFile(localePath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(localePath, []byte(strings.ReplaceAll(string(data), "\"Material\"", "\"Current material\"")), 0600); err != nil {
		t.Fatal(err)
	}

	current, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	entry := inventory.Entry{
		ID:       "saved",
		Snapshot: saved,
		Quantity: 5,
	}

	for _, language := range []string{"en", "ru"} {
		labels := []string{}
		for _, key := range []string{"consumable", "material", "weapon"} {
			labels = append(labels, current.Locales().Text(language, "item.component."+key+".name"))
		}

		view := Inspection(current.Locales(), language, entry)
		text := plain(view.Text)
		if !strings.Contains(text, current.Locales().Text(language, "menu.item.components")+": "+strings.Join(labels, ", ")) ||
			!strings.Contains(text, saved.Text(language).Name) || len(view.Rows) != 0 {
			t.Fatalf("saved classification inspection: %+v", view)
		}

		before, _, _, _ := Inventory(current.Locales(), language, characters.Character{}, []inventory.Entry{entry}, 0)
		entry.Snapshot.Components = items.Components{}
		empty := Inspection(current.Locales(), language, entry)
		if !strings.Contains(plain(empty.Text), current.Locales().Text(language, "menu.item.components")+": "+current.Locales().Text(language, "item.component.none")) {
			t.Fatal("empty/legacy classification missing")
		}

		after, _, _, _ := Inventory(current.Locales(), language, characters.Character{}, []inventory.Entry{entry}, 0)
		if !reflect.DeepEqual(before, after) || entry.Quantity != 5 {
			t.Fatal("classification changed inventory rows or ownership")
		}

		entry.Snapshot = saved
	}
}
