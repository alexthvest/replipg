package views

import (
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

func TestInventoryClampsPageAfterEntriesAreRemoved(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}
	owner := characters.Character{
		ID:   "owner",
		Name: "Player",
	}
	entries := make([]inventory.Entry, 13)
	for i := range entries {
		entries[i].Snapshot = snapshot
		entries[i].Quantity = 1
	}
	_, _, page, pages := Inventory(bundle.Locales(), "en", owner, entries, 2)
	if page != 2 || pages != 3 {
		t.Fatal("initial page was not preserved")
	}
	view, visible, page, pages := Inventory(bundle.Locales(), "en", owner, entries[:1], page)
	if page != 0 || pages != 1 || len(visible) != 1 || plain(view.Text) == "" {
		t.Fatal("removed final pages did not clamp to remaining inventory")
	}
	_, visible, page, pages = Inventory(bundle.Locales(), "en", owner, nil, 2)
	if page != 0 || pages != 1 || len(visible) != 0 {
		t.Fatal("empty inventory retained an invalid page")
	}
}
