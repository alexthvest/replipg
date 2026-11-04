package inventory

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var errInventoryFault = errors.New("sensitive SQL and inventory payload")

type inventoryFaultDriver struct{}

type inventoryFaultConn struct{ mode string }

type inventoryFaultRows struct {
	mode string
	read bool
}

func init() { sql.Register("inventory_fault", inventoryFaultDriver{}) }

func (inventoryFaultDriver) Open(mode string) (driver.Conn, error) {
	return inventoryFaultConn{mode}, nil
}

func (inventoryFaultConn) Prepare(string) (driver.Stmt, error) { return nil, errInventoryFault }
func (inventoryFaultConn) Begin() (driver.Tx, error)           { return nil, errInventoryFault }
func (inventoryFaultConn) Close() error                        { return nil }
func (c inventoryFaultConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "query" {
		return nil, errInventoryFault
	}

	return &inventoryFaultRows{mode: c.mode}, nil
}

func (*inventoryFaultRows) Columns() []string { return []string{"id", "user_id"} }

func (r *inventoryFaultRows) Close() error {
	if r.mode == "close" {
		return errInventoryFault
	}

	return nil
}

func (r *inventoryFaultRows) Next(values []driver.Value) error {
	if r.mode == "iteration" {
		return errInventoryFault
	}

	if r.read {
		return io.EOF
	}

	r.read = true
	values[0] = inventoryTestCharacter
	values[1] = int64(42)
	if r.mode == "scan" {
		values[1] = "sensitive SQL and inventory payload"
	}
	return nil
}

func TestInventoryReadFailureClassificationAndPrivacy(t *testing.T) {
	_, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	for _, mode := range []string{"query", "scan", "iteration", "close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := sql.Open("inventory_fault", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = raw.Close() }()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if mode == "cancel" {
				cancel()
			}
			_, err = service.Inspect(ctx, raw, inventoryTestCharacter)
			var storage runtimecontract.StorageFailure

			if !errors.As(err, &storage) || !errors.Is(err, contract.ErrStorage) || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("unsafe unclassified error %v", err)
			}

			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation cause lost")
				}
			} else if mode != "scan" && !errors.Is(err, errInventoryFault) {
				t.Fatal("storage cause lost")
			}
		})
	}
}

func TestInventoryBusyTransactionRemainsRetryable(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	var id charactercontract.ID

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		id = opened.Current.CharacterID
		return err
	})
	lock, err := sql.Open("sqlite", filepath.Join(dir, "replipg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()

	lock.SetMaxOpenConns(1)
	if _, err := lock.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = lock.ExecContext(context.Background(), `ROLLBACK`) }()

	request := contract.GrantRequest{OperationID: "fault:busy:grant", CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 1}}}
	err = db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := service.Grant(ctx, tx, request)
		return err
	})
	if !errors.Is(err, persistence.ErrContention) || !persistence.IsStorageFailure(err) {
		t.Fatalf("busy failure not retryable: %v", err)
	}

	if _, err := lock.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		quantity, err := ownedQuantity(ctx, tx, id, "coin")
		if err != nil {
			return err
		}

		if quantity != 10 {
			t.Fatal("busy operation partially changed items")
		}

		result, err := service.Grant(ctx, tx, request)
		if err != nil {
			return err
		}

		if !result.Applied {
			t.Fatal("busy failure consumed identity")
		}

		return nil
	})
}

type inventoryFailureHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (fn inventoryFailureHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	return fn(ctx, scope, event)
}

type inventoryFaultScope struct {
	runtimecontract.WorkScope
	tx runtimecontract.DBTX
}

func (s inventoryFaultScope) DBTX() runtimecontract.DBTX { return s.tx }

type inventoryCleanupFault struct {
	runtimecontract.DBTX
	stage string
}

func (tx inventoryCleanupFault) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(query, tx.stage) {
		return nil, errInventoryFault
	}

	return tx.DBTX.ExecContext(ctx, query, args...)
}

func TestInventoryCallbackCleanupFailureRemainsRetryable(t *testing.T) {
	for _, stage := range []string{"ROLLBACK TO SAVEPOINT", "RELEASE SAVEPOINT"} {
		t.Run(stage, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
			var token string
			var before contract.Snapshot

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}

				opened, err := service.Open(ctx, tx, 42)
				if err != nil {
					return err
				}

				before = opened.Current
				token, err = createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: before.CharacterID, Action: "equip", ItemID: "stick", Slot: contract.SlotWeapon, ExpectedRevision: before.Revision - 1})
				return err
			})
			fail := true
			registry := interaction.NewRegistry()
			handler := inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}
			if err := registry.RegisterCallback("inv:", inventoryFailureHandler(func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
				if fail {
					scope = inventoryFaultScope{scope, inventoryCleanupFault{scope.DBTX(), stage}}
				}
				return handler.Handle(ctx, scope, event)
			})); err != nil {
				t.Fatal(err)
			}

			registry.Seal()
			var diagnostics bytes.Buffer

			router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Now: clock.Now, Diagnostics: observability.NewJSONSink(&diagnostics)})
			event := interaction.Callback{EphemeralMessageID: 900, Identity: interaction.Identity{Update: 77, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "callback-storage-failure", Data: token}
			result, err := router.Dispatch(context.Background(), event)
			var storage runtimecontract.StorageFailure

			if err == nil || !errors.As(err, &storage) || len(result.ImmediateResponses) != 0 || result.Status != "" {
				t.Fatalf("cleanup became completed personal response %+v err%v", result, err)
			}

			if diagnostics.Len() != 0 {
				t.Fatal("storage failure emitted terminal diagnostics")
			}

			if _, found, err := db.TerminalOutcome(context.Background(), 77); err != nil || found {
				t.Fatal("failed update marked terminal")
			}

			if cursor, err := db.CompletedUpdate(context.Background()); err != nil || cursor != 0 {
				t.Fatal("failed update advanced cursor")
			}

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				after, _, err := readInventory(ctx, tx, catalog, before.CharacterID)

				if !reflect.DeepEqual(after, before) {
					t.Fatal("cleanup failure altered state")
				}

				return err
			})
			fail = false
			result, err = router.Dispatch(context.Background(), event)
			if err != nil || result.Status != persistence.UpdateCompleted || len(result.ImmediateResponses) != 1 {
				t.Fatalf("same update failed safe retry %+v %v", result, err)
			}
		})
	}
}
