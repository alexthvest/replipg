package character_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var errCharacterStorageFault = errors.New("sensitive supplied data and raw SQL")

type characterFaultDriver struct{}
type characterFaultConn struct{ mode string }
type characterFaultRows struct {
	mode     string
	position int
}

func init() { sql.Register("character_fault", characterFaultDriver{}) }
func (characterFaultDriver) Open(mode string) (driver.Conn, error) {
	return &characterFaultConn{mode: mode}, nil
}
func (*characterFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unsupported prepare")
}
func (*characterFaultConn) Begin() (driver.Tx, error) { return nil, errors.New("unsupported begin") }
func (*characterFaultConn) Close() error              { return nil }
func (c *characterFaultConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "query" {
		return nil, errCharacterStorageFault
	}
	return &characterFaultRows{mode: c.mode}, nil
}
func (*characterFaultRows) Columns() []string {
	return []string{"id", "user_id", "max_health", "health", "attack", "defense", "recover_at_ms", "recovery_health", "revision"}
}
func (r *characterFaultRows) Close() error {
	if r.mode == "close" {
		return errCharacterStorageFault
	}
	return nil
}
func (r *characterFaultRows) Next(values []driver.Value) error {
	if r.mode == "iteration" {
		return errCharacterStorageFault
	}
	if r.position > 0 {
		return io.EOF
	}
	r.position++
	row := []driver.Value{"0123456789abcdef0123456789abcdef", int64(42), int64(100), int64(100), int64(10), int64(5), nil, nil, int64(1)}
	if r.mode == "scan" {
		row[2] = "sensitive supplied data and raw SQL"
	}
	copy(values, row)
	return nil
}

func TestCharacterStorageFailuresAreSanitizedAndInspectable(t *testing.T) {
	service, _, _ := serviceFixture(t)
	for _, mode := range []string{"query", "scan", "iteration", "close"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := sql.Open("character_fault", mode)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := raw.Close(); err != nil {
					t.Error(err)
				}
			})
			// This test-owned capability injects driver failures; composed service callers
			// above supply only actual transactions owned by persistence.
			var tx runtimecontract.DBTX = raw
			got, found, err := service.Lookup(context.Background(), tx, 42)
			var storage *contract.StorageError
			if !errors.As(err, &storage) || !errors.Is(err, contract.ErrStorage) {
				t.Fatalf("missing inspectable storage wrapper: %v", err)
			}
			if mode != "scan" && !errors.Is(err, errCharacterStorageFault) {
				t.Fatalf("lost underlying cause: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "SQL") {
				t.Fatalf("unsafe error %v", err)
			}
			if found || got != (contract.Snapshot{}) {
				t.Fatal("failed read returned success")
			}
		})
	}
}

type failingCharacterExec struct {
	runtimecontract.DBTX
	failure error
}

func (tx failingCharacterExec) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, tx.failure
}
func TestCharacterInsertFailureRollsBackAndPreservesCause(t *testing.T) {
	service, db, _ := serviceFixture(t)
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, _, err := service.Ensure(ctx, failingCharacterExec{DBTX: tx, failure: errCharacterStorageFault}, 42)
		if !errors.Is(err, errCharacterStorageFault) || !errors.Is(err, contract.ErrStorage) {
			t.Fatalf("lost exec failure: %v", err)
		}
		if strings.Contains(err.Error(), "SQL") {
			t.Fatalf("unsafe error: %v", err)
		}
		return err
	})
	if !errors.Is(err, errCharacterStorageFault) {
		t.Fatal("caller did not receive original failure")
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, found, err := service.Lookup(ctx, tx, 42)
		if found {
			t.Fatal("failed insert persisted")
		}
		return err
	})
}
