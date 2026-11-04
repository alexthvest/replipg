package inventory

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func inventoryCharacters(t *testing.T, now func() time.Time) *character.Service {
	t.Helper()
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func inventoryService(t *testing.T, catalog Catalog, characters characterPort, now func() time.Time) *Service {
	t.Helper()
	s, err := NewService(catalog, characters, now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestServiceOpenStarter(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var first contract.OpenResult
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		var err error
		first, err = service.Open(ctx, tx, 42)
		return err
	})
	if !first.CharacterCreated || !first.StarterGranted || first.Current.Revision != 2 || first.Current.Weapon != "" || first.Current.Armor != "" {
		t.Fatalf("first: %+v", first)
	}
	for _, line := range catalog.Starter() {
		found := false
		for _, item := range first.Current.Items {
			if item.ItemID == line.ItemID && item.Quantity == line.Quantity {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing starter %v", line)
		}
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		again, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		if again.CharacterCreated || again.StarterGranted || !reflect.DeepEqual(again.Current, first.Current) {
			t.Fatalf("repeat: %+v", again)
		}
		return nil
	})
}
func TestServiceInspectionDoesNotInitialize(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := service.Inspect(ctx, tx, "missing"); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		ch, _, err := chars.Ensure(ctx, tx, 7)
		if err != nil {
			return err
		}
		snapshot, err := service.Inspect(ctx, tx, ch.ID)
		if err != nil {
			return err
		}
		mods, err := service.Modifiers(ctx, tx, ch.ID)
		if err != nil {
			return err
		}
		if snapshot.CharacterID != ch.ID || snapshot.Revision != 0 || len(snapshot.Items) != 0 || mods != (contract.Modifiers{}) {
			t.Fatalf("uninitialized: %+v %+v", snapshot, mods)
		}
		return nil
	})
}
func TestServiceStarterPreservesCharacterAndPendingEntitlement(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		ch, _, err := chars.Ensure(ctx, tx, 7)
		if err != nil {
			return err
		}
		damaged, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "test:damage", CharacterID: ch.ID, Kind: charactercontract.HealthDamage, Amount: 15})
		if err != nil {
			return err
		}
		if _, err := service.grant(ctx, tx, contract.GrantRequest{OperationID: "test:reward", CharacterID: ch.ID, Items: []contract.GrantLine{{ItemID: catalog.Starter()[0].ItemID, Quantity: 8}}}, "grant"); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 7)
		if err != nil {
			return err
		}
		after, _, err := chars.Lookup(ctx, tx, 7)
		if err != nil {
			return err
		}
		if opened.CharacterCreated || !opened.StarterGranted || after != damaged.Current {
			t.Fatalf("reset: %+v %+v", opened, after)
		}
		for _, item := range opened.Current.Items {
			if item.ItemID == catalog.Starter()[0].ItemID && item.Quantity != 8+catalog.Starter()[0].Quantity {
				t.Fatalf("quantity: %+v", item)
			}
		}
		return nil
	})
}
func TestServiceFailedStarterCanCommitRejectionAndRetry(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var id charactercontract.ID
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		ch, _, err := chars.Ensure(ctx, tx, 8)
		if err != nil {
			return err
		}
		id = ch.ID
		if err := ensureInventoryRoot(ctx, tx, id); err != nil {
			return err
		}
		// Overflow on the final starter line after earlier quantities have been written.
		lines := catalog.Starter()
		return increaseQuantity(ctx, tx, id, lines[len(lines)-1].ItemID, math.MaxInt64)
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := service.Open(ctx, tx, 8); !errors.Is(err, contract.ErrExhausted) {
			t.Fatalf("overflow: %v", err)
		}
		current, starter, err := readInventory(ctx, tx, catalog, id)
		if err != nil {
			return err
		}
		if starter || len(current.Items) != 1 || current.Revision != 1 {
			t.Fatalf("partial starter: %+v %v", current, starter)
		}
		return nil // Deliberately commit a handled domain rejection.
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		lines := catalog.Starter()
		if err := decreaseQuantity(ctx, tx, id, lines[len(lines)-1].ItemID, math.MaxInt64); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 8)
		if err == nil && !opened.StarterGranted {
			t.Fatal("entitlement lost")
		}
		return err
	})
}
func TestServiceConcurrentAndRestartOpening(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, service.Prepare)
	var wg sync.WaitGroup
	results := make(chan contract.OpenResult, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			errs <- db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := service.Open(ctx, tx, 9)
				if err == nil {
					results <- result
				}
				return err
			})
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	grants := 0
	var before contract.Snapshot
	for result := range results {
		if result.StarterGranted {
			grants++
		}
		before = result.Current
	}
	if grants != 1 {
		t.Fatalf("grants=%d", grants)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openInventoryDB(t, dir)
	changed, err := ParseCatalog([]byte(strings.Replace(string(embeddedCatalog), `"quantity":3`, `"quantity":30`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(changed.Starter(), catalog.Starter()) {
		t.Fatal("fixture did not change tuning")
	}
	service = inventoryService(t, changed, chars, clock.Now)
	inventoryTx(t, reopened, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 9)
		if err != nil {
			return err
		}
		if opened.StarterGranted || opened.CharacterCreated || !reflect.DeepEqual(before, opened.Current) {
			t.Fatalf("restart regrant: %+v", opened)
		}
		return nil
	})
}
func TestServiceConstructorRejectsInvalidInputs(t *testing.T) {
	_, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	for _, tc := range []struct {
		catalog Catalog
		chars   characterPort
		clock   func() time.Time
	}{{Catalog{}, chars, clock.Now}, {catalog, nil, clock.Now}, {catalog, (*character.Service)(nil), clock.Now}, {catalog, chars, nil}} {
		if _, err := NewService(tc.catalog, tc.chars, tc.clock); err == nil {
			t.Fatal("accepted invalid inputs")
		}
	}
}

func TestServiceFailedNewOpeningRollsBackCharacterAndReceipt(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_starter BEFORE UPDATE OF starter_granted ON inventories WHEN NEW.starter_granted=1 BEGIN SELECT RAISE(ABORT,'test failure'); END`)
		return err
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := service.Open(ctx, tx, 11)
		var failure runtimecontract.StorageFailure
		if !errors.As(err, &failure) || !reflect.DeepEqual(result, contract.OpenResult{}) {
			t.Fatalf("failure: %+v %v", result, err)
		}
		if _, found, err := chars.Lookup(ctx, tx, 11); err != nil || found {
			t.Fatalf("partial character: %v %v", found, err)
		}
		for _, table := range []string{"inventories", "inventory_items", "inventory_operations"} {
			var count int
			if err := queryInventoryRows(ctx, tx, `SELECT count(*) FROM `+table, nil, func(rows *sql.Rows) error { return rows.Scan(&count) }); err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("partial %s: %d", table, count)
			}
		}
		return nil // Test isolation even if a caller incorrectly commits storage failure.
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER reject_starter`); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 11)
		if err == nil && (!opened.CharacterCreated || !opened.StarterGranted) {
			t.Fatalf("retry: %+v", opened)
		}
		return err
	})
}

func TestServiceOpeningPreservesIncapacitationAndNormalizesDueRecovery(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var defeated charactercontract.Snapshot
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		ch, _, err := chars.Ensure(ctx, tx, 12)
		if err != nil {
			return err
		}
		mutation, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "test:defeat", CharacterID: ch.ID, Kind: charactercontract.HealthDamage, Amount: ch.MaxHealth})
		if err != nil {
			return err
		}
		defeated = mutation.Current
		if _, err := service.Open(ctx, tx, 12); err != nil {
			return err
		}
		if _, err := service.Inspect(ctx, tx, ch.ID); err != nil {
			return err
		}
		after, _, err := chars.Lookup(ctx, tx, 12)
		if err != nil {
			return err
		}
		if after != defeated {
			t.Fatalf("incapacitation cleared: %+v", after)
		}
		return nil
	})
	clock.Advance(24 * time.Hour)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := service.Modifiers(ctx, tx, defeated.ID); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 12)
		if err != nil {
			return err
		}
		after, _, err := chars.Lookup(ctx, tx, 12)
		if err != nil {
			return err
		}
		if opened.StarterGranted || after.State != charactercontract.StateActive || after.Health <= 0 || after.Revision != defeated.Revision+1 {
			t.Fatalf("due recovery: %+v %+v", opened, after)
		}
		return nil
	})
}
