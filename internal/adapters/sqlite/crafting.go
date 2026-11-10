package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	application "github.com/alexthvest/replipg/internal/application/crafting"
	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type craftingTransaction struct {
	*grantTransaction
}

func (d *Database) WithinCrafting(ctx context.Context, fn func(application.Transaction) error) error {
	if fn == nil {
		return errors.New("crafting transaction callback required")
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin crafting transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	ports := &craftingTransaction{grantTransaction: &grantTransaction{
		tx:     tx,
		digest: items.Digest,
	}}
	if err = fn(application.Transaction{
		Repository: ports,
		Inventory:  ports,
	}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit crafting transaction: %w", err)
	}
	return nil
}

func scanRecipe(row scanner) (application.Recipe, error) {
	var r application.Recipe
	var ingredients, outcome []byte
	err := row.Scan(&r.ID, &r.Identity.Digest, &r.Identity.Canonical, &ingredients, &outcome)
	if err != nil {
		return r, err
	}

	if err = json.Unmarshal(ingredients, &r.Identity.Ingredients); err != nil {
		return r, err
	}
	if len(outcome) > 0 {
		if err = json.Unmarshal(outcome, &r.Outcome); err != nil {
			return r, fmt.Errorf("decode saved crafting outcome: %w", err)
		}
		err = r.Outcome.Validate()
	}
	return r, err
}

const recipeQuery = "SELECT id,digest,canonical,ingredients,outcome FROM crafting_recipes"

func (t *craftingTransaction) Recipe(ctx context.Context, identity domain.Identity) (application.Recipe, bool, error) {
	rows, err := t.tx.QueryContext(ctx, recipeQuery+" WHERE digest=?", identity.Digest)
	if err != nil {
		return application.Recipe{}, false, err
	}
	defer func() {
		_ = rows.Close()
	}()
	for rows.Next() {
		r, err := scanRecipe(rows)
		if err != nil {
			return r, false, err
		}
		if bytes.Equal(r.Identity.Canonical, identity.Canonical) {
			return r, true, nil
		}
	}
	return application.Recipe{}, false, rows.Err()
}

func (t *craftingTransaction) RecipeByID(ctx context.Context, id core.ID) (application.Recipe, error) {
	return scanRecipe(t.tx.QueryRowContext(ctx, recipeQuery+" WHERE id=?", id))
}

func (t *craftingTransaction) EnsureRecipe(ctx context.Context, r application.Recipe) (application.Recipe, error) {
	data, err := json.Marshal(r.Identity.Ingredients)
	if err != nil {
		return r, err
	}

	_, err = t.tx.ExecContext(ctx, "INSERT INTO crafting_recipes(id,digest,canonical,ingredients) VALUES(?,?,?,?) ON CONFLICT(canonical) DO NOTHING", r.ID, r.Identity.Digest, r.Identity.Canonical, data)
	if err != nil {
		return r, err
	}
	r, _, err = t.Recipe(ctx, r.Identity)
	if err != nil {
		return r, err
	}
	_, err = t.tx.ExecContext(ctx, "INSERT INTO crafting_jobs(recipe_id) VALUES(?) ON CONFLICT DO NOTHING", r.ID)
	if err == nil {
		_, err = t.tx.ExecContext(ctx, "UPDATE crafting_jobs SET state='pending',token='',expires=0 WHERE recipe_id=? AND state='failed'", r.ID)
	}
	return r, err
}

func (t *craftingTransaction) Attempt(ctx context.Context, token core.ID) (domain.Attempt, bool, error) {
	var data []byte
	err := t.tx.QueryRowContext(ctx, "SELECT result FROM crafting_attempts WHERE confirmation=?", token).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Attempt{}, false, nil
	}
	if err != nil {
		return domain.Attempt{}, false, err
	}

	var a domain.Attempt
	err = json.Unmarshal(data, &a)
	return a, true, err
}

func (t *craftingTransaction) SaveAttempt(ctx context.Context, a domain.Attempt) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO crafting_attempts(id,confirmation,recipe_id,group_id,character_id,user_id,state,intent,result) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(confirmation) DO UPDATE SET state=excluded.state,result=excluded.result WHERE crafting_attempts.state='pending'`, a.ID, a.Token, a.Recipe, a.Scope.Group, a.Character, a.Scope.Actor, a.State, data, data)
	return err
}

func (t *craftingTransaction) attempts(ctx context.Context, where string, args ...any) ([]domain.Attempt, error) {
	rows, err := t.tx.QueryContext(ctx, "SELECT result FROM crafting_attempts WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()
	var result []domain.Attempt
	for rows.Next() {
		var data []byte
		var a domain.Attempt
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (t *craftingTransaction) Pending(ctx context.Context) ([]domain.Attempt, error) {
	return t.attempts(ctx, "state='pending'")
}

func (t *craftingTransaction) Affected(ctx context.Context, recipe core.ID) ([]domain.Attempt, error) {
	return t.attempts(ctx, "recipe_id=?", recipe)
}

func (t *craftingTransaction) Claim(ctx context.Context, now, expires time.Time, token core.ID) (application.Claim, bool, error) {
	var id core.ID
	err := t.tx.QueryRowContext(ctx, `SELECT j.recipe_id FROM crafting_jobs j JOIN crafting_recipes r ON r.id=j.recipe_id
 WHERE r.outcome IS NULL AND (j.state='pending' OR (j.state='claimed' AND j.expires<=?))
 AND EXISTS(SELECT 1 FROM crafting_attempts a WHERE a.recipe_id=j.recipe_id AND a.state='pending') ORDER BY j.rowid LIMIT 1`, now.UnixNano()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return application.Claim{}, false, nil
	}
	if err != nil {
		return application.Claim{}, false, err
	}
	_, err = t.tx.ExecContext(ctx, "UPDATE crafting_jobs SET token=?,expires=?,state='claimed' WHERE recipe_id=?", token, expires.UnixNano(), id)
	if err != nil {
		return application.Claim{}, false, err
	}
	r, err := t.RecipeByID(ctx, id)
	return application.Claim{
		Recipe:      id,
		Token:       token,
		Ingredients: r.Identity.Ingredients,
		Expires:     expires,
	}, err == nil, err
}

func (t *craftingTransaction) validClaim(ctx context.Context, c application.Claim) (bool, error) {
	var n int
	err := t.tx.QueryRowContext(ctx, "SELECT count(*) FROM crafting_jobs WHERE recipe_id=? AND token=? AND state='claimed'", c.Recipe, c.Token).Scan(&n)
	return n == 1, err
}

func (t *craftingTransaction) Publish(ctx context.Context, c application.Claim, o domain.Outcome) (bool, error) {
	valid, err := t.validClaim(ctx, c)
	if err != nil || !valid {
		return false, err
	}
	if err = o.Validate(); err != nil {
		return false, err
	}
	data, err := json.Marshal(o)
	if err != nil {
		return false, err
	}
	_, err = t.tx.ExecContext(ctx, "UPDATE crafting_recipes SET outcome=? WHERE id=? AND outcome IS NULL", data, c.Recipe)
	if err != nil {
		return false, err
	}
	_, err = t.tx.ExecContext(ctx, "UPDATE crafting_jobs SET state='done' WHERE recipe_id=? AND token=?", c.Recipe, c.Token)
	return err == nil, err
}

func (t *craftingTransaction) FailClaim(ctx context.Context, c application.Claim) (bool, error) {
	valid, err := t.validClaim(ctx, c)
	if err != nil || !valid {
		return false, err
	}
	_, err = t.tx.ExecContext(ctx, "UPDATE crafting_jobs SET state='failed' WHERE recipe_id=? AND token=?", c.Recipe, c.Token)
	if err != nil {
		return false, err
	}
	attempts, err := t.attempts(ctx, "recipe_id=? AND state='pending'", c.Recipe)
	if err != nil {
		return false, err
	}
	for _, a := range attempts {
		a.State = domain.TechnicalFailure
		if err = t.SaveAttempt(ctx, a); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (t *craftingTransaction) Validate(ctx context.Context, a domain.Attempt) error {
	character, found, err := findCharacter(ctx, t.tx, a.Scope.Group, a.Scope.Actor)
	if err != nil {
		return err
	}
	if !found || character.ID != a.Character {
		return domain.ErrUnavailable
	}

	entries := make([]inventory.Entry, 0, len(a.Allocations))
	for _, allocation := range a.Allocations {
		entry, err := scanEntry(t.tx.QueryRowContext(ctx, entryQuery+" WHERE e.id=? AND e.group_id=? AND e.character_id=?", allocation.Entry, a.Scope.Group, a.Character))
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrUnavailable
		}
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	return domain.ValidateAllocations(a.Scope.Group, a.Character, a.Allocations, entries)
}

func (t *craftingTransaction) Consume(ctx context.Context, a domain.Attempt) error {
	if err := t.Validate(ctx, a); err != nil {
		return err
	}

	for _, allocation := range a.Allocations {
		if _, err := t.tx.ExecContext(ctx, "DELETE FROM inventory_entries WHERE id=? AND quantity=? AND group_id=? AND character_id=?", allocation.Entry, allocation.Quantity, a.Scope.Group, a.Character); err != nil {
			return err
		}
		if _, err := t.tx.ExecContext(ctx, "UPDATE inventory_entries SET quantity=quantity-? WHERE id=? AND group_id=? AND character_id=?", allocation.Quantity, allocation.Entry, a.Scope.Group, a.Character); err != nil {
			return err
		}
	}
	return nil
}
