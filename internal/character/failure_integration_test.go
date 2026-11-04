package character_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	runtimeconfig "github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type storageFailureHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (fn storageFailureHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	return fn(ctx, scope, event)
}

type characterRowFault struct {
	runtimecontract.DBTX
	rows *sql.DB
}

func (tx characterRowFault) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "FROM characters") {
		return tx.rows.QueryContext(ctx, query, args...)
	}
	return tx.DBTX.QueryContext(ctx, query, args...)
}

func TestCharacterRowFailuresRemainRetryableAtDispatch(t *testing.T) {
	for _, mode := range []string{"scan", "iteration", "close"} {
		t.Run(mode, func(t *testing.T) {
			service, db, _ := serviceFixture(t)
			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				_, _, err := service.Ensure(ctx, tx, 42)
				return err
			})
			raw, err := sql.Open("character_fault", mode)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := raw.Close(); err != nil {
					t.Error(err)
				}
			})
			registry := interaction.NewRegistry()
			err = registry.RegisterCommand("failure", storageFailureHandler(func(ctx context.Context, scope runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
				if _, err := scope.DBTX().ExecContext(ctx, `UPDATE characters SET health=99,revision=2 WHERE user_id=42`); err != nil {
					return interaction.HandlerResult{}, err
				}
				_, _, err := service.Lookup(ctx, characterRowFault{DBTX: scope.DBTX(), rows: raw}, 42)
				if !errors.Is(err, contract.ErrStorage) {
					t.Fatalf("expected character storage error: %v", err)
				}
				if mode == "scan" {
					var storage *contract.StorageError
					if !errors.As(err, &storage) || errors.Unwrap(storage) == nil {
						t.Fatalf("lost scan conversion cause: %v", err)
					}
				} else if !errors.Is(err, errCharacterStorageFault) {
					t.Fatalf("lost row failure cause: %v", err)
				}
				return interaction.HandlerResult{}, err
			}))
			if err != nil {
				t.Fatal(err)
			}
			registry.Seal()
			var diagnostics bytes.Buffer
			router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Diagnostics: observability.NewJSONSink(&diagnostics)})
			outcome, err := router.Dispatch(context.Background(), interaction.Command{Identity: interaction.Identity{Update: 77, Chat: -100, User: 42, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "failure"})
			if diagnostics.Len() != 0 {
				t.Fatal("retryable row failure emitted terminal diagnostic")
			}
			if err == nil || outcome.Status != "" {
				t.Fatalf("storage failure became terminal outcome: status=%q err=%v", outcome.Status, err)
			}
			if !errors.Is(err, contract.ErrStorage) {
				t.Fatalf("lost storage wrapper: %v", err)
			}
			if _, found, err := db.TerminalOutcome(context.Background(), 77); err != nil || found {
				t.Fatalf("failed update durably completed: found=%v err=%v", found, err)
			}
			if cursor, err := db.CompletedUpdate(context.Background()); err != nil || cursor != 0 {
				t.Fatalf("failed update advanced cursor: %d err=%v", cursor, err)
			}
			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, _, err := service.Lookup(ctx, tx, 42)
				if got.Health != 100 || got.Revision != 1 {
					t.Fatal("partial character state persisted")
				}
				return err
			})
		})
	}
}

func TestCharacterStorageClassificationRetainsSafeMessage(t *testing.T) {
	wrapped := contract.NewStorageError(errors.New("sensitive raw SQL and supplied data"))
	if !persistence.IsStorageFailure(fmt.Errorf("outer: %w", wrapped)) {
		t.Fatal("generic character storage failure is not classified as storage")
	}
	if strings.Contains(wrapped.Error(), "sensitive") {
		t.Fatal("unsafe storage message")
	}
}

type mutationStorageFault struct {
	runtimecontract.DBTX
	mode   string
	cancel context.CancelFunc
}

func (tx mutationStorageFault) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx.mode == "query" && strings.Contains(query, "FROM character_operations") {
		return nil, errCharacterStorageFault
	}
	return tx.DBTX.QueryContext(ctx, query, args...)
}
func (tx mutationStorageFault) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if tx.mode == "update" && strings.HasPrefix(query, "UPDATE characters") || tx.mode == "receipt" && strings.HasPrefix(query, "INSERT INTO character_operations") {
		return nil, errCharacterStorageFault
	}
	if tx.mode == "cancel" && strings.HasPrefix(query, "INSERT INTO character_operations") {
		tx.cancel()
	}
	return tx.DBTX.ExecContext(ctx, query, args...)
}
func TestCharacterMutationFailuresDoNotConsumeUpdatesOrReceipts(t *testing.T) {
	for _, mode := range []string{"query", "update", "receipt", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			service, db, _ := serviceFixture(t)
			var snapshot contract.Snapshot
			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				var err error
				snapshot, _, err = service.Ensure(ctx, tx, 42)
				return err
			})
			change := contract.HealthChange{OperationID: "fault:mutation:77", CharacterID: snapshot.ID, Kind: contract.HealthDamage, Amount: 30}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			registry := interaction.NewRegistry()
			inject := true
			if err := registry.RegisterCommand("failure", storageFailureHandler(func(ctx context.Context, scope runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
				tx := scope.DBTX()
				if inject {
					tx = mutationStorageFault{DBTX: tx, mode: mode, cancel: cancel}
				}
				result, err := service.ApplyHealth(ctx, tx, change)
				if inject {
					if result != (contract.MutationResult{}) || !errors.Is(err, contract.ErrStorage) {
						t.Fatalf("unsafe failed mutation result: %+v err=%v", result, err)
					}
					if mode == "cancel" {
						if !errors.Is(err, context.Canceled) {
							t.Fatal("lost cancellation cause")
						}
					} else if !errors.Is(err, errCharacterStorageFault) {
						t.Fatal("lost SQL failure cause")
					}
					if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "SQL") {
						t.Fatal("unsafe diagnostic text")
					}
				}
				return interaction.HandlerResult{}, err
			})); err != nil {
				t.Fatal(err)
			}
			registry.Seal()
			var diagnostics bytes.Buffer
			router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Diagnostics: observability.NewJSONSink(&diagnostics)})
			event := interaction.Command{Identity: interaction.Identity{Update: 77, Chat: -100, User: 42, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "failure"}
			outcome, err := router.Dispatch(ctx, event)
			if err == nil || outcome.Status != "" || diagnostics.Len() != 0 {
				t.Fatalf("failed mutation completed: %+v err=%v", outcome, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("dispatch lost cancellation")
			}
			var receipts int
			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM character_operations`).Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("receipt escaped failure: %d %v", receipts, err)
			}
			if _, found, err := db.TerminalOutcome(context.Background(), 77); err != nil || found {
				t.Fatalf("update completed despite failed mutation: %v %v", found, err)
			}
			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, _, err := service.Lookup(ctx, tx, 42)
				if got != snapshot {
					t.Fatal("failed mutation changed durable state")
				}
				return err
			})
			inject = false
			outcome, err = router.Dispatch(context.Background(), event)
			if err != nil || outcome.Status != persistence.UpdateCompleted {
				t.Fatalf("retry could not complete: %+v %v", outcome, err)
			}
			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, _, err := service.Lookup(ctx, tx, 42)
				if got.Health != 70 || got.Revision != 2 {
					t.Fatalf("retry applied incorrectly: %+v", got)
				}
				return err
			})
		})
	}
}

func TestCharacterTransactionContentionRemainsRetryable(t *testing.T) {
	service, holder, dir := serviceFixture(t)
	inCharacterTx(t, holder, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, _, err := service.Ensure(ctx, tx, 42)
		return err
	})
	contender, err := persistence.Open(context.Background(), persistence.Options{StorageDir: dir, StorageFilesystem: runtimeconfig.FilesystemLocal, BusyTimeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := contender.Close(); err != nil {
			t.Error(err)
		}
	})
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error { close(locked); <-release; return nil })
	}()
	<-locked
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	called := false
	err = contender.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		called = true
		_, _, err := service.Ensure(ctx, tx, 42)
		return err
	})
	if called || !errors.Is(err, persistence.ErrContention) || !persistence.IsStorageFailure(err) {
		t.Fatalf("lock failure misclassified: callback=%v err=%v", called, err)
	}
}
