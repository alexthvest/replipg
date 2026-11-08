package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

func (t *shopTransaction) Available(
	ctx context.Context,
	group core.GroupID,
	character, id core.ID,
) (inventory.Entry, bool, error) {
	entry, err := scanEntry(t.tx.QueryRowContext(
		ctx,
		entryQuery+" WHERE e.group_id=? AND e.character_id=? AND e.id=?",
		group,
		character,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return entry, false, nil
	}
	return entry, err == nil, err
}

func (t *shopTransaction) Reserve(ctx context.Context, order domain.Order) error {
	saved, exists, err := t.Order(ctx, order.ID)
	if err != nil {
		return err
	}
	if !exists || saved != order || order.Quote.Direction != domain.Sell || order.State != domain.Reserved {
		return errors.New("sale reservation requires its durable reserved order")
	}

	quote := order.Quote
	entry, found, err := t.Available(ctx, quote.Group, quote.Character, quote.Entry)
	if err != nil {
		return err
	}
	if !found || entry.Snapshot != quote.Snapshot || entry.Available() < quote.Quantity {
		return errors.New("sale quantity is not available in this inventory")
	}

	_, err = t.tx.ExecContext(
		ctx,
		`INSERT INTO inventory_reservations(order_id,group_id,character_id,entry_id,quantity) VALUES(?,?,?,?,?)`,
		order.ID,
		quote.Group,
		quote.Character,
		quote.Entry,
		quote.Quantity,
	)
	if err != nil {
		return fmt.Errorf("reserve sale inventory: %w", err)
	}
	return nil
}

func (t *shopTransaction) Release(ctx context.Context, id core.ID) error {
	_, err := t.tx.ExecContext(ctx, "DELETE FROM inventory_reservations WHERE order_id=?", id)
	return err
}

// Consume removes precisely the order's reservation after verified payout. The
// caller completes the order in this same transaction. A remaining entry is
// returned for projection; an empty result means the last unit was removed.
func (t *shopTransaction) Consume(
	ctx context.Context,
	order domain.Order,
) ([]inventory.Entry, error) {
	saved, exists, err := t.Order(ctx, order.ID)
	if err != nil {
		return nil, err
	}
	if !exists || saved != order || order.Quote.Direction != domain.Sell || order.State != domain.PayoutConfirmed {
		return nil, errors.New("sale consumption requires a durable confirmed payout")
	}

	var proof int
	if err = t.tx.QueryRowContext(ctx, "SELECT count(*) FROM financial_proofs WHERE order_id=?", order.ID).Scan(&proof); err != nil {
		return nil, err
	}
	if proof != 1 {
		return nil, errors.New("sale payout proof is missing")
	}

	quote := order.Quote
	var reserved int64
	err = t.tx.QueryRowContext(
		ctx,
		`SELECT quantity FROM inventory_reservations WHERE order_id=? AND group_id=? AND character_id=? AND entry_id=?`,
		order.ID,
		quote.Group,
		quote.Character,
		quote.Entry,
	).Scan(&reserved)
	if err != nil {
		return nil, fmt.Errorf("read sale reservation: %w", err)
	}
	if reserved != quote.Quantity {
		return nil, errors.New("sale reservation differs from frozen quantity")
	}

	entry, found, err := t.Available(ctx, quote.Group, quote.Character, quote.Entry)
	if err != nil {
		return nil, err
	}
	if !found || entry.Snapshot != quote.Snapshot || entry.Quantity < reserved {
		return nil, errors.New("sale reservation ownership mismatch")
	}

	if err = t.Release(ctx, order.ID); err != nil {
		return nil, err
	}
	entry.Quantity -= reserved
	entry.Reserved -= reserved
	if entry.Quantity == 0 {
		if _, err = t.tx.ExecContext(
			ctx,
			"DELETE FROM inventory_entries WHERE id=? AND group_id=? AND character_id=?",
			entry.ID,
			entry.Group,
			entry.Character,
		); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err = t.PutEntry(ctx, entry); err != nil {
		return nil, err
	}
	return []inventory.Entry{entry}, nil
}
