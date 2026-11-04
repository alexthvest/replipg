package character_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type coordinatedHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (h coordinatedHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	return h(ctx, scope, event)
}

type countedCoordinationTransactions struct {
	db    *persistence.DB
	calls int
}

func (owner *countedCoordinationTransactions) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	owner.calls++
	return owner.db.WithinTransaction(ctx, fn)
}

type coordinationCursor struct {
	persistence.CursorStore
	inspect func(runtimecontract.DBTX)
}

func (store coordinationCursor) RecordTerminal(ctx context.Context, tx runtimecontract.DBTX, outcome persistence.UpdateOutcome) error {
	if err := store.CursorStore.RecordTerminal(ctx, tx, outcome); err != nil {
		return err
	}

	store.inspect(tx)
	return nil
}

type coordinationState struct {
	health, revision                                           int64
	gameplay, operations, completion, cursors, invalidChildren int
}

func readCoordinationState(t *testing.T, tx runtimecontract.DBTX, id contract.ID) coordinationState {
	t.Helper()
	ctx := context.Background()
	var got coordinationState

	if err := tx.QueryRowContext(ctx, `SELECT health,revision FROM characters WHERE id=?`, id).Scan(&got.health, &got.revision); err != nil {
		t.Fatal(err)
	}

	for _, count := range []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM coordination_gameplay`, &got.gameplay},
		{`SELECT count(*) FROM character_operations`, &got.operations},
		{`SELECT count(*) FROM processed_updates`, &got.completion},
		{`SELECT count(*) FROM runtime_cursors`, &got.cursors},
		{`SELECT count(*) FROM coordination_child`, &got.invalidChildren},
	} {
		if err := tx.QueryRowContext(ctx, count.query).Scan(count.target); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func assertCoordinationState(t *testing.T, tx runtimecontract.DBTX, id contract.ID, want coordinationState) {
	t.Helper()
	if got := readCoordinationState(t, tx, id); got != want {
		t.Fatalf("coordinated state = %+v, want %+v", got, want)
	}
}

type rejectCoordinationReceipt struct {
	runtimecontract.DBTX
	inspect func()
	failure error
}

func (tx rejectCoordinationReceipt) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO character_operations") {
		tx.inspect()
		return nil, tx.failure
	}

	return tx.DBTX.ExecContext(ctx, query, args...)
}

func TestCoordinatedCharacterEffectsCommitAndRollbackWithRuntime(t *testing.T) {
	for _, stage := range []string{"success", "before_receipt", "after_receipt", "commit"} {
		t.Run(stage, func(t *testing.T) {
			service, db, _ := serviceFixture(t)
			ctx := context.Background()
			if _, err := db.ExecContext(ctx, `CREATE TABLE coordination_gameplay(update_id INTEGER PRIMARY KEY,value INTEGER NOT NULL);
 CREATE TABLE coordination_parent(id INTEGER PRIMARY KEY);
 CREATE TABLE coordination_child(parent_id INTEGER REFERENCES coordination_parent(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
				t.Fatal(err)
			}

			var id contract.ID

			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, _, err := service.Ensure(ctx, tx, 42)
				id = got.ID
				return err
			})
			before := coordinationState{health: 100, revision: 1}
			after := coordinationState{health: 60, revision: 2, gameplay: 1, operations: 1, completion: 1, cursors: 1}
			fail := stage != "success"
			handlerCalls := 0
			terminalCalls := 0
			injected := errors.New("injected coordination failure")
			registry := interaction.NewRegistry()
			owner := &countedCoordinationTransactions{db: db}
			var tentative contract.MutationResult

			if err := registry.RegisterCommand("coordinate", coordinatedHandler(func(ctx context.Context, scope runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
				handlerCalls++
				tx := scope.DBTX()
				if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_gameplay(update_id,value) VALUES(100,1)`); err != nil {
					return interaction.HandlerResult{}, err
				}

				effectTx := tx
				if fail && stage == "before_receipt" {
					effectTx = rejectCoordinationReceipt{DBTX: tx, failure: injected, inspect: func() {
						// Both gameplay and health are tentative before the receipt is written.
						assertCoordinationState(t, tx, id, coordinationState{health: 60, revision: 2, gameplay: 1})
						assertCoordinationState(t, db, id, before)
					}}
				}
				var err error

				tentative, err = service.ApplyHealth(ctx, effectTx, contract.HealthChange{OperationID: "coordination:damage", CharacterID: id, Kind: contract.HealthDamage, Amount: 40})
				if err != nil {
					return interaction.HandlerResult{}, err
				}

				if !tentative.Applied || tentative.Current.Health != 60 {
					t.Fatalf("retry did not apply exactly once: %+v", tentative)
				}

				// A successful service result does not make any state durable by itself.
				assertCoordinationState(t, tx, id, coordinationState{health: 60, revision: 2, gameplay: 1, operations: 1})
				assertCoordinationState(t, db, id, before)
				if fail && stage == "after_receipt" {
					return interaction.HandlerResult{}, contract.NewStorageError(injected)
				}

				if fail && stage == "commit" {
					if _, err := tx.ExecContext(ctx, `INSERT INTO coordination_child(parent_id) VALUES(999)`); err != nil {
						return interaction.HandlerResult{}, err
					}
				}
				return interaction.HandlerResult{}, nil
			})); err != nil {
				t.Fatal(err)
			}

			registry.Seal()
			cursor := coordinationCursor{CursorStore: db, inspect: func(tx runtimecontract.DBTX) {
				terminalCalls++
				inside := after
				if fail && stage == "commit" {
					inside.invalidChildren = 1
				}
				// Even completed-update and cursor records remain invisible until commit.
				assertCoordinationState(t, tx, id, inside)
				assertCoordinationState(t, db, id, before)
			}}
			router := interaction.NewRouter(registry, owner, cursor, nil, nil, interaction.RouterOptions{
				Now:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
				NewCorrelationID: func() (runtimecontract.CorrelationID, error) { return "coordination-correlation", nil }, Diagnostics: observability.NewJSONSink(io.Discard),
			})
			event := interaction.Command{Identity: interaction.Identity{Update: 100, Chat: -100, User: 42, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "coordinate"}
			outcome, err := router.Dispatch(ctx, event)

			if fail {
				var transaction *persistence.TransactionError

				if !errors.As(err, &transaction) || !reflect.DeepEqual(outcome, interaction.DispatchOutcome{}) {
					t.Fatalf("failed dispatch returned success: %+v, %v", outcome, err)
				}

				expectedStage := persistence.TransactionStageCallback
				if stage == "commit" {
					expectedStage = persistence.TransactionStageCommit
				}

				if transaction.Stage != expectedStage {
					t.Fatalf("failure stage %q, want %q", transaction.Stage, expectedStage)
				}

				assertCoordinationState(t, db, id, before)
				if cursor, err := db.CompletedUpdate(ctx); err != nil || cursor != 0 {
					t.Fatalf("failed action completed update: cursor=%d err=%v", cursor, err)
				}

				if owner.calls != 1 {
					t.Fatalf("failed action opened %d transactions", owner.calls)
				}

				if stage != "before_receipt" && !tentative.Applied {
					t.Fatal("failure did not follow a tentative successful mutation")
				}

				if stage == "commit" && terminalCalls != 1 {
					t.Fatal("deferred constraint failed before terminal writes")
				}

				fail = false
				outcome, err = router.Dispatch(ctx, event)
			}

			if err != nil || outcome.Status != persistence.UpdateCompleted {
				t.Fatalf("coordinated success: %+v, %v", outcome, err)
			}

			assertCoordinationState(t, db, id, after)
			if cursor, err := db.CompletedUpdate(ctx); err != nil || cursor != event.Update {
				t.Fatalf("successful cursor=%d err=%v", cursor, err)
			}

			wantCalls := 1
			if stage != "success" {
				wantCalls = 2
			}

			if handlerCalls != wantCalls || owner.calls != wantCalls {
				t.Fatalf("handler calls=%d transactions=%d, want %d", handlerCalls, owner.calls, wantCalls)
			}

			// A completed runtime update cannot repeat unrelated gameplay or character work.
			if _, err := router.Dispatch(ctx, event); err != nil {
				t.Fatal(err)
			}

			assertCoordinationState(t, db, id, after)

			if handlerCalls != wantCalls {
				t.Fatal("completed update reran handler")
			}
		})
	}
}
