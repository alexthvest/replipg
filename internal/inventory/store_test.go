package inventory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const storeCharacterID charactercontract.ID = "0123456789abcdef0123456789abcdef"

func seedStoreCharacter(ctx context.Context, tx runtimecontract.DBTX) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,0,10,5,1,25,7)`, storeCharacterID)
	return err
}

func TestStoreCatalogPreparation(t *testing.T) {
	db, catalog, _, dir := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return prepareCatalog(ctx, tx, catalog) })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openInventoryDB(t, dir)
	display, err := ParseCatalog([]byte(strings.ReplaceAll(string(embeddedCatalog), "A simple wooden stick.", "Updated description.")))
	if err != nil {
		t.Fatal(err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return prepareCatalog(ctx, tx, display) })
	for _, kind := range []string{"changed", "removed"} {
		t.Run(kind, func(t *testing.T) {
			candidate := catalog
			candidate.items = append([]ItemDefinition{{ID: "added", Category: contract.CategoryInert}}, catalog.Items()...)
			candidate.byID = make(map[contract.ItemID]ItemDefinition)
			for i, item := range candidate.items {
				if item.ID == "stick" && kind == "changed" {
					item.AttackBonus++
					candidate.items[i] = item
				}
				candidate.byID[item.ID] = item
			}

			if kind == "removed" {
				delete(candidate.byID, "stick")
			}
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error { return prepareCatalog(ctx, tx, candidate) })
			if !errors.Is(err, contract.ErrInvalidCatalog) {
				t.Fatalf("mismatch=%v", err)
			}

			var count int

			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_catalog WHERE item_id='added'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial registration count=%d err=%v", count, err)
			}
		})
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var count int

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventories`).Scan(&count); err != nil {
			return err
		}

		if count != 0 {
			t.Fatal("preparation initialized inventory")
		}

		return nil
	})
}

func TestStoreReadsEmptyAndMissing(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}

		if _, _, err := readInventory(ctx, tx, catalog, storeCharacterID); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("missing=%v", err)
		}

		if err := seedStoreCharacter(ctx, tx); err != nil {
			return err
		}

		owner, err := characterOwner(ctx, tx, storeCharacterID)
		if err != nil {
			return err
		}

		if owner != 42 {
			t.Fatal("owner")
		}

		got, starter, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if err != nil {
			return err
		}

		if got.CharacterID != storeCharacterID || got.Revision != 0 || len(got.Items) != 0 || got.Modifiers != (contract.Modifiers{}) || starter {
			t.Fatalf("empty=%+v starter=%v", got, starter)
		}

		page, err := readInventoryPage(ctx, tx, catalog, storeCharacterID, 999)
		if err != nil {
			return err
		}

		if page.Page != 0 || page.HasNext || len(page.Current.Items) != 0 {
			t.Fatalf("empty page=%+v", page)
		}

		var health, revision int64

		if err := tx.QueryRowContext(ctx, `SELECT health,revision FROM characters`).Scan(&health, &revision); err != nil {
			return err
		}

		if health != 0 || revision != 7 {
			t.Fatal("read changed health")
		}

		var count int

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventories`).Scan(&count); err != nil {
			return err
		}

		if count != 0 {
			t.Fatal("read initialized inventory")
		}

		return nil
	})
}

func TestStoreReadsPagesEquipmentAndReopen(t *testing.T) {
	db, catalog, _, dir := inventoryFixture(t)
	for i := 0; i < 18; i++ {
		item := ItemDefinition{ID: contract.ItemID(fmt.Sprintf("extra_%02d", i)), Category: contract.CategoryInert}
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

		if _, err := tx.ExecContext(ctx, `INSERT INTO inventories VALUES(?,9,1)`, storeCharacterID); err != nil {
			return err
		}

		for _, item := range catalog.Items() {
			if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_items VALUES(?,?,3)`, storeCharacterID, item.ID); err != nil {
				return err
			}
		}

		_, err := tx.ExecContext(ctx, `INSERT INTO inventory_equipment VALUES(?,'weapon','stick'),(?,'armor','cloth_armor')`, storeCharacterID, storeCharacterID)
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		full, starter, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if err != nil {
			return err
		}

		if !starter || full.Revision != 9 || full.Weapon != "stick" || full.Armor != "cloth_armor" || full.Modifiers != (contract.Modifiers{Attack: 1, Defense: 1}) {
			t.Fatalf("full=%+v starter=%v", full, starter)
		}

		var gathered []contract.OwnedItem

		for pageNumber := 0; pageNumber < 3; pageNumber++ {
			page, err := readInventoryPage(ctx, tx, catalog, storeCharacterID, pageNumber)
			if err != nil {
				return err
			}

			if page.Page != pageNumber || page.HasNext != (pageNumber < 2) || len(page.Current.Items) > 8 || page.Current.Modifiers != full.Modifiers || page.Current.Weapon != full.Weapon || page.Current.Revision != 9 {
				t.Fatalf("page=%+v", page)
			}

			gathered = append(gathered, page.Current.Items...)
		}

		if !reflect.DeepEqual(gathered, full.Items) || len(full.Items) != 22 || full.Items[1].ItemID != "coin" {
			t.Fatalf("page omissions full=%+v gathered=%+v", full.Items, gathered)
		}

		for _, n := range []int{-5, 999, int(^uint(0) >> 1)} {
			page, err := readInventoryPage(ctx, tx, catalog, storeCharacterID, n)
			if err != nil {
				return err
			}

			want := 2
			if n < 0 {
				want = 0
			}

			if page.Page != want {
				t.Fatalf("clamp %d=%d", n, page.Page)
			}
		}
		return nil
	})
}

func TestStoreCatalogNewIdentityAndStorageErrors(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return prepareCatalog(ctx, tx, catalog) })
	item := ItemDefinition{ID: "new_coin", Category: contract.CategoryInert}
	catalog.items = append(catalog.Items(), item)
	catalog.byID[item.ID] = item
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return prepareCatalog(ctx, tx, catalog) })
	var count int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_catalog WHERE item_id='new_coin'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("addition=%d err=%v", count, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range map[string]func() error{
		"prepare": func() error { return prepareCatalog(ctx, db, catalog) },
		"owner":   func() error { _, err := characterOwner(ctx, db, storeCharacterID); return err },
		"full":    func() error { _, _, err := readInventory(ctx, db, catalog, storeCharacterID); return err },
		"page":    func() error { _, err := readInventoryPage(ctx, db, catalog, storeCharacterID, 0); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, context.Canceled) {
				t.Fatalf("cause not preserved: %v", err)
			}
		})
	}
}

func TestStoreInitializedEmptySnapshot(t *testing.T) {
	db, catalog, _, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := seedStoreCharacter(ctx, tx); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO inventories VALUES(?,4,0)`, storeCharacterID); err != nil {
			return err
		}

		full, starter, err := readInventory(ctx, tx, catalog, storeCharacterID)
		if err != nil {
			return err
		}

		if starter || full.Revision != 4 || len(full.Items) != 0 || full.Weapon != "" || full.Armor != "" || full.Modifiers != (contract.Modifiers{}) {
			t.Fatalf("initialized empty=%+v starter=%v", full, starter)
		}

		page, err := readInventoryPage(ctx, tx, catalog, storeCharacterID, 1)
		if err != nil {
			return err
		}

		if page.Page != 0 || page.HasNext || !reflect.DeepEqual(page.Current, full) {
			t.Fatalf("initialized page=%+v", page)
		}

		return nil
	})
}
