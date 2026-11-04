package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func seedEquipmentStore(t *testing.T, db *persistence.DB, catalog Catalog) Catalog {
	t.Helper()
	for _, item := range []ItemDefinition{
		{ID: "sword", Category: contract.CategoryWeapon, AttackBonus: 7},
		{ID: "plate", Category: contract.CategoryArmor, DefenseBonus: 9},
		{ID: "unowned_weapon", Category: contract.CategoryWeapon},
	} {
		catalog.items = append(catalog.items, item)
		catalog.byID[item.ID] = item
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if err := seedStoreCharacter(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventories VALUES(?,7,1)`, storeCharacterID); err != nil {
			return err
		}
		for _, item := range catalog.Items() {
			if item.ID == "unowned_weapon" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_items VALUES(?,?,3)`, storeCharacterID, item.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return catalog
}

func TestEquipmentStoreRoundTrip(t *testing.T) {
	rootT := t
	db, catalog, _, dir := inventoryFixture(t)
	catalog = seedEquipmentStore(t, db, catalog)
	var owned []contract.OwnedItem
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		current, _, err := readInventory(ctx, tx, catalog, storeCharacterID)
		owned = current.Items
		return err
	})
	for _, step := range []struct {
		name          string
		weapon, armor contract.ItemID
		modifiers     contract.Modifiers
	}{
		{name: "empty"},
		{name: "equipped", weapon: "stick", armor: "cloth_armor", modifiers: contract.Modifiers{Attack: 1, Defense: 1}},
		{name: "same assignments", weapon: "stick", armor: "cloth_armor", modifiers: contract.Modifiers{Attack: 1, Defense: 1}},
		{name: "replaced", weapon: "sword", armor: "plate", modifiers: contract.Modifiers{Attack: 7, Defense: 9}},
		{name: "weapon unequipped", armor: "plate", modifiers: contract.Modifiers{Defense: 9}},
		{name: "all unequipped"},
		{name: "previous copies reequipped", weapon: "stick", armor: "cloth_armor", modifiers: contract.Modifiers{Attack: 1, Defense: 1}},
	} {
		t.Run(step.name, func(t *testing.T) {
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := writeEquipment(ctx, tx, storeCharacterID, contract.SlotWeapon, step.weapon); err != nil {
					return err
				}
				return writeEquipment(ctx, tx, storeCharacterID, contract.SlotArmor, step.armor)
			})
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db = openInventoryDB(rootT, dir)
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				current, starter, err := readInventory(ctx, tx, catalog, storeCharacterID)
				if err != nil {
					return err
				}
				if !starter || current.Revision != 7 || current.Weapon != step.weapon || current.Armor != step.armor || current.Modifiers != step.modifiers || !reflect.DeepEqual(current.Items, owned) {
					t.Fatalf("state=%+v starter=%v, want weapon=%s armor=%s modifiers=%+v owned=%+v", current, starter, step.weapon, step.armor, step.modifiers, owned)
				}
				page, err := readInventoryPage(ctx, tx, catalog, storeCharacterID, 0)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(page.Current, current) {
					t.Fatalf("page differs: %+v", page)
				}
				var maxHealth, health, attack, defense, recoverAt, recoveryHealth, revision int64
				if err := tx.QueryRowContext(ctx, `SELECT max_health,health,attack,defense,recover_at_ms,recovery_health,revision FROM characters WHERE id=?`, storeCharacterID).Scan(&maxHealth, &health, &attack, &defense, &recoverAt, &recoveryHealth, &revision); err != nil {
					return err
				}
				if maxHealth != 100 || health != 0 || attack != 10 || defense != 5 || recoverAt != 1 || recoveryHealth != 25 || revision != 7 {
					t.Fatalf("character changed: maxHealth=%d health=%d attack=%d defense=%d recoverAt=%d recoveryHealth=%d revision=%d", maxHealth, health, attack, defense, recoverAt, recoveryHealth, revision)
				}
				return nil
			})
		})
	}
}

func TestEquipmentStoreRejectsInvalidAssignments(t *testing.T) {
	for _, replacing := range []bool{false, true} {
		for _, item := range []contract.ItemID{"unowned_weapon", "missing", "cloth_armor", "coin", "healing_potion"} {
			t.Run(string(item)+map[bool]string{false: "/insert", true: "/replace"}[replacing], func(t *testing.T) {
				db, catalog, _, _ := inventoryFixture(t)
				catalog = seedEquipmentStore(t, db, catalog)
				if replacing {
					inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
						return writeEquipment(ctx, tx, storeCharacterID, contract.SlotWeapon, "stick")
					})
				}
				var assignmentErr error
				err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
					assignmentErr = writeEquipment(ctx, tx, storeCharacterID, contract.SlotWeapon, item)
					return assignmentErr
				})
				if err == nil || !errors.Is(assignmentErr, contract.ErrStorage) {
					t.Fatalf("assignment failure=%v transaction=%v", assignmentErr, err)
				}
				inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					current, _, err := readInventory(ctx, tx, catalog, storeCharacterID)
					if err != nil {
						return err
					}
					want := contract.ItemID("")
					if replacing {
						want = "stick"
					}
					if current.Weapon != want || current.Revision != 7 || len(current.Items) != 6 {
						t.Fatalf("rejected assignment changed state: %+v", current)
					}
					return nil
				})
			})
		}
	}
}

func TestEquipmentStoreCallerRollbackAndStorageErrors(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	catalog = seedEquipmentStore(t, db, catalog)
	rollback := errors.New("caller rollback")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := writeEquipment(ctx, tx, storeCharacterID, contract.SlotWeapon, "stick"); err != nil {
			return err
		}
		current, _, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if err != nil {
			return err
		}
		if current.Weapon != "stick" || current.Modifiers.Attack != 1 {
			t.Fatalf("transaction cannot read its assignment: %+v", current)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback=%v", err)
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		current, _, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if current.Weapon != "" || current.Modifiers != (contract.Modifiers{}) || current.Revision != 7 {
			t.Fatalf("write escaped caller rollback: %+v", current)
		}
		return err
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, item := range []contract.ItemID{"stick", ""} {
		err := writeEquipment(ctx, db, storeCharacterID, contract.SlotWeapon, item)
		if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, context.Canceled) {
			t.Fatalf("storage cause lost: %v", err)
		}
	}
}

func TestEquipmentStoreRequiresPositiveBackingOwnership(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	catalog = seedEquipmentStore(t, db, catalog)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		return writeEquipment(ctx, tx, storeCharacterID, contract.SlotWeapon, "stick")
	})
	for _, query := range []string{
		`DELETE FROM inventory_items WHERE character_id=? AND item_id='stick'`,
		`UPDATE inventory_items SET quantity=0 WHERE character_id=? AND item_id='stick'`,
	} {
		err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			_, err := tx.ExecContext(ctx, query, storeCharacterID)
			return err
		})
		if err == nil {
			t.Fatal("equipped item lost positive backing ownership")
		}
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		current, _, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if err != nil {
			return err
		}
		quantity, err := ownedQuantity(ctx, tx, storeCharacterID, "stick")
		if err != nil {
			return err
		}
		if current.Weapon != "stick" || current.Modifiers.Attack != 1 || quantity != 3 {
			t.Fatalf("rejected ownership change altered state: snapshot=%+v quantity=%d", current, quantity)
		}
		return nil
	})
}
