package character_test

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	runtimeconfig "github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func serviceFixture(t *testing.T) (*character.Service, *persistence.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db := openCharacterDB(t, dir)
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	clock := testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service, db, dir
}
func openCharacterDB(t *testing.T, dir string) *persistence.DB {
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
func inCharacterTx(t *testing.T, db *persistence.DB, fn func(context.Context, runtimecontract.DBTX) error) {
	t.Helper()
	if err := db.WithinTransaction(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}
func TestEnsureCharacterAndStableOwnership(t *testing.T) {
	service, db, _ := serviceFixture(t)
	var first contract.Snapshot
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var created bool
		var err error
		first, created, err = service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		if !created || first.UserID != 42 || first.Health != 100 || first.MaxHealth != 100 || first.Attack != 10 || first.Defense != 5 || first.State != contract.StateActive || first.Revision != 1 || !first.RecoverAt.IsZero() {
			t.Fatalf("invalid new character: %+v created=%v", first, created)
		}
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(string(first.ID)) {
			t.Fatalf("not opaque 128-bit hex ID: %q", first.ID)
		}
		second, created, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		if created || second != first {
			t.Fatal("repeat creation changed character")
		}
		other, created, err := service.Ensure(ctx, tx, 43)
		if err != nil {
			return err
		}
		if !created || other.ID == first.ID || other.UserID != 43 {
			t.Fatal("owners not separated")
		}
		return nil
	})
}
func TestLookupDoesNotCreateAndInvalidInputDoesNotWrite(t *testing.T) {
	service, db, _ := serviceFixture(t)
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, found, err := service.Lookup(ctx, tx, 42)
		if err != nil {
			return err
		}
		if found || got != (contract.Snapshot{}) {
			t.Fatal("missing lookup created character")
		}
		for _, id := range []runtimecontract.UserID{0, -1} {
			if _, _, err := service.Ensure(ctx, tx, id); !errors.Is(err, contract.ErrInvalidInput) {
				t.Fatalf("invalid identity accepted: %v", err)
			}
			if _, _, err := service.Lookup(ctx, tx, id); !errors.Is(err, contract.ErrInvalidInput) {
				t.Fatalf("invalid lookup identity accepted: %v", err)
			}
		}
		return nil
	})
}
func TestEnsurePreservesStoredStateAcrossReopenAndTuning(t *testing.T) {
	service, db, dir := serviceFixture(t)
	var want contract.Snapshot
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		want = got
		want.Health = 0
		want.State = contract.StateIncapacitated
		want.RecoverAt = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		want.Revision = 2
		_, err = tx.ExecContext(ctx, `UPDATE characters SET health=0,recover_at_ms=?,recovery_health=25,revision=2 WHERE id=?`, want.RecoverAt.UnixMilli(), got.ID)
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openCharacterDB(t, dir)
	cfg, err := character.ParseConfig([]byte(`{"max_health":200,"attack":20,"defense":15,"incapacitation_duration":"1h","recovery_percent":"50"}`))
	if err != nil {
		t.Fatal(err)
	}
	service, err = character.NewService(cfg, func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, created, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		if created || got != want {
			t.Fatalf("stored character changed: %+v want %+v", got, want)
		}
		got, found, err := service.Lookup(ctx, tx, 42)
		if err != nil {
			return err
		}
		if !found || got != want {
			t.Fatal("lookup changed stored state")
		}
		return nil
	})
}
func TestEnsureConcurrentRequestsHaveOneInitialState(t *testing.T) {
	service, db, _ := serviceFixture(t)
	const count = 8
	var wg sync.WaitGroup
	results := make(chan contract.Snapshot, count)
	createdResults := make(chan bool, count)
	failures := make(chan error, count)
	for range count {
		wg.Go(func() {
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, created, err := service.Ensure(ctx, tx, 42)
				if err != nil {
					return err
				}
				results <- got
				createdResults <- created
				return nil
			})
			if err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(results)
	close(createdResults)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first contract.Snapshot
	for got := range results {
		if first.ID == "" {
			first = got
		}
		if got != first {
			t.Fatal("concurrent creation produced different states")
		}
	}
	createdCount := 0
	for created := range createdResults {
		if created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created %d times", createdCount)
	}
}
func TestEnsureRollsBackWithCaller(t *testing.T) {
	service, db, _ := serviceFixture(t)
	rejected := errors.New("caller rejected")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, _, err := service.Ensure(ctx, tx, 42); err != nil {
			return err
		}
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, found, err := service.Lookup(ctx, tx, 42)
		if found {
			t.Fatal("creation escaped rollback")
		}
		return err
	})
}
func TestServiceConstructionValidation(t *testing.T) {
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := character.NewService(character.Config{}, time.Now); !errors.Is(err, contract.ErrInvalidConfig) {
		t.Fatalf("invalid config accepted: %v", err)
	}
	if _, err := character.NewService(cfg, nil); !errors.Is(err, contract.ErrInvalidInput) {
		t.Fatalf("nil clock accepted: %v", err)
	}
	service, err := character.NewService(cfg, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Ensure(context.Background(), nil, 42); !errors.Is(err, contract.ErrInvalidInput) {
		t.Fatalf("nil transaction accepted: %v", err)
	}
}
