package inventory

import (
	"context"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// The caller owns validation, revision updates and transaction completion.
// Schema constraints keep every assignment compatible and backed by ownership.
func writeEquipment(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID, slot contract.Slot, itemID itemcontract.ItemID) error {
	var err error

	if itemID == "" {
		_, err = tx.ExecContext(ctx, `DELETE FROM inventory_equipment WHERE character_id=? AND slot=?`, id, slot)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO inventory_equipment(character_id,slot,item_id) VALUES(?,?,?) ON CONFLICT(character_id,slot) DO UPDATE SET item_id=excluded.item_id`, id, slot, itemID)
	}
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}
