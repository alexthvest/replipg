package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
)

func findCharacter(
	ctx context.Context,
	q queryer,
	group core.GroupID,
	user core.UserID,
) (characters.Character, bool, error) {
	var c characters.Character
	err := q.QueryRowContext(
		ctx,
		"SELECT id,group_id,user_id,name FROM characters WHERE group_id=? AND user_id=?",
		group,
		user,
	).Scan(&c.ID, &c.Group, &c.User, &c.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func ensureCharacter(ctx context.Context, q queryer, c characters.Character) (characters.Character, error) {
	if _, err := q.ExecContext(ctx, "INSERT INTO groups(group_id) VALUES (?) ON CONFLICT DO NOTHING", c.Group); err != nil {
		return characters.Character{}, err
	}

	if _, err := q.ExecContext(
		ctx,
		"INSERT INTO characters(id,group_id,user_id,name) VALUES (?,?,?,?) ON CONFLICT(group_id,user_id) DO NOTHING",
		c.ID,
		c.Group,
		c.User,
		c.Name,
	); err != nil {
		return characters.Character{}, err
	}

	saved, _, err := findCharacter(ctx, q, c.Group, c.User)
	return saved, err
}

func (d *Database) FindCharacter(
	ctx context.Context,
	group core.GroupID,
	user core.UserID,
) (characters.Character, bool, error) {
	return findCharacter(ctx, d.db, group, user)
}

func (d *Database) EnsureCharacter(ctx context.Context, c characters.Character) (characters.Character, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return characters.Character{}, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	saved, err := ensureCharacter(ctx, tx, c)
	if err != nil {
		return characters.Character{}, err
	}

	if err = tx.Commit(); err != nil {
		return characters.Character{}, err
	}
	return saved, nil
}

func (d *Database) RenameCharacter(
	ctx context.Context,
	group core.GroupID,
	user core.UserID,
	name string,
) (characters.Character, error) {
	var c characters.Character
	err := d.db.QueryRowContext(
		ctx,
		"UPDATE characters SET name=? WHERE group_id=? AND user_id=? RETURNING id,group_id,user_id,name",
		name,
		group,
		user,
	).Scan(&c.ID, &c.Group, &c.User, &c.Name)
	return c, err
}

func (d *Database) LoadCharacters(ctx context.Context) ([]characters.Character, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT id,group_id,user_id,name FROM characters ORDER BY group_id,id")
	if err != nil {
		return nil, err
	}

	defer func() {
		_ = rows.Close()
	}()

	var result []characters.Character
	for rows.Next() {
		var c characters.Character
		if err = rows.Scan(&c.ID, &c.Group, &c.User, &c.Name); err != nil {
			return nil, err
		}

		result = append(result, c)
	}
	return result, rows.Err()
}
