package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type snapshotCatalog struct {
	snapshot items.Snapshot
	err      error
}

func (c snapshotCatalog) Capture(string) (items.Snapshot, error) {
	return c.snapshot, c.err
}

func grantSetup(t *testing.T) (*Database, *grants.Service, items.Snapshot, items.Snapshot) {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "grants.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})
	permissions, err := groups.New(db, 1)
	if err != nil {
		t.Fatal(err)
	}

	service, err := grants.New(db, permissions, core.NewID)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}

	coin, err := bundle.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	stick, err := bundle.Capture("stick")
	if err != nil {
		t.Fatal(err)
	}
	return db, service, coin, stick
}

func request(update int64, quantity int64) grants.Request {
	return grants.Request{
		Scope: core.Scope{
			BotID:    123,
			UpdateID: update,
			Group:    -1,
			Actor:    1,
		},
		Recipient: &core.User{
			ID:        2,
			FirstName: "Recipient",
		},
		ItemID:   "coin",
		Quantity: quantity,
	}
}

func counts(t *testing.T, db *Database) map[string]int {
	t.Helper()
	result := map[string]int{}
	for _, table := range []string{
		"groups",
		"characters",
		"inventory_entries",
		"item_snapshots",
		"grant_operations",
	} {
		var count int
		if err := db.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}

		result[table] = count
	}
	return result
}

func TestGrantConcurrencySnapshotsAndReplay(t *testing.T) {
	db, service, coin, stick := grantSetup(t)
	ctx := context.Background()
	catalog := snapshotCatalog{snapshot: coin}
	var joined sync.WaitGroup
	results := make(chan grants.Result, 10)
	for range 10 {
		joined.Add(1)
		go func() {
			defer joined.Done()

			result, err := service.Give(ctx, request(1, 5), catalog)
			if err != nil {
				t.Error(err)
				return
			}

			results <- result
		}()
	}

	joined.Wait()
	close(results)
	var character core.ID
	for result := range results {
		if character == "" {
			character = result.Character.ID
		}

		if result.Character.ID != character || result.Quantity != 5 {
			t.Fatal("replay outcome changed")
		}
	}

	if _, err := service.Give(ctx, request(2, 5), catalog); err != nil {
		t.Fatal(err)
	}

	edited := coin
	edited.Russian.Description = "Изменено"
	if _, err := service.Give(ctx, request(3, 5), snapshotCatalog{snapshot: edited}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Give(ctx, request(4, 5), catalog); err != nil {
		t.Fatal(err)
	}

	individual := request(5, 3)
	individual.ItemID = "stick"
	if _, err := service.Give(ctx, individual, snapshotCatalog{snapshot: stick}); err != nil {
		t.Fatal(err)
	}

	entries, err := db.Entries(ctx, -1, character)
	if err != nil || len(entries) != 5 {
		t.Fatalf("entries: %+v %v", entries, err)
	}

	for _, entry := range entries {
		if entry.Snapshot.Equal(coin) && entry.Quantity != 15 {
			t.Fatal("identical data did not merge")
		}

		if entry.Snapshot.Equal(edited) && entry.Quantity != 5 {
			t.Fatal("edited data overwritten")
		}

		if entry.Snapshot.Mode == items.Individual && entry.Quantity != 1 {
			t.Fatal("instances stacked")
		}
	}
	// A saved replay stays valid despite permission revocation and catalog deletion.
	if _, err = db.SetAdmin(ctx, -1, 3, true); err != nil {
		t.Fatal(err)
	}

	admin := request(6, 1)
	admin.Scope.Actor = 3
	if _, err = service.Give(ctx, admin, catalog); err != nil {
		t.Fatal(err)
	}

	if _, err = db.SetAdmin(ctx, -1, 3, false); err != nil {
		t.Fatal(err)
	}

	replay, err := service.Give(ctx, admin, snapshotCatalog{err: errors.New("definition deleted")})
	if err != nil || replay.Quantity != 1 {
		t.Fatalf("revoked replay: %+v %v", replay, err)
	}

	if got := counts(t, db); got["characters"] != 1 || got["grant_operations"] != 6 || got["item_snapshots"] != 3 {
		t.Fatal(got)
	}
}

func TestGrantRollbackAndNoInvalidSideEffects(t *testing.T) {
	db, service, coin, stick := grantSetup(t)
	ctx := context.Background()
	for _, input := range []grants.Request{
		request(1, 0),
		request(2, 1001),
		request(3, -1),
		func() grants.Request {
			r := request(4, 1)
			r.Recipient = nil
			return r
		}(),
		func() grants.Request {
			r := request(5, 1)
			r.Scope.Actor = 99
			return r
		}(),
	} {
		if _, err := service.Give(ctx, input, snapshotCatalog{snapshot: coin}); err == nil {
			t.Fatal("invalid grant succeeded")
		}
	}

	if _, err := service.Give(ctx, request(6, 1), snapshotCatalog{err: errors.New("unknown item")}); err == nil {
		t.Fatal("unknown item granted")
	}

	for table, count := range counts(t, db) {
		if count != 0 {
			t.Fatalf("invalid grant changed %s", table)
		}
	}

	if _, err := db.db.Exec(`CREATE TRIGGER injected_failure BEFORE INSERT ON inventory_entries WHEN (SELECT count(*) FROM inventory_entries)>=1 BEGIN SELECT RAISE(ABORT,'injected entry failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Give(ctx, request(7, 3), snapshotCatalog{snapshot: stick}); err == nil {
		t.Fatal("partial grant succeeded")
	}

	for table, count := range counts(t, db) {
		if count != 0 {
			t.Fatalf("rollback left %s", table)
		}
	}
}

func TestOverflowAndOwnershipConstraints(t *testing.T) {
	db, service, coin, stick := grantSetup(t)
	ctx := context.Background()
	result, err := service.Give(ctx, request(1, 1), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = db.db.Exec("UPDATE inventory_entries SET quantity=?", int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Give(ctx, request(2, 1), snapshotCatalog{snapshot: coin}); !errors.Is(err, inventory.ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}

	entries, err := db.Entries(ctx, -1, result.Character.ID)
	if err != nil || entries[0].Quantity != math.MaxInt64 {
		t.Fatal("overflow changed quantity")
	}

	if got := counts(t, db); got["grant_operations"] != 1 {
		t.Fatal("failed operation recorded")
	}

	err = db.WithinGrant(ctx, func(tx grants.Transaction) error {
		duplicate := entries[0]
		duplicate.ID = "duplicate-stack"
		return tx.Inventory.PutEntry(ctx, duplicate)
	})
	if err == nil {
		t.Fatal("duplicate stack accepted")
	}

	for _, quantity := range []int64{
		0,
		-1,
	} {
		if _, err = db.db.Exec("UPDATE inventory_entries SET quantity=?", quantity); err == nil {
			t.Fatal("invalid quantity accepted")
		}
	}

	err = db.WithinGrant(ctx, func(tx grants.Transaction) error {
		snapshotID, err := tx.Inventory.ReuseSnapshot(ctx, stick)
		if err != nil {
			return err
		}
		return tx.Inventory.PutEntry(ctx, inventory.Entry{
			ID:         "foreign",
			Group:      -2,
			Character:  result.Character.ID,
			SnapshotID: snapshotID,
			Snapshot:   stick,
			Quantity:   1,
		})
	})
	if err == nil {
		t.Fatal("foreign group entry accepted")
	}

	err = db.WithinGrant(ctx, func(tx grants.Transaction) error {
		snapshotID, err := tx.Inventory.ReuseSnapshot(ctx, stick)
		if err != nil {
			return err
		}
		return tx.Inventory.PutEntry(ctx, inventory.Entry{
			ID:         "bad-instance",
			Group:      -1,
			Character:  result.Character.ID,
			SnapshotID: snapshotID,
			Snapshot:   stick,
			Quantity:   2,
		})
	})
	if err == nil {
		t.Fatal("individual quantity >1 accepted")
	}
}

func TestDigestCollisionsCompareCanonicalData(t *testing.T) {
	db, _, coin, _ := grantSetup(t)
	other := coin
	other.English.Description = "Other saved data"
	var a, b, c core.ID
	err := db.withinGrant(context.Background(), func(tx grants.Transaction) error {
		var err error
		a, err = tx.Inventory.ReuseSnapshot(context.Background(), coin)
		if err != nil {
			return err
		}

		b, err = tx.Inventory.ReuseSnapshot(context.Background(), other)
		if err != nil {
			return err
		}

		c, err = tx.Inventory.ReuseSnapshot(context.Background(), coin)
		return err
	}, func([]byte) string {
		return "forced-collision"
	})
	if err != nil || a == b || a != c {
		t.Fatalf("collision comparison: %s %s %s %v", a, b, c, err)
	}
}

type cancelCommit struct {
	db     *Database
	cancel context.CancelFunc
}

func (u cancelCommit) WithinGrant(ctx context.Context, fn func(grants.Transaction) error) error {
	return u.db.WithinGrant(ctx, func(tx grants.Transaction) error {
		if err := fn(tx); err != nil {
			return err
		}

		u.cancel()
		return nil
	})
}

func TestCommitFailureRollsBackAllFeatures(t *testing.T) {
	db, _, coin, _ := grantSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	permission, err := groups.New(db, 1)
	if err != nil {
		t.Fatal(err)
	}

	service, err := grants.New(cancelCommit{
		db:     db,
		cancel: cancel,
	}, permission, core.NewID)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err == nil || result.Quantity != 0 {
		t.Fatal("commit failure reported success")
	}

	for table, count := range counts(t, db) {
		if count != 0 {
			t.Fatalf("commit failure left %s", table)
		}
	}
}

func TestLegacyAndClassifiedGrantReplayAfterRestart(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	classified := coin
	classified.Components = items.Components{
		Consumable: true,
		Material:   true,
	}

	result, err := service.Give(ctx, request(1, 3), snapshotCatalog{snapshot: classified})
	if err != nil {
		t.Fatal(err)
	}

	// Historical operation JSON, independent of the current encoder.
	legacyResult := `{"Group":-1,"Character":{"ID":"historical-owner","Group":-1,"User":2,"Name":"Historical"},"Snapshot":` + preComponentSnapshot + `,"Quantity":5,"Entries":[{"ID":"historical-stack","Group":-1,"Character":"historical-owner","SnapshotID":"historical-snapshot","Snapshot":` + preComponentSnapshot + `,"Quantity":5}]}`
	if _, err := db.db.Exec("INSERT INTO grant_operations VALUES(123,2,-1,?)", []byte(legacyResult)); err != nil {
		t.Fatal(err)
	}

	var savedClassified []byte
	if err := db.db.QueryRow("SELECT result_json FROM grant_operations WHERE update_id=1").Scan(&savedClassified); err != nil {
		t.Fatal(err)
	}

	db, service = reopenComponentDatabase(t, db)
	var historical grants.Result
	if err := json.Unmarshal([]byte(legacyResult), &historical); err != nil {
		t.Fatal(err)
	}

	before := counts(t, db)
	for range 2 {
		for i, expected := range []grants.Result{result, historical} {
			replay, err := service.Give(ctx, request(int64(i+1), 99), snapshotCatalog{err: errors.New("definition removed")})
			if err != nil || !reflect.DeepEqual(replay, expected) {
				t.Fatalf("replay %d changed saved data: %+v %v", i, replay, err)
			}
		}
	}

	if !reflect.DeepEqual(before, counts(t, db)) {
		t.Fatal("replay changed durable counts")
	}

	for i, expected := range [][]byte{savedClassified, []byte(legacyResult)} {
		var data []byte
		if err := db.db.QueryRow("SELECT result_json FROM grant_operations WHERE update_id=?", i+1).Scan(&data); err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("historical operation rewritten: %s %v", data, err)
		}
	}

	entries, err := db.Entries(ctx, -1, result.Character.ID)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 3 || entries[0].Snapshot != classified {
		t.Fatalf("classified replay changed ownership: %+v %v", entries, err)
	}
}
