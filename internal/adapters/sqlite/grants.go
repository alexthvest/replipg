package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type grantTransaction struct {
	tx     *sql.Tx
	digest func([]byte) string
}

func (d *Database) WithinGrant(ctx context.Context, fn func(grants.Transaction) error) error {
	return d.withinGrant(ctx, fn, items.Digest)
}

func (d *Database) withinGrant(
	ctx context.Context,
	fn func(
		grants.Transaction,
	) error,
	digest func(
		[]byte,
	) string,
) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	ports := &grantTransaction{
		tx:     tx,
		digest: digest,
	}
	if err = fn(grants.Transaction{
		Characters: ports,
		Inventory:  ports,
		Operations: ports,
	}); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit grant: %w", err)
	}
	return nil
}

func (t *grantTransaction) FindCharacter(
	ctx context.Context,
	g core.GroupID,
	u core.UserID,
) (characters.Character, bool, error) {
	return findCharacter(ctx, t.tx, g, u)
}

func (t *grantTransaction) EnsureCharacter(ctx context.Context, c characters.Character) (characters.Character, error) {
	return ensureCharacter(ctx, t.tx, c)
}

func (t *grantTransaction) Operation(ctx context.Context, key grants.Key) (grants.Result, bool, error) {
	var data []byte
	err := t.tx.QueryRowContext(
		ctx,
		"SELECT result_json FROM grant_operations WHERE bot_id=? AND update_id=?",
		key.BotID,
		key.UpdateID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return grants.Result{}, false, nil
	}
	if err != nil {
		return grants.Result{}, false, err
	}

	var result grants.Result
	if err = json.Unmarshal(data, &result); err != nil {
		return grants.Result{}, false, fmt.Errorf("decode saved grant: %w", err)
	}
	return result, true, nil
}

func (t *grantTransaction) Record(ctx context.Context, key grants.Key, result grants.Result) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	_, err = t.tx.ExecContext(
		ctx,
		"INSERT INTO grant_operations(bot_id,update_id,group_id,result_json) VALUES (?,?,?,?)",
		key.BotID,
		key.UpdateID,
		result.Group,
		data,
	)
	return err
}

func (t *grantTransaction) ReuseSnapshot(ctx context.Context, snapshot items.Snapshot) (core.ID, error) {
	if err := snapshot.Validate(); err != nil {
		return "", err
	}

	data, err := snapshot.Canonical()
	if err != nil {
		return "", err
	}

	digest := t.digest(data)
	rows, err := t.tx.QueryContext(ctx, "SELECT id,canonical_json FROM item_snapshots WHERE digest=?", digest)
	if err != nil {
		return "", err
	}

	var found core.ID
	for rows.Next() {
		var id core.ID
		var saved []byte
		if err = rows.Scan(&id, &saved); err != nil {
			_ = rows.Close()
			return "", err
		}

		if bytes.Equal(data, saved) {
			found = id
			break
		}
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return "", err
	}
	if found != "" {
		return found, nil
	}

	id, err := core.NewID()
	if err != nil {
		return "", err
	}

	if _, err = t.tx.ExecContext(
		ctx,
		"INSERT INTO item_snapshots(id,catalog_id,canonical_json,digest) VALUES (?,?,?,?)",
		id,
		snapshot.CatalogID,
		data,
		digest,
	); err != nil {
		return "", err
	}
	return id, nil
}

func (t *grantTransaction) Stack(
	ctx context.Context,
	group core.GroupID,
	character, snapshot core.ID,
) (inventory.Entry, bool, error) {
	entry, err := scanEntry(t.tx.QueryRowContext(
		ctx,
		entryQuery+" WHERE e.group_id=? AND e.character_id=? AND e.snapshot_id=? AND e.mode='stackable'",
		group,
		character,
		snapshot,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return entry, false, nil
	}
	return entry, err == nil, err
}

func (t *grantTransaction) PutEntry(ctx context.Context, entry inventory.Entry) error {
	result, err := t.tx.ExecContext(ctx, `INSERT INTO inventory_entries(id,group_id,character_id,snapshot_id,mode,quantity) VALUES(?,?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET quantity=excluded.quantity
 WHERE inventory_entries.group_id=excluded.group_id AND inventory_entries.character_id=excluded.character_id
 AND inventory_entries.snapshot_id=excluded.snapshot_id AND inventory_entries.mode=excluded.mode`, entry.ID, entry.Group, entry.Character, entry.SnapshotID, entry.Snapshot.Mode, entry.Quantity)
	if err != nil {
		return err
	}

	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("foreign inventory entry update")
	}
	return nil
}
