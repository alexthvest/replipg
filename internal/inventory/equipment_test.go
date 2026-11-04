package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestEquipmentMutationReplayAndStaleness(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	characters := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, characters, clock.Now)
	var snapshot contract.Snapshot

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		snapshot = opened.Current
		return err
	})
	request := contract.EquipmentChange{OperationID: "test:equip:one", CharacterID: snapshot.CharacterID, Slot: contract.SlotWeapon, ItemID: "stick", ExpectedRevision: snapshot.Revision}
	var first contract.MutationResult

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		first, err = service.ChangeEquipment(ctx, tx, request)
		if err != nil {
			return err
		}

		if !first.Applied || first.Current.Weapon != "stick" || first.AppliedRevision != snapshot.Revision+1 || first.Current.Modifiers.Attack != 1 {
			t.Fatalf("equip %+v", first)
		}

		stale := request
		stale.OperationID = "test:equip:stale"
		stale.ItemID = ""
		if _, err := service.ChangeEquipment(ctx, tx, stale); !errors.Is(err, contract.ErrStaleControl) {
			t.Fatalf("stale %v", err)
		}

		changed := request
		changed.ItemID = ""
		if _, err := service.ChangeEquipment(ctx, tx, changed); !errors.Is(err, contract.ErrOperationConflict) {
			t.Fatalf("conflict %v", err)
		}

		noOp := request
		noOp.OperationID = "test:equip:noop"
		noOp.ExpectedRevision = first.Current.Revision
		result, err := service.ChangeEquipment(ctx, tx, noOp)
		if err != nil {
			return err
		}

		if !result.Applied || result.Current.Revision != first.Current.Revision {
			t.Fatalf("noop %+v", result)
		}

		unequip := noOp
		unequip.OperationID = "test:unequip"
		unequip.ItemID = ""
		result, err = service.ChangeEquipment(ctx, tx, unequip)
		if err != nil {
			return err
		}

		if result.Current.Weapon != "" || result.Current.Modifiers.Attack != 0 {
			t.Fatal("unequip")
		}

		return nil
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		replay, err := service.ChangeEquipment(ctx, tx, request)
		if err != nil {
			return err
		}

		if replay.Applied || replay.AppliedRevision != first.AppliedRevision || replay.Current.Weapon != "" || replay.Current.Revision <= first.AppliedRevision {
			t.Fatalf("later state replay %+v", replay)
		}

		for _, item := range replay.Current.Items {
			if item.ItemID == "stick" && item.Quantity != 1 {
				t.Fatal("equipment changed total ownership")
			}
		}
		return nil
	})
}

func TestEquipmentReplacementAndFailureRollback(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	alternate := itemcontract.Definition{ID: "sword", Category: itemcontract.CategoryWeapon, AttackBonus: 3}
	catalog.items = append(catalog.Items(), alternate)
	catalog.byID[alternate.ID] = alternate
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	var snapshot contract.Snapshot

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		snapshot = opened.Current
		if err := increaseQuantity(ctx, tx, snapshot.CharacterID, "sword", 2); err != nil {
			return err
		}

		first, err := service.ChangeEquipment(ctx, tx, contract.EquipmentChange{OperationID: "replace:first", CharacterID: snapshot.CharacterID, Slot: contract.SlotWeapon, ItemID: "stick", ExpectedRevision: snapshot.Revision})
		if err != nil {
			return err
		}

		snapshot = first.Current
		return nil
	})
	request := contract.EquipmentChange{OperationID: "replace:sword", CharacterID: snapshot.CharacterID, Slot: contract.SlotWeapon, ItemID: "sword", ExpectedRevision: snapshot.Revision}
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER reject_inventory_equipment_receipt BEFORE INSERT ON inventory_operations WHEN NEW.kind='equipment' BEGIN SELECT RAISE(ABORT,'private receipt failure'); END`); err != nil {
		t.Fatal(err)
	}

	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := service.ChangeEquipment(ctx, tx, request)
		if !errors.Is(err, contract.ErrStorage) {
			t.Fatalf("receipt fault classification %v", err)
		}

		return err
	})
	if err == nil {
		t.Fatalf("receipt fault %v", err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		after, _, err := readInventory(ctx, tx, catalog, snapshot.CharacterID)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(after, snapshot) {
			t.Fatal("failed receipt changed assignment/revision/quantities")
		}

		if _, err := tx.ExecContext(ctx, `DROP TRIGGER reject_inventory_equipment_receipt`); err != nil {
			return err
		}

		result, err := service.ChangeEquipment(ctx, tx, request)
		if err != nil {
			return err
		}

		if result.Current.Weapon != "sword" || result.Current.Modifiers.Attack != 3 || result.Current.Revision != snapshot.Revision+1 {
			t.Fatalf("replacement %+v", result)
		}

		for _, line := range result.Current.Items {
			if line.ItemID == "sword" && line.Quantity != 2 || line.ItemID == "stick" && line.Quantity != 1 {
				t.Fatal("replacement removed ownership")
			}
		}
		return nil
	})
}
