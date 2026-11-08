package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

const entryQuery = `SELECT e.id,e.group_id,e.character_id,e.snapshot_id,e.quantity,e.mode,s.canonical_json,
 COALESCE((SELECT SUM(r.quantity) FROM inventory_reservations r WHERE r.entry_id=e.id),0)
 FROM inventory_entries e JOIN item_snapshots s ON s.id=e.snapshot_id`

type scanner interface{ Scan(...any) error }

func scanEntry(row scanner) (inventory.Entry, error) {
	var entry inventory.Entry
	var data []byte
	var mode items.Mode
	if err := row.Scan(
		&entry.ID,
		&entry.Group,
		&entry.Character,
		&entry.SnapshotID,
		&entry.Quantity,
		&mode,
		&data,
		&entry.Reserved,
	); err != nil {
		return entry, err
	}

	if err := json.Unmarshal(data, &entry.Snapshot); err != nil {
		return entry, err
	}

	if err := entry.Snapshot.Validate(); err != nil {
		return entry, err
	}
	if entry.Snapshot.Mode != mode {
		return entry, errors.New("stored snapshot ownership mode mismatch")
	}
	if entry.Reserved < 0 || entry.Reserved > entry.Quantity {
		return entry, errors.New("stored inventory reservation exceeds ownership")
	}
	return entry, nil
}

func (d *Database) Entries(ctx context.Context, group core.GroupID, character core.ID) ([]inventory.Entry, error) {
	// Row IDs preserve acquisition order; updating a stack keeps its original position.
	rows, err := d.db.QueryContext(
		ctx,
		entryQuery+" WHERE e.group_id=? AND e.character_id=? ORDER BY e.rowid",
		group,
		character,
	)
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = rows.Close()
	}()

	var entries []inventory.Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (d *Database) Entry(
	ctx context.Context,
	group core.GroupID,
	character, id core.ID,
) (inventory.Entry, bool, error) {
	entry, err := scanEntry(d.db.QueryRowContext(
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

func (d *Database) LoadInventory(ctx context.Context) ([]inventory.Entry, error) {
	rows, err := d.db.QueryContext(ctx, entryQuery+" ORDER BY e.group_id,e.rowid")
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = rows.Close()
	}()

	var entries []inventory.Entry
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
