package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/groups"
)

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readGroup(ctx context.Context, q queryer, id core.GroupID) (groups.State, error) {
	state := groups.State{
		ID:       id,
		Language: "en",
	}
	if err := q.QueryRowContext(ctx, "SELECT language FROM groups WHERE group_id=?", id).Scan(&state.Language); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return state, err
	}

	rows, err := q.QueryContext(ctx, "SELECT user_id FROM game_admins WHERE group_id=? ORDER BY user_id", id)
	if err != nil {
		return state, err
	}

	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var user core.UserID
		if err = rows.Scan(&user); err != nil {
			return state, err
		}

		state.Admins = append(state.Admins, user)
	}
	return state, rows.Err()
}

func (d *Database) Group(ctx context.Context, id core.GroupID) (groups.State, error) {
	return readGroup(ctx, d.db, id)
}

func (d *Database) LoadGroups(ctx context.Context) ([]groups.State, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT group_id FROM groups ORDER BY group_id")
	if err != nil {
		return nil, err
	}

	var ids []core.GroupID

	for rows.Next() {
		var id core.GroupID
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}

		ids = append(ids, id)
	}

	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}

	var states []groups.State

	for _, id := range ids {
		state, err := d.Group(ctx, id)
		if err != nil {
			return nil, err
		}

		states = append(states, state)
	}
	return states, nil
}

func (d *Database) updateGroup(ctx context.Context, id core.GroupID, mutate func(*sql.Tx) error) (groups.State, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return groups.State{}, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.ExecContext(ctx, "INSERT INTO groups(group_id) VALUES (?) ON CONFLICT DO NOTHING", id); err != nil {
		return groups.State{}, err
	}

	if err = mutate(tx); err != nil {
		return groups.State{}, err
	}

	state, err := readGroup(ctx, tx, id)
	if err != nil {
		return state, err
	}

	if err = tx.Commit(); err != nil {
		return groups.State{}, fmt.Errorf("commit group: %w", err)
	}
	return state, nil
}

func (d *Database) SetAdmin(ctx context.Context, id core.GroupID, user core.UserID, add bool) (groups.State, error) {
	return d.updateGroup(ctx, id, func(tx *sql.Tx) error {
		query := "DELETE FROM game_admins WHERE group_id=? AND user_id=?"
		if add {
			query = "INSERT INTO game_admins(group_id,user_id) VALUES (?,?) ON CONFLICT DO NOTHING"
		}

		_, err := tx.ExecContext(ctx, query, id, user)
		return err
	})
}

func (d *Database) SetLanguage(ctx context.Context, id core.GroupID, language string) (groups.State, error) {
	return d.updateGroup(ctx, id, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE groups SET language=? WHERE group_id=?", language, id)
		return err
	})
}
