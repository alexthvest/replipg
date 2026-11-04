package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Read storage directly: character Lookup could itself normalize due recovery.
func coordinatedInventoryState(t *testing.T, tx runtimecontract.DBTX) string {
	t.Helper()
	state := make(map[string][][]any)
	for _, table := range []string{"characters", "inventories", "inventory_items", "inventory_operations", "character_operations", "battle_state", "coordination_child"} {
		rows, err := tx.QueryContext(context.Background(), `SELECT * FROM `+table+` ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			targets := make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			state[table] = append(state[table], values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func createInventoryCoordinationFixture(t *testing.T, db *persistence.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `CREATE TABLE battle_state(id INTEGER PRIMARY KEY, turn INTEGER NOT NULL);
 INSERT INTO battle_state VALUES(1,0);
 CREATE TABLE coordination_parent(id INTEGER PRIMARY KEY);
 CREATE TABLE coordination_child(parent_id INTEGER REFERENCES coordination_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	if err != nil {
		t.Fatal(err)
	}
}

type coordinatedInventoryFault struct {
	runtimecontract.DBTX
	match   string
	after   bool
	cause   error
	inspect func()
	fired   bool
}

func (tx *coordinatedInventoryFault) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx.fired || !strings.HasPrefix(query, tx.match) {
		return tx.DBTX.ExecContext(ctx, query, args...)
	}
	tx.fired = true
	var result sql.Result
	if tx.after {
		var err error
		result, err = tx.DBTX.ExecContext(ctx, query, args...)
		if err != nil {
			return result, err
		}
	}
	tx.inspect()
	return result, tx.cause
}

func TestCoordinatedInventoryHealingOuterAction(t *testing.T) {
	for _, stage := range []string{"success", "before_character_receipt", "after_character_receipt", "before_inventory_receipt", "after_inventory_receipt", "release", "rollback", "outer_callback", "outer_commit"} {
		t.Run(stage, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			chars := inventoryCharacters(t, clock.Now)
			service := inventoryService(t, catalog, chars, clock.Now)
			createInventoryCoordinationFixture(t, db)
			var id charactercontract.ID
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}
				opened, err := service.Open(ctx, tx, 42)
				if err != nil {
					return err
				}
				id = opened.Current.CharacterID
				_, err = chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 30})
				return err
			})
			before := coordinatedInventoryState(t, db)
			injected := errors.New("injected coordinated action failure")
			request := contract.ConsumeRequest{OperationID: "battle:heal", CharacterID: id, ItemID: "healing_potion"}
			fail := stage != "success"
			var committed string
			run := func() error {
				return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
					if _, err := tx.ExecContext(ctx, `UPDATE battle_state SET turn=turn+1`); err != nil {
						return err
					}
					effectTx := tx
					var fault *coordinatedInventoryFault
					if fail {
						match := ""
						after := strings.HasPrefix(stage, "after_")
						switch stage {
						case "before_character_receipt", "after_character_receipt":
							match = "INSERT INTO character_operations"
						case "before_inventory_receipt", "after_inventory_receipt":
							match = "INSERT INTO inventory_operations"
						case "release":
							match = "RELEASE SAVEPOINT inventory_operation"
						case "rollback":
							match = "ROLLBACK TO SAVEPOINT inventory_operation"
						}
						if match != "" {
							fault = &coordinatedInventoryFault{DBTX: tx, match: match, after: after, cause: injected, inspect: func() {
								var health, characterReceipts, inventoryReceipts int
								if err := tx.QueryRowContext(ctx, `SELECT health,(SELECT count(*) FROM character_operations),(SELECT count(*) FROM inventory_operations) FROM characters WHERE id=?`, id).Scan(&health, &characterReceipts, &inventoryReceipts); err != nil {
									t.Fatal(err)
								}
								wantCharacter, wantInventory := 2, 1
								if stage == "before_character_receipt" {
									wantCharacter = 1
								}
								if stage == "after_inventory_receipt" || stage == "release" {
									wantInventory = 2
								}
								if health != 80 || characterReceipts != wantCharacter || inventoryReceipts != wantInventory {
									t.Fatalf("wrong tentative boundary: health=%d character receipts=%d inventory receipts=%d", health, characterReceipts, inventoryReceipts)
								}
								if got := coordinatedInventoryState(t, db); got != before {
									t.Fatalf("tentative write became durable: %s", got)
								}
							}}
							effectTx = fault
						}
						if stage == "rollback" {
							// Force a late domain rejection after the character has healed.
							if _, err := tx.ExecContext(ctx, `UPDATE inventories SET revision=? WHERE character_id=?`, int64(math.MaxInt64), id); err != nil {
								return err
							}
						}
					}
					result, err := service.ConsumeHealing(ctx, effectTx, request)
					if fault != nil && !fault.fired {
						t.Fatal("fault boundary was not reached")
					}
					if err != nil {
						var storage runtimecontract.StorageFailure
						if !errors.As(err, &storage) || !errors.Is(err, injected) || !reflect.DeepEqual(result, contract.ConsumeResult{}) {
							t.Fatalf("failed action result=%+v error=%v", result, err)
						}
						if stage == "rollback" && !errors.Is(err, contract.ErrExhausted) {
							t.Fatalf("cleanup lost domain failure: %v", err)
						}
						return err // The caller owns rollback of the preceding battle write.
					}
					if !result.Mutation.Applied || result.Character.Health != 80 {
						t.Fatalf("retry did not apply once: %+v", result)
					}
					qty, err := ownedQuantity(ctx, tx, id, "healing_potion")
					if err != nil {
						return err
					}
					if qty != 2 {
						t.Fatalf("quantity=%d", qty)
					}
					if got := coordinatedInventoryState(t, db); got != before {
						t.Fatal("service committed caller transaction")
					}
					if fail && stage == "outer_callback" {
						return injected
					}
					if fail && stage == "outer_commit" {
						if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_child VALUES(999)`); err != nil {
							return err
						}
					}
					committed = coordinatedInventoryState(t, tx)
					return nil
				})
			}
			err := run()
			if fail {
				var transaction *persistence.TransactionError
				if !errors.As(err, &transaction) {
					t.Fatalf("missing transaction failure: %v", err)
				}
				wantStage := persistence.TransactionStageCallback
				if stage == "outer_commit" {
					wantStage = persistence.TransactionStageCommit
				}
				if transaction.Stage != wantStage {
					t.Fatalf("stage=%s want=%s", transaction.Stage, wantStage)
				}
				if got := coordinatedInventoryState(t, db); got != before {
					t.Fatalf("partial action survived: %s\nwant %s", got, before)
				}
				fail = false
				err = run() // The same operation identity must remain available after rollback.
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := coordinatedInventoryState(t, db); got != committed {
				t.Fatalf("committed action differs: %s\nwant %s", got, committed)
			}
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				replay, err := service.ConsumeHealing(ctx, tx, request)
				if err == nil && (replay.Mutation.Applied || replay.Character.Health != 80) {
					t.Fatalf("replay=%+v", replay)
				}
				return err
			})
			if got := coordinatedInventoryState(t, db); got != committed {
				t.Fatal("replay changed coordinated state")
			}
		})
	}
}

func TestCoordinatedInventoryExpectedRejectionCommitsOnlyCallerState(t *testing.T) {
	for _, action := range []string{"healing", "starter"} {
		t.Run(action, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			chars := inventoryCharacters(t, clock.Now)
			service := inventoryService(t, catalog, chars, clock.Now)
			createInventoryCoordinationFixture(t, db)
			var defeated charactercontract.Snapshot
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}
				ch, _, err := chars.Ensure(ctx, tx, 42)
				if err != nil {
					return err
				}
				result, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "defeat", CharacterID: ch.ID, Kind: charactercontract.HealthDamage, Amount: 100})
				if err != nil {
					return err
				}
				defeated = result.Current
				if action == "healing" {
					if _, err := service.Open(ctx, tx, 42); err != nil {
						return err
					}
					_, err = tx.ExecContext(ctx, `UPDATE inventories SET revision=? WHERE character_id=?`, int64(math.MaxInt64), ch.ID)
					return err
				}
				if err := ensureInventoryRoot(ctx, tx, ch.ID); err != nil {
					return err
				}
				lines := catalog.Starter()
				return increaseQuantity(ctx, tx, ch.ID, lines[len(lines)-1].ItemID, math.MaxInt64)
			})
			clock.Advance(defeated.RecoverAt.Sub(clock.Now()))
			before := coordinatedInventoryState(t, db)
			var committed string
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				var err error
				if action == "healing" {
					_, err = service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "rejected", CharacterID: defeated.ID, ItemID: "healing_potion"})
				} else {
					_, err = service.Open(ctx, tx, 42)
				}
				var storage runtimecontract.StorageFailure
				if !errors.Is(err, contract.ErrExhausted) || errors.As(err, &storage) {
					t.Fatalf("expected safe rejection: %v", err)
				}
				if got := coordinatedInventoryState(t, tx); got != before {
					t.Fatalf("rejection leaked recovery/starter: %s\nwant %s", got, before)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE battle_state SET turn=1`); err != nil {
					return err
				}
				committed = coordinatedInventoryState(t, tx)
				return nil // Caller intentionally handles the rejection and commits.
			})
			if got := coordinatedInventoryState(t, db); got != committed {
				t.Fatal("handled rejection did not commit only caller state")
			}
		})
	}
}

func TestCoordinatedInventoryOrphanCharacterReplayAbortsCallerAction(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	createInventoryCoordinationFixture(t, db)
	var request contract.ConsumeRequest
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		id := opened.Current.CharacterID
		request = contract.ConsumeRequest{OperationID: "orphan", CharacterID: id, ItemID: "healing_potion"}
		if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 50}); err != nil {
			return err
		}
		_, err = chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: healingOperationID(request.OperationID), CharacterID: id, Kind: charactercontract.HealthHeal, Amount: 10})
		return err
	})
	before := coordinatedInventoryState(t, db)
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `UPDATE battle_state SET turn=1`); err != nil {
			return err
		}
		result, err := service.ConsumeHealing(ctx, tx, request)
		var storage runtimecontract.StorageFailure
		if !errors.As(err, &storage) || !reflect.DeepEqual(result, contract.ConsumeResult{}) {
			t.Fatalf("orphan receipt accepted: %+v %v", result, err)
		}
		return err
	})
	if err == nil {
		t.Fatal("invariant failure committed")
	}
	if got := coordinatedInventoryState(t, db); got != before {
		t.Fatal("orphan replay leaked cross-feature effects")
	}
}
