package inventory

import (
	"context"
	"database/sql"
	"errors"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Preparation validates every saved definition before registering additions.
// The caller owns the transaction and rolls it back on any storage failure.
func prepareCatalog(ctx context.Context, tx runtimecontract.DBTX, catalog Catalog) error {
	compatible := true
	err := queryInventoryRows(ctx, tx, `SELECT item_id,category,attack_bonus,defense_bonus,healing_hp FROM inventory_catalog`, nil, func(rows *sql.Rows) error {
		var saved ItemDefinition
		if err := rows.Scan(&saved.ID, &saved.Category, &saved.AttackBonus, &saved.DefenseBonus, &saved.HealingHP); err != nil {
			return err
		}
		current, exists := catalog.Lookup(saved.ID)
		if !exists || saved.Category != current.Category || saved.AttackBonus != current.AttackBonus || saved.DefenseBonus != current.DefenseBonus || saved.HealingHP != current.HealingHP {
			compatible = false
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !compatible {
		return invalidCatalog("mechanics")
	}
	for _, item := range catalog.Items() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_catalog(item_id,category,attack_bonus,defense_bonus,healing_hp) VALUES(?,?,?,?,?) ON CONFLICT(item_id) DO NOTHING`, item.ID, item.Category, item.AttackBonus, item.DefenseBonus, item.HealingHP); err != nil {
			return contract.NewStorageError(err)
		}
	}
	return nil
}

// Identity resolution deliberately reads no character health or recovery state.
func characterOwner(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID) (runtimecontract.UserID, error) {
	var owner runtimecontract.UserID
	found := false
	err := queryInventoryRows(ctx, tx, `SELECT id,user_id FROM characters WHERE id=?`, []any{id}, func(rows *sql.Rows) error {
		var actual charactercontract.ID
		if err := rows.Scan(&actual, &owner); err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, &contract.DomainError{Kind: contract.ErrorNotFound}
	}
	return owner, nil
}

func readInventory(ctx context.Context, tx runtimecontract.DBTX, catalog Catalog, id charactercontract.ID) (contract.Snapshot, bool, error) {
	snapshot, starter, err := readInventoryMetadata(ctx, tx, catalog, id)
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	snapshot.Items, err = readInventoryItems(ctx, tx, id, `SELECT item_id,quantity FROM inventory_items WHERE character_id=? AND quantity>0 ORDER BY item_id`, nil)
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	return snapshot, starter, nil
}

func readInventoryPage(ctx context.Context, tx runtimecontract.DBTX, catalog Catalog, id charactercontract.ID, page int) (inventoryPage, error) {
	snapshot, _, err := readInventoryMetadata(ctx, tx, catalog, id)
	if err != nil {
		return inventoryPage{}, err
	}
	var count int64
	err = queryInventoryRows(ctx, tx, `SELECT count(*) FROM inventory_items WHERE character_id=? AND quantity>0`, []any{id}, func(rows *sql.Rows) error { return rows.Scan(&count) })
	if err != nil {
		return inventoryPage{}, err
	}
	last := int64(0)
	if count > 0 {
		last = (count - 1) / inventoryPageSize
	}
	if page < 0 {
		page = 0
	}
	if int64(page) > last {
		page = int(last)
	}
	snapshot.Items, err = readInventoryItems(ctx, tx, id, `SELECT item_id,quantity FROM inventory_items WHERE character_id=? AND quantity>0 ORDER BY item_id LIMIT ? OFFSET ?`, []any{inventoryPageSize, int64(page) * inventoryPageSize})
	if err != nil {
		return inventoryPage{}, err
	}
	return inventoryPage{Current: snapshot, Page: page, HasNext: int64(page) < last}, nil
}

func readInventoryMetadata(ctx context.Context, tx runtimecontract.DBTX, catalog Catalog, id charactercontract.ID) (contract.Snapshot, bool, error) {
	if _, err := characterOwner(ctx, tx, id); err != nil {
		return contract.Snapshot{}, false, err
	}
	snapshot := contract.Snapshot{CharacterID: id}
	var starter bool
	if err := queryInventoryRows(ctx, tx, `SELECT revision,starter_granted FROM inventories WHERE character_id=?`, []any{id}, func(rows *sql.Rows) error { return rows.Scan(&snapshot.Revision, &starter) }); err != nil {
		return contract.Snapshot{}, false, err
	}
	if snapshot.Revision == 0 {
		return snapshot, false, nil
	}
	invalidEquipment := false
	err := queryInventoryRows(ctx, tx, `SELECT slot,item_id FROM inventory_equipment WHERE character_id=? ORDER BY slot`, []any{id}, func(rows *sql.Rows) error {
		var slot contract.Slot
		var itemID contract.ItemID
		if err := rows.Scan(&slot, &itemID); err != nil {
			return err
		}
		item, ok := catalog.Lookup(itemID)
		if !ok || string(item.Category) != string(slot) {
			invalidEquipment = true
			return nil
		}
		switch slot {
		case contract.SlotWeapon:
			snapshot.Weapon = itemID
			snapshot.Modifiers.Attack = item.AttackBonus
		case contract.SlotArmor:
			snapshot.Armor = itemID
			snapshot.Modifiers.Defense = item.DefenseBonus
		}
		return nil
	})
	if err != nil {
		return contract.Snapshot{}, false, err
	}
	if invalidEquipment {
		return contract.Snapshot{}, false, invalidCatalog("mechanics")
	}
	return snapshot, starter, nil
}

func readInventoryItems(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, query string, extra []any) ([]contract.OwnedItem, error) {
	var items []contract.OwnedItem
	args := append([]any{id}, extra...)
	err := queryInventoryRows(ctx, tx, query, args, func(rows *sql.Rows) error {
		var item contract.OwnedItem
		if err := rows.Scan(&item.ItemID, &item.Quantity); err != nil {
			return err
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// Keep close, iteration and scan failures visible to the transaction owner.
func queryInventoryRows(ctx context.Context, tx runtimecontract.DBTX, query string, args []any, scan func(*sql.Rows) error) (err error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return contract.NewStorageError(err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = contract.NewStorageError(errors.Join(err, closeErr))
		}
	}()
	for rows.Next() {
		if scanErr := scan(rows); scanErr != nil {
			return contract.NewStorageError(scanErr)
		}
	}
	if rowErr := rows.Err(); rowErr != nil {
		return contract.NewStorageError(rowErr)
	}
	return nil
}
