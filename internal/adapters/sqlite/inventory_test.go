package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
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

const preComponentSnapshot = `{"version":1,"catalog_id":"coin","mode":"stackable","rarity":"common","en":{"name":"Coin","description":"English"},"ru":{"name":"Монета","description":"Русский"}}`

func reopenComponentDatabase(t *testing.T, db *Database) (*Database, *grants.Service) {
	t.Helper()
	var path string
	if err := db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = reopened.Close()
	})
	permissions, err := groups.New(reopened, 1)
	if err != nil {
		t.Fatal(err)
	}

	service, err := grants.New(reopened, permissions, core.NewID)
	if err != nil {
		t.Fatal(err)
	}
	return reopened, service
}

func TestLegacySnapshotIdentityAndComponentStacksAcrossRestart(t *testing.T) {
	db, _, _, _ := grantSetup(t)
	ctx := context.Background()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{sql: "INSERT INTO groups VALUES(-1,'en')"},
		{sql: "INSERT INTO characters VALUES('legacy-owner',-1,2,'Recipient')"},
		{
			sql:  "INSERT INTO item_snapshots VALUES('legacy-snapshot','coin',?,?)",
			args: []any{[]byte(preComponentSnapshot), "0b20e996759455025e77f067737bdc07d7cbd6ea6f6fa324cf62b4b8659d69db"},
		},
		{sql: "INSERT INTO inventory_entries VALUES('legacy-stack',-1,'legacy-owner','legacy-snapshot','stackable',5)"},
	} {
		if _, err := db.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	var rowID int64
	if err := db.db.QueryRow("SELECT rowid FROM inventory_entries WHERE id='legacy-stack'").Scan(&rowID); err != nil {
		t.Fatal(err)
	}

	var legacy items.Snapshot
	if err := json.Unmarshal([]byte(preComponentSnapshot), &legacy); err != nil {
		t.Fatal(err)
	}

	db, service := reopenComponentDatabase(t, db)
	for i, object := range []string{`{}`, `{"material":{}}`, `{"weapon":{},"material":{}}`, `{"material":{},"weapon":{}}`, `{}`} {
		snapshot := legacy
		if err := json.Unmarshal([]byte(object), &snapshot.Components); err != nil {
			t.Fatal(err)
		}

		if _, err := service.Give(ctx, request(int64(i+10), 2), snapshotCatalog{snapshot: snapshot}); err != nil {
			t.Fatal(err)
		}

		if i == 2 {
			db, service = reopenComponentDatabase(t, db)
		}
	}

	entries, err := db.Entries(ctx, -1, "legacy-owner")
	if err != nil || len(entries) != 3 {
		t.Fatalf("component stacks: %+v %v", entries, err)
	}

	if entries[0].ID != "legacy-stack" || entries[0].SnapshotID != "legacy-snapshot" || entries[0].Quantity != 9 || entries[0].Snapshot != legacy ||
		entries[1].Quantity != 2 || !entries[1].Snapshot.Components.Material || entries[1].Snapshot.Components.Weapon ||
		entries[2].Quantity != 4 || !entries[2].Snapshot.Components.Material || !entries[2].Snapshot.Components.Weapon {
		t.Fatalf("stack identities/order changed: %+v", entries)
	}

	var position int64
	if err := db.db.QueryRow("SELECT rowid FROM inventory_entries WHERE id='legacy-stack'").Scan(&position); err != nil || position != rowID {
		t.Fatalf("acquisition position changed: %d %v", position, err)
	}

	var data []byte
	var digest string
	if err := db.db.QueryRow("SELECT canonical_json,digest FROM item_snapshots WHERE id='legacy-snapshot'").Scan(&data, &digest); err != nil || string(data) != preComponentSnapshot || digest != "0b20e996759455025e77f067737bdc07d7cbd6ea6f6fa324cf62b4b8659d69db" {
		t.Fatalf("legacy snapshot rewritten: %s %s %v", data, digest, err)
	}
}
