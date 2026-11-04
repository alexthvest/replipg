package inventory

import (
	"context"
	"database/sql"
	"math"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func ensureInventoryRoot(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO inventories(character_id,revision,starter_granted) VALUES(?,1,0) ON CONFLICT(character_id) DO NOTHING`, id)
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}
func ownedQuantity(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, item contract.ItemID) (int64, error) {
	var quantity int64

	err := queryInventoryRows(ctx, tx, `SELECT quantity FROM inventory_items WHERE character_id=? AND item_id=?`, []any{id, item}, func(rows *sql.Rows) error { return rows.Scan(&quantity) })
	return quantity, err
}
func increaseQuantity(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, item contract.ItemID, quantity int64) error {
	if quantity <= 0 {
		return contract.ErrInvalidInput
	}

	old, err := ownedQuantity(ctx, tx, id, item)
	if err != nil {
		return err
	}

	if old > math.MaxInt64-quantity {
		return contract.ErrExhausted
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_items(character_id,item_id,quantity) VALUES(?,?,?) ON CONFLICT(character_id,item_id) DO UPDATE SET quantity=excluded.quantity`, id, item, old+quantity)
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}
func decreaseQuantity(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, item contract.ItemID, quantity int64) error {
	if quantity <= 0 {
		return contract.ErrInvalidInput
	}

	old, err := ownedQuantity(ctx, tx, id, item)
	if err != nil {
		return err
	}

	if old < quantity {
		return contract.ErrNotOwned
	}

	if old == quantity {
		_, err = tx.ExecContext(ctx, `DELETE FROM inventory_items WHERE character_id=? AND item_id=?`, id, item)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_items SET quantity=? WHERE character_id=? AND item_id=?`, old-quantity, id, item)
	}
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}

func advanceInventoryRevision(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, expected int64) error {
	if expected < 1 {
		return contract.ErrInvalidInput
	}

	if expected == math.MaxInt64 {
		return contract.ErrExhausted
	}

	result, err := tx.ExecContext(ctx, `UPDATE inventories SET revision=revision+1 WHERE character_id=? AND revision=?`, id, expected)
	if err != nil {
		return contract.NewStorageError(err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return contract.NewStorageError(err)
	}

	if affected != 1 {
		return contract.ErrStaleControl
	}

	return nil
}

func markStarterGranted(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID) error {
	result, err := tx.ExecContext(ctx, `UPDATE inventories SET starter_granted=1 WHERE character_id=? AND starter_granted=0`, id)
	if err != nil {
		return contract.NewStorageError(err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return contract.NewStorageError(err)
	}

	if affected != 1 {
		return contract.ErrOperationConflict
	}

	return nil
}
