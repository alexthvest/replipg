package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestGrantCanonicalReplayAndPendingStarter(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var req contract.GrantRequest
	var first contract.MutationResult

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		ch, _, err := chars.Ensure(ctx, tx, 61)
		if err != nil {
			return err
		}

		req = contract.GrantRequest{OperationID: "reward:canonical", CharacterID: ch.ID, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 2}, {ItemID: "stick", Quantity: 1}, {ItemID: "coin", Quantity: 3}}}
		first, err = service.Grant(ctx, tx, req)
		if err != nil {
			return err
		}

		if !first.Applied || first.AppliedRevision != 2 || first.Current.Revision != 2 {
			t.Fatalf("first: %+v", first)
		}

		assertGrantQuantity(t, first.Current, "coin", 5)
		assertGrantQuantity(t, first.Current, "stick", 1)
		_, starter, err := readInventory(ctx, tx, catalog, ch.ID)
		if err != nil {
			return err
		}

		if starter {
			t.Fatal("reward consumed starter entitlement")
		}

		// Later changes survive replay; its application revision remains the original one.
		opened, err := service.Open(ctx, tx, 61)
		if err != nil {
			return err
		}

		if !opened.StarterGranted {
			t.Fatal("missing starter entitlement")
		}

		replay := req
		replay.Items = []contract.GrantLine{{ItemID: "stick", Quantity: 1}, {ItemID: "coin", Quantity: 5}}
		again, err := service.Grant(ctx, tx, replay)
		if err != nil {
			return err
		}

		if again.Applied || again.AppliedRevision != first.AppliedRevision || !reflect.DeepEqual(again.Current, opened.Current) {
			t.Fatalf("replay: %+v", again)
		}

		return nil
	})
}

func assertGrantQuantity(t *testing.T, snapshot contract.Snapshot, id contract.ItemID, want int64) {
	t.Helper()
	for _, item := range snapshot.Items {
		if item.ItemID == id {
			if item.Quantity != want {
				t.Fatalf("%s quantity=%d want=%d", id, item.Quantity, want)
			}

			return
		}
	}

	t.Fatalf("missing %s", id)
}

func TestGrantRejectsAtomicallyWithOuterCommit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lines    []contract.GrantLine
		want     error
		existing bool
		revision bool
	}{
		{name: "empty", want: contract.ErrInvalidInput},
		{name: "invalid trailing", lines: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "stick", Quantity: 0}}, want: contract.ErrInvalidInput},
		{name: "negative trailing", lines: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "stick", Quantity: -1}}, want: contract.ErrInvalidInput},
		{name: "unknown trailing", lines: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "unknown", Quantity: 1}}, want: contract.ErrNotFound},
		{name: "aggregate overflow", lines: []contract.GrantLine{{ItemID: "coin", Quantity: math.MaxInt64}, {ItemID: "coin", Quantity: 1}}, want: contract.ErrExhausted},
		{name: "owned overflow trailing", lines: []contract.GrantLine{{ItemID: "cloth_armor", Quantity: 1}, {ItemID: "coin", Quantity: 1}}, want: contract.ErrExhausted, existing: true},
		{name: "revision overflow", lines: []contract.GrantLine{{ItemID: "stick", Quantity: 1}}, want: contract.ErrExhausted, revision: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			chars := inventoryCharacters(t, clock.Now)
			service := inventoryService(t, catalog, chars, clock.Now)
			var id charactercontract.ID
			var before contract.Snapshot

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}

				ch, _, err := chars.Ensure(ctx, tx, 62)
				if err != nil {
					return err
				}

				id = ch.ID
				if tc.existing || tc.revision {
					if err := ensureInventoryRoot(ctx, tx, id); err != nil {
						return err
					}
				}
				if tc.existing {
					if err := increaseQuantity(ctx, tx, id, "coin", math.MaxInt64); err != nil {
						return err
					}
				}
				if tc.revision {
					if _, err := tx.ExecContext(ctx, `UPDATE inventories SET revision=? WHERE character_id=?`, math.MaxInt64, id); err != nil {
						return err
					}
				}
				before, err = service.Inspect(ctx, tx, id)
				return err
			})
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: "reward:rejected", CharacterID: id, Items: tc.lines})
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(result, contract.MutationResult{}) {
					t.Fatalf("result=%+v error=%v", result, err)
				}

				return nil // The caller handles the error and commits its outer transaction.
			})
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				after, err := service.Inspect(ctx, tx, id)
				if err != nil {
					return err
				}

				if !reflect.DeepEqual(before, after) {
					t.Fatalf("partial write: before=%+v after=%+v", before, after)
				}

				var count int

				return queryInventoryRows(ctx, tx, `SELECT count(*) FROM inventory_operations`, nil, func(rows *sql.Rows) error {
					if err := rows.Scan(&count); err != nil {
						return err
					}

					if count != 0 {
						t.Fatalf("partial receipt: %d", count)
					}

					return nil
				})
			})
		})
	}
}

func TestGrantMissingCharacterAndInvalidTransaction(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	req := contract.GrantRequest{OperationID: "reward:missing", CharacterID: "missing", Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}}}
	if _, err := service.Grant(context.Background(), nil, req); !errors.Is(err, contract.ErrInvalidInput) {
		t.Fatalf("nil tx: %v", err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		if _, err := service.Grant(ctx, tx, req); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}

		for _, table := range []string{"characters", "inventories", "inventory_items", "inventory_operations"} {
			var count int

			if err := queryInventoryRows(ctx, tx, `SELECT count(*) FROM `+table, nil, func(rows *sql.Rows) error { return rows.Scan(&count) }); err != nil {
				return err
			}

			if count != 0 {
				t.Fatalf("created %s", table)
			}
		}
		return nil
	})
}

func TestGrantConflictPrecedesCurrentState(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		ch, _, err := chars.Ensure(ctx, tx, 63)
		if err != nil {
			return err
		}

		req := contract.GrantRequest{OperationID: "reward:conflict", CharacterID: ch.ID, Items: []contract.GrantLine{{ItemID: "coin", Quantity: math.MaxInt64}}}
		if _, err := service.Grant(ctx, tx, req); err != nil {
			return err
		}

		for _, changed := range []contract.GrantRequest{
			{OperationID: req.OperationID, CharacterID: ch.ID, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}}},
			{OperationID: req.OperationID, CharacterID: ch.ID, Items: []contract.GrantLine{{ItemID: "unknown", Quantity: 1}}},
			{OperationID: req.OperationID, CharacterID: "missing", Items: req.Items},
		} {
			if _, err := service.Grant(ctx, tx, changed); !errors.Is(err, contract.ErrOperationConflict) {
				t.Fatalf("conflict: %v", err)
			}
		}

		replay, err := service.Grant(ctx, tx, req)
		if err == nil && (replay.Applied || replay.AppliedRevision != 2) {
			t.Fatalf("overflowing replay: %+v", replay)
		}

		return err
	})
}

func TestGrantConcurrentReplayAndRestart(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var id charactercontract.ID

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		ch, _, err := chars.Ensure(ctx, tx, 64)
		id = ch.ID
		return err
	})
	var wg sync.WaitGroup

	errs := make(chan error, 12)
	results := make(chan contract.MutationResult, 12)
	for i := range 12 {
		wg.Go(func() {
			errs <- db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				r, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: contract.OperationID(fmt.Sprintf("reward:concurrent:%d", i%4)), CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 3}}})
				if err == nil {
					results <- r
				}
				return err
			})
		})
	}

	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	applied := 0
	for result := range results {
		if result.Applied {
			applied++
		}
	}

	if applied != 4 {
		t.Fatalf("applications=%d", applied)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openInventoryDB(t, dir)
	service = inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, reopened, func(ctx context.Context, tx runtimecontract.DBTX) error {
		replay, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: "reward:concurrent:0", CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}, {ItemID: "coin", Quantity: 2}}})
		if err != nil {
			return err
		}

		if replay.Applied || replay.Current.Revision != 5 || replay.AppliedRevision < 2 || replay.AppliedRevision > 5 {
			t.Fatalf("restart: %+v", replay)
		}

		assertGrantQuantity(t, replay.Current, "coin", 12)
		return nil
	})
}

func TestGrantLateStorageFailureAndOuterRollback(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var id charactercontract.ID

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		ch, _, err := chars.Ensure(ctx, tx, 65)
		id = ch.ID
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `CREATE TRIGGER reject_grant BEFORE INSERT ON inventory_operations WHEN NEW.kind='grant' BEGIN SELECT RAISE(ABORT,'test grant failure'); END`)
		return err
	})
	req := contract.GrantRequest{OperationID: "reward:storage", CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 7}, {ItemID: "stick", Quantity: 2}}}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := service.Grant(ctx, tx, req)
		var failure runtimecontract.StorageFailure

		if !errors.As(err, &failure) || !reflect.DeepEqual(result, contract.MutationResult{}) {
			t.Fatalf("storage failure: %+v %v", result, err)
		}

		return nil // Even a caller committing after storage failure cannot retain partial writes.
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		current, err := service.Inspect(ctx, tx, id)
		if err != nil {
			return err
		}

		if current.Revision != 0 || len(current.Items) != 0 {
			t.Fatalf("partial write: %+v", current)
		}

		_, err = tx.ExecContext(ctx, `DROP TRIGGER reject_grant`)
		return err
	})
	aborted := errors.New("caller abort")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := service.Grant(ctx, tx, req); err != nil {
			return err
		}

		return aborted
	})
	if !errors.Is(err, aborted) {
		t.Fatalf("rollback: %v", err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := service.Grant(ctx, tx, req)
		if err != nil {
			return err
		}

		if !result.Applied || result.AppliedRevision != 2 {
			t.Fatalf("aborted grant retained receipt: %+v", result)
		}

		assertGrantQuantity(t, result.Current, "coin", 7)
		assertGrantQuantity(t, result.Current, "stick", 2)
		return nil
	})
}

func TestGrantPreservesEquippedCopies(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 66)
		if err != nil {
			return err
		}

		id := opened.Current.CharacterID
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_equipment(character_id,slot,item_id) VALUES(?,'weapon','stick')`, id); err != nil {
			return err
		}

		granted, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: "reward:equipped", CharacterID: id, Items: []contract.GrantLine{{ItemID: "stick", Quantity: 2}, {ItemID: "coin", Quantity: 4}}})
		if err != nil {
			return err
		}

		if granted.Current.Weapon != "stick" || granted.Current.Modifiers.Attack != 1 {
			t.Fatalf("equipment changed: %+v", granted)
		}

		assertGrantQuantity(t, granted.Current, "stick", 3)
		return nil
	})
}
