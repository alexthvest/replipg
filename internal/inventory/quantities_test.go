package inventory

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const inventoryTestCharacter = "0123456789abcdef0123456789abcdef"

func TestStoreQuantityTransitions(t *testing.T) {
	db, catalog, _, dir := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,100,10,5,NULL,NULL,1)`, inventoryTestCharacter); err != nil {
			return err
		}
		if err := ensureInventoryRoot(ctx, tx, inventoryTestCharacter); err != nil {
			return err
		}
		if err := increaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 10); err != nil {
			return err
		}
		if err := increaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 5); err != nil {
			return err
		}
		if err := decreaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 14); err != nil {
			return err
		}
		if err := advanceInventoryRevision(ctx, tx, inventoryTestCharacter, 1); err != nil {
			return err
		}
		return nil
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		snapshot, starter, err := readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if err != nil {
			return err
		}
		if starter || snapshot.Revision != 2 || len(snapshot.Items) != 1 || snapshot.Items[0].Quantity != 1 {
			t.Fatalf("state %+v starter %v", snapshot, starter)
		}
		if err := decreaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 2); !errors.Is(err, contract.ErrNotOwned) {
			t.Fatalf("insufficient %v", err)
		}
		if err := increaseQuantity(ctx, tx, inventoryTestCharacter, "coin", math.MaxInt64); !errors.Is(err, contract.ErrExhausted) {
			t.Fatalf("overflow %v", err)
		}
		if err := advanceInventoryRevision(ctx, tx, inventoryTestCharacter, 1); !errors.Is(err, contract.ErrStaleControl) {
			t.Fatalf("stale %v", err)
		}
		if err := decreaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 1); err != nil {
			return err
		}
		if err := markStarterGranted(ctx, tx, inventoryTestCharacter); err != nil {
			return err
		}
		if err := ensureInventoryRoot(ctx, tx, inventoryTestCharacter); err != nil {
			return err
		}
		snapshot, starter, err = readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if !starter || len(snapshot.Items) != 0 || snapshot.Revision != 2 {
			t.Fatalf("final %+v starter%v", snapshot, starter)
		}
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		snapshot, starter, err := readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if err != nil {
			return err
		}
		if !starter || len(snapshot.Items) != 0 || snapshot.Revision != 2 {
			t.Fatalf("reopened final state %+v starter %v", snapshot, starter)
		}
		if !errors.Is(markStarterGranted(ctx, tx, inventoryTestCharacter), contract.ErrOperationConflict) {
			t.Fatal("starter marked twice")
		}
		if !errors.Is(advanceInventoryRevision(ctx, tx, inventoryTestCharacter, math.MaxInt64), contract.ErrExhausted) {
			t.Fatal("revision overflow accepted")
		}
		return nil
	})
}

func TestStoreQuantityOperationIsolationAndEquipment(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,100,10,5,NULL,NULL,1)`, inventoryTestCharacter); err != nil {
			return err
		}
		if err := ensureInventoryRoot(ctx, tx, inventoryTestCharacter); err != nil {
			return err
		}
		if err := increaseQuantity(ctx, tx, inventoryTestCharacter, "stick", 2); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO inventory_equipment VALUES(?,'weapon','stick')`, inventoryTestCharacter)
		return err
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		err := withInventoryOperation(ctx, tx, func() error {
			if err := increaseQuantity(ctx, tx, inventoryTestCharacter, "coin", 20); err != nil {
				return err
			}
			return decreaseQuantity(ctx, tx, inventoryTestCharacter, "healing_potion", 1)
		})
		if !errors.Is(err, contract.ErrNotOwned) {
			t.Fatalf("rejection %v", err)
		}
		return nil // Expected rejection may commit; no partial coin grant.
	})
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		snapshot, starter, err := readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if err != nil {
			return err
		}
		if starter || len(snapshot.Items) != 1 || snapshot.Items[0].ItemID != "stick" || snapshot.Items[0].Quantity != 2 || snapshot.Weapon != "stick" {
			t.Fatalf("partial writes or assignment loss %+v", snapshot)
		}
		if err := decreaseQuantity(ctx, tx, inventoryTestCharacter, "stick", 1); err != nil {
			return err
		}
		if err := decreaseQuantity(ctx, tx, inventoryTestCharacter, "stick", 1); !errors.Is(err, contract.ErrStorage) {
			t.Fatalf("assigned final copy deletion %v", err)
		}
		return errors.New("force outer rollback after storage failure")
	})
	if err == nil {
		t.Fatal("expected outer rollback")
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		quantity, err := ownedQuantity(ctx, tx, inventoryTestCharacter, "stick")
		if err != nil {
			return err
		}
		if quantity != 2 {
			t.Fatal("outer rollback lost equipped quantity")
		}
		for _, quantity := range []int64{0, -1} {
			if !errors.Is(increaseQuantity(ctx, tx, inventoryTestCharacter, "coin", quantity), contract.ErrInvalidInput) || !errors.Is(decreaseQuantity(ctx, tx, inventoryTestCharacter, "coin", quantity), contract.ErrInvalidInput) {
				t.Fatal("invalid quantity accepted")
			}
		}
		return nil
	})
}

func TestStoreCompletedStarterReopens(t *testing.T) {
	db, catalog, _, dir := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,100,10,5,NULL,NULL,1)`, inventoryTestCharacter); err != nil {
			return err
		}
		return withInventoryOperation(ctx, tx, func() error {
			if err := ensureInventoryRoot(ctx, tx, inventoryTestCharacter); err != nil {
				return err
			}
			for _, line := range catalog.Starter() {
				if err := increaseQuantity(ctx, tx, inventoryTestCharacter, line.ItemID, line.Quantity); err != nil {
					return err
				}
			}
			if err := advanceInventoryRevision(ctx, tx, inventoryTestCharacter, 1); err != nil {
				return err
			}
			return markStarterGranted(ctx, tx, inventoryTestCharacter)
		})
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := ensureInventoryRoot(ctx, tx, inventoryTestCharacter); err != nil {
			return err
		}
		snapshot, starter, err := readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if err != nil {
			return err
		}
		if !starter || snapshot.Revision != 2 || len(snapshot.Items) != len(catalog.Starter()) || snapshot.Weapon != "" || snapshot.Armor != "" {
			t.Fatalf("reopened kit %+v granted%v", snapshot, starter)
		}
		for _, line := range catalog.Starter() {
			quantity, err := ownedQuantity(ctx, tx, inventoryTestCharacter, line.ItemID)
			if err != nil {
				return err
			}
			if quantity != line.Quantity {
				t.Fatalf("quantity %s=%d", line.ItemID, quantity)
			}
		}
		if !errors.Is(markStarterGranted(ctx, tx, inventoryTestCharacter), contract.ErrOperationConflict) {
			t.Fatal("repeated entitlement completion accepted")
		}
		after, _, err := readInventory(ctx, tx, catalog, inventoryTestCharacter)
		if !reflect.DeepEqual(after, snapshot) {
			t.Fatal("root/repeated entitlement modified kit")
		}
		return err
	})
}
