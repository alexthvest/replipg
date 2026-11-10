package app

import (
	"math"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/ui/menus"
)

func TestRestoreCraftExactSnapshotsReservationsAndShortages(t *testing.T) {
	original := items.Snapshot{
		CatalogID:  "material",
		Mode:       items.Stackable,
		English:    items.Text{Name: "Original"},
		Components: items.Components{Material: true},
	}
	changed := original
	changed.English.Name = "Changed version"
	entries := []inventory.Entry{
		{
			ID:       "foreign-version",
			Snapshot: changed,
			Quantity: 100,
		},
		{
			ID:       "replacement",
			Snapshot: original,
			Quantity: 10,
			Reserved: 7,
		},
		{
			ID:       "original",
			Snapshot: original,
			Quantity: 2,
		},
	}
	allocations := []domain.Allocation{
		{
			Entry:    "original",
			Snapshot: original,
			Quantity: 4,
		},
		{
			Entry:    "gone",
			Snapshot: original,
			Quantity: 4,
		},
	}
	var selection menus.CraftSelection
	snapshots := restoreCraft(&selection, allocations, entries)
	if selection.Quantity("original") != 2 || selection.Quantity("replacement") != 3 || selection.Quantity("foreign-version") != 0 {
		t.Fatalf("bad matching allocation: %+v", selection.Picks())
	}
	missing := core.ID("craft-shortage:1")
	if selection.Quantity(missing) != 3 || snapshots[missing] != original {
		t.Fatalf("shortage not preserved: %+v", selection.Picks())
	}
	var total int64
	for _, pick := range selection.Picks() {
		total += pick.Quantity
	}
	if total != 8 {
		t.Fatal("requested recipe quantity changed")
	}
}

func TestRestoreCraftIndividualReplacementAndLargeQuantities(t *testing.T) {
	snapshot := items.Snapshot{
		CatalogID:  "material",
		Mode:       items.Individual,
		Components: items.Components{Material: true},
	}
	var selection menus.CraftSelection
	restoreCraft(&selection, []domain.Allocation{
		{
			Entry:    "gone",
			Snapshot: snapshot,
			Quantity: 1,
		},
		{
			Entry:    "also-gone",
			Snapshot: snapshot,
			Quantity: 1,
		},
	}, []inventory.Entry{
		{
			ID:       "reserved",
			Snapshot: snapshot,
			Quantity: 1,
			Reserved: 1,
		},
		{
			ID:       "replacement-a",
			Snapshot: snapshot,
			Quantity: 1,
		},
		{
			ID:       "replacement-b",
			Snapshot: snapshot,
			Quantity: 1,
		},
	})
	if len(selection.Picks()) != 2 || selection.Quantity("replacement-a") != 1 || selection.Quantity("replacement-b") != 1 {
		t.Fatalf("instance allocation reused or skipped instances: %+v", selection.Picks())
	}
	snapshot.Mode = items.Stackable
	restoreCraft(&selection, []domain.Allocation{
		{
			Entry:    "a",
			Snapshot: snapshot,
			Quantity: math.MaxInt64,
		},
	}, []inventory.Entry{
		{
			ID:       "a",
			Snapshot: snapshot,
			Quantity: math.MaxInt64 - 1,
		},
		{
			ID:       "b",
			Snapshot: snapshot,
			Quantity: 1,
		},
	})
	if selection.Quantity("a") != math.MaxInt64-1 || selection.Quantity("b") != 1 {
		t.Fatal("large quantity restore overflowed")
	}
}

func TestAvailableCraftOutputExcludesReservationsAndOtherVersions(t *testing.T) {
	snapshot := items.Snapshot{
		CatalogID:  "generated:result",
		Mode:       items.Stackable,
		Components: items.Components{Material: true},
	}
	other := snapshot
	other.English.Name = "Different saved version"
	entries := []inventory.Entry{
		{
			ID:       "reserved",
			Snapshot: snapshot,
			Quantity: 1,
			Reserved: 1,
		},
		{
			ID:       "other-version",
			Snapshot: other,
			Quantity: 5,
		},
	}
	if _, ok := availableCraftOutput(entries, snapshot); ok {
		t.Fatal("reserved result or changed snapshot treated as available output")
	}
	entries = append(entries, inventory.Entry{
		ID:       "available",
		Snapshot: snapshot,
		Quantity: 2,
		Reserved: 1,
	})
	if entry, ok := availableCraftOutput(entries, snapshot); !ok || entry.ID != "available" {
		t.Fatal("available matching output not selected")
	}
}
