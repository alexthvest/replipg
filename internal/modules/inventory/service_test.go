package inventory

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type fakeWriter struct {
	stack  Entry
	found  bool
	writes []Entry
}

func (f *fakeWriter) ReuseSnapshot(context.Context, items.Snapshot) (core.ID, error) {
	return "snapshot", nil
}

func (f *fakeWriter) Stack(context.Context, core.GroupID, core.ID, core.ID) (Entry, bool, error) {
	return f.stack, f.found, nil
}

func (f *fakeWriter) PutEntry(_ context.Context, entry Entry) error {
	f.writes = append(f.writes, entry)
	return nil
}

func TestOwnershipRulesWithPorts(t *testing.T) {
	snapshot := items.Snapshot{
		Version:   1,
		CatalogID: "coin",
		Mode:      items.Stackable,
		Rarity:    "common",
		English: items.Text{
			Name:        "Coin",
			Description: "Coin",
		},
		Russian: items.Text{
			Name:        "Монета",
			Description: "Монета",
		},
	}
	writer := &fakeWriter{
		found: true,
		stack: Entry{Quantity: math.MaxInt64},
	}
	if _, err := Grant(context.Background(), writer, core.NewID, -1, "character", snapshot, 1); !errors.Is(err, ErrOverflow) ||
		len(writer.writes) != 0 {
		t.Fatal("overflow mutated port")
	}

	writer = &fakeWriter{}
	snapshot.Mode = items.Individual
	entries, err := Grant(context.Background(), writer, core.NewID, -1, "character", snapshot, 3)
	if err != nil || len(entries) != 3 {
		t.Fatalf("instances: %+v %v", entries, err)
	}

	ids := map[core.ID]bool{}
	for _, entry := range entries {
		if entry.Quantity != 1 || ids[entry.ID] {
			t.Fatal("instance identity/quantity")
		}

		ids[entry.ID] = true
	}
}
