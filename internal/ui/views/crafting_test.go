package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/ui/menus"
)

func TestCraftingMaterialPagesAndReservations(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Components.Material = true
	snapshot.Mode = items.Stackable
	var entries []inventory.Entry
	for i := range 13 {
		entries = append(entries, inventory.Entry{
			ID:       core.ID(fmt.Sprint(i)),
			Snapshot: snapshot,
			Quantity: 20,
			Reserved: 5,
		})
	}
	nonmaterial := entries[0]
	nonmaterial.Snapshot.Components.Material = false
	entries = append(entries, nonmaterial)
	reserved := entries[0]
	reserved.Quantity = reserved.Reserved
	entries = append(entries, reserved)
	for page, want := range []int{6, 6, 1} {
		view, visible, actual, pages := CraftMaterials(bundle.Locales(), "en", entries, map[core.ID]int64{"0": 7}, page)
		if len(visible) != want || actual != page || pages != 3 || strings.Contains(plain(view.Text), "(20)") {
			t.Fatalf("page %d: %+v %d/%d", page, visible, actual, pages)
		}
		if page == 0 && !strings.Contains(plain(view.Text), "Selected × 7") {
			t.Fatal("accumulated selection absent")
		}
	}
}

func TestCraftingReviewWarningsAndBilingualStatus(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.English.Name = "<b>saved & literal</b>"
	snapshot.Russian.Name = "<b>сохранённый & текст</b>"
	var allocations []crafting.Allocation
	for i := range 20 {
		allocations = append(allocations, crafting.Allocation{
			Entry:    core.ID(fmt.Sprint(i)),
			Snapshot: snapshot,
			Quantity: int64(i + 1),
		})
	}
	for _, language := range []string{"en", "ru"} {
		var firstReview string
		for _, kind := range []string{"", crafting.Success, crafting.Uncraftable} {
			view, page, pages := CraftReview(bundle.Locales(), language, allocations, kind, 0)
			text := plain(view.Text)
			if page != 0 || pages != 4 || !strings.Contains(text, bundle.Locales().Text(language, "menu.craft.loss_warning")) {
				t.Fatal("review missing destructive warning or pagination")
			}
			if strings.Contains(text, "uncraftable") || strings.Contains(text, "уничтожит") || strings.Contains(text, "отклонить") {
				t.Fatal("new review offers a destructive rejection")
			}
			if firstReview == "" {
				firstReview = text
			} else if text != firstReview {
				t.Fatal("cached legacy rejection changed the new review")
			}
		}
		for _, state := range []string{"pending", "success", "uncraftable", "unavailable", "disabled", "technical-failure", "cancelled"} {
			view := CraftStatus(bundle.Locales(), language, state, &snapshot)
			if !strings.Contains(plain(view.Text), bundle.Locales().Text(language, "menu.craft."+state)) {
				t.Fatalf("missing status %s", state)
			}
			if state == "success" && !strings.Contains(plain(view.Text), snapshot.Text(language).Name) {
				t.Fatal("saved bilingual output lost")
			}
			for _, span := range view.Text {
				if span.Text == "\n\n"+snapshot.Text(language).Name && span.Style != core.Bold {
					t.Fatal("output text was parsed as markup")
				}
			}
		}
	}
	individual := inventory.Entry{
		Snapshot: snapshot,
		Quantity: 1,
	}
	individual.Snapshot.Mode = items.Individual
	if strings.Contains(plain(CraftEditor(bundle.Locales(), "en", individual, 1).Text), "Quantity") {
		t.Fatal("individual editor shows quantity")
	}
	individual.Reserved = 1
	if !strings.Contains(plain(CraftEditor(bundle.Locales(), "en", individual, 1).Text), bundle.Locales().Text("en", "menu.craft.unavailable")) {
		t.Fatal("zero availability not reported")
	}
}

func TestCraftCompositionAndAttemptBoundedPages(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Components.Material = true
	snapshot.English.Name = strings.Repeat("x", 1000)
	snapshot.Russian.Name = strings.Repeat("я", 1000)
	var selection menus.CraftSelection
	snapshots := map[core.ID]items.Snapshot{}
	var allocations []crafting.Allocation
	for i := range 13 {
		id := core.ID(fmt.Sprint(i))
		if err = selection.Set(id, int64(i+1), 20, false); err != nil {
			t.Fatal(err)
		}
		snapshots[id] = snapshot
		allocations = append(allocations, crafting.Allocation{
			Entry:    id,
			Snapshot: snapshot,
			Quantity: int64(i + 1),
		})
	}
	for _, language := range []string{"en", "ru"} {
		for page, want := range []int{6, 6, 1} {
			view, visible, actual, pages := CraftComposition(bundle.Locales(), language, &selection, snapshots, nil, page)
			text := plain(view.Text)
			if len(visible) != want || actual != page || pages != 3 || len([]rune(text)) > 3000 || !strings.Contains(text, bundle.Locales().Text(language, "menu.craft.available")+": 0") {
				t.Fatalf("composition page %d missing shortages or bounded entries: %s", page, text)
			}
			attempt := crafting.Attempt{
				State:       crafting.Success,
				Allocations: allocations,
				Outcome: crafting.Outcome{
					Kind: crafting.Success,
					Item: &snapshot,
				},
			}
			result, actual, pages := CraftAttempt(bundle.Locales(), language, attempt, page)
			if actual != page || pages != 3 || strings.Count(plain(result.Text), " × ") != want+1 || len([]rune(plain(result.Text))) > 3000 {
				t.Fatalf("attempt page not bounded: %s", plain(result.Text))
			}
		}
		var empty menus.CraftSelection
		view, visible, _, _ := CraftComposition(bundle.Locales(), language, &empty, nil, nil, 0)
		if len(visible) != 0 || !strings.Contains(plain(view.Text), bundle.Locales().Text(language, "menu.craft.composition-empty")) {
			t.Fatal("empty composition missing guidance")
		}
	}
}
