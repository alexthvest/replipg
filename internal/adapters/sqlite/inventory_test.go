package sqlite

import (
	"context"
	"fmt"
	"testing"

	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

func TestInventoryPreservesAcquisitionOrder(t *testing.T) {
	db, service, coin, stick := grantSetup(t)
	ctx := context.Background()
	first, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	want := []core.ID{first.Entries[0].ID}
	err = db.WithinGrant(ctx, func(tx grants.Transaction) error {
		snapshotID, err := tx.Inventory.ReuseSnapshot(ctx, stick)
		if err != nil {
			return err
		}

		for i := 8; i > 0; i-- {
			id := core.ID(fmt.Sprintf("instance-%d", i))
			if err = tx.Inventory.PutEntry(ctx, inventory.Entry{
				ID:         id,
				Group:      -1,
				Character:  first.Character.ID,
				SnapshotID: snapshotID,
				Snapshot:   stick,
				Quantity:   1,
			}); err != nil {
				return err
			}
			want = append(want, id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Give(ctx, request(2, 5), snapshotCatalog{snapshot: coin}); err != nil {
		t.Fatal(err)
	}

	var sequence int
	var name, path string
	if err = db.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})

	entries, err := reopened.Entries(ctx, -1, first.Character.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, entry := range entries {
		if entry.ID != want[i] {
			t.Fatalf("entry %d: got %s, want %s", i, entry.ID, want[i])
		}
	}
	if entries[0].Quantity != 10 {
		t.Fatal("stack quantity was not updated")
	}
}
