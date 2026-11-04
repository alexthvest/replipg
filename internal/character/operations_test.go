package character

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	runtimeconfig "github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func operationDB(t *testing.T, dir string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: dir, StorageFilesystem: runtimeconfig.FilesystemLocal, BusyTimeout: time.Second})
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

func operationFixture(t *testing.T) (*persistence.DB, string, contract.HealthChange) {
	t.Helper()
	dir := t.TempDir()
	db := operationDB(t, dir)
	cfg, err := LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	service, err := NewService(cfg, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	change := contract.HealthChange{OperationID: "battle:1:target:1", Kind: contract.HealthDamage, Amount: 0}
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		snapshot, _, err := service.Ensure(ctx, tx, 42)
		change.CharacterID = snapshot.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}

	return db, dir, change
}

func TestOperationReceiptsReplayEqualityAndReopen(t *testing.T) {
	db, dir, change := operationFixture(t)
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		revision, found, err := checkOperation(ctx, tx, change)
		if err != nil {
			return err
		}

		if found || revision != 0 {
			t.Fatal("missing receipt found")
		}

		return recordOperation(ctx, tx, change, 1)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = operationDB(t, dir)
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		revision, found, err := checkOperation(ctx, tx, change)
		if err != nil {
			return err
		}

		if !found || revision != 1 {
			t.Fatal("lost original receipt revision")
		}

		for _, tc := range []struct {
			name   string
			change func(*contract.HealthChange)
		}{
			{"target", func(c *contract.HealthChange) { c.CharacterID = "another-character" }},
			{"kind", func(c *contract.HealthChange) { c.Kind = contract.HealthHeal }},
			{"amount", func(c *contract.HealthChange) { c.Amount = 1 }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				other := change
				tc.change(&other)
				revision, found, err := checkOperation(ctx, tx, other)
				if !errors.Is(err, contract.ErrOperationConflict) || found || revision != 0 {
					t.Fatalf("changed replay result: %d %v %v", revision, found, err)
				}
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOperationReceiptRollbackDoesNotConsumeIdentity(t *testing.T) {
	db, _, change := operationFixture(t)
	rejected := errors.New("caller rejected")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := recordOperation(ctx, tx, change, 1); err != nil {
			return err
		}

		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}

	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, found, err := checkOperation(ctx, tx, change)
		if err != nil {
			return err
		}

		if found {
			t.Fatal("receipt escaped caller rollback")
		}

		change.Amount = 7
		return recordOperation(ctx, tx, change, 2)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOperationReceiptInvalidInputDoesNotWrite(t *testing.T) {
	db, _, change := operationFixture(t)
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		invalid := change
		invalid.Amount = -1
		if _, _, err := checkOperation(ctx, tx, invalid); !errors.Is(err, contract.ErrInvalidInput) {
			t.Fatalf("invalid lookup accepted: %v", err)
		}

		if err := recordOperation(ctx, tx, invalid, 1); !errors.Is(err, contract.ErrInvalidInput) {
			t.Fatalf("invalid receipt accepted: %v", err)
		}

		if err := recordOperation(ctx, tx, change, 0); !errors.Is(err, contract.ErrInvalidInput) {
			t.Fatalf("invalid revision accepted: %v", err)
		}

		_, found, err := checkOperation(ctx, tx, change)

		if found {
			t.Fatal("invalid work consumed identity")
		}

		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOperationReceiptStorageFailurePreservesCause(t *testing.T) {
	db, _, change := operationFixture(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, err := range []error{recordOperation(context.Background(), db, change, 1), func() error { _, _, err := checkOperation(context.Background(), db, change); return err }()} {
		if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, persistence.ErrPersistenceClosed) {
			t.Fatalf("lost storage classification: %v", err)
		}

		if err.Error() != "character storage operation failed" {
			t.Fatalf("unsafe storage error: %v", err)
		}
	}
}
