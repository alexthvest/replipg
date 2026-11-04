package inventory

import (
	"context"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func inventoryFixture(t *testing.T) (*persistence.DB, fixtureCatalog, *testsupport.ManualClock, string) {
	t.Helper()
	dir := t.TempDir()
	db := openInventoryDB(t, dir)
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	return db, catalog, testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), dir
}

func openInventoryDB(t *testing.T, dir string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	return db
}

func inventoryTx(t *testing.T, db *persistence.DB, fn func(context.Context, runtimecontract.DBTX) error) {
	t.Helper()
	if err := db.WithinTransaction(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}
