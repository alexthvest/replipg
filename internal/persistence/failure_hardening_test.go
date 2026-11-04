package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestCancellationInterruptsImmediateLockWait(t *testing.T) {
	dir := t.TempDir()
	holder := openPersistenceWithTimeout(t, dir, 2*time.Second)
	contender := openPersistenceWithTimeout(t, dir, 2*time.Second)
	if _, err := holder.ExecContext(context.Background(), `CREATE TABLE cancellation_probe (value INTEGER)`); err != nil {
		t.Fatalf("create probe: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	contenderStarted := make(chan struct{})
	contenderDone := make(chan error, 1)
	go func() {
		close(contenderStarted)
		contenderDone <- contender.WithinTransaction(ctx, func(context.Context, runtimecontract.DBTX) error {
			return errors.New("callback must not run")
		})
	}()
	<-contenderStarted
	select {
	case err := <-contenderDone:
		close(release)
		t.Fatalf("contender returned before cancellation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancelledAt := time.Now()
	cancel()
	select {
	case err := <-contenderDone:
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrContention) {
			close(release)
			t.Fatalf("WithinTransaction() error = %v, want cancellation only", err)
		}
		var transactionErr *TransactionError
		if !errors.As(err, &transactionErr) || transactionErr.Stage != TransactionStageBegin {
			close(release)
			t.Fatalf("WithinTransaction() error = %#v, want begin TransactionError", err)
		}
		if elapsed := time.Since(cancelledAt); elapsed > 500*time.Millisecond {
			close(release)
			t.Fatalf("cancellation took %v, want prompt lock interruption", elapsed)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cancellation did not interrupt immediate lock wait")
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder transaction error = %v", err)
	}
	if err := contender.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error { return nil }); err != nil {
		t.Fatalf("transaction after cancellation error = %v", err)
	}
}

func TestCloseIsIdempotentAndPublicOperationsFailSafely(t *testing.T) {
	dir := t.TempDir()
	db := openPersistenceWithTimeout(t, dir, testBusyTimeout)
	set, err := RuntimeMigrations()
	if err != nil {
		t.Fatalf("RuntimeMigrations() error = %v", err)
	}
	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	checks := []struct {
		name string
		run  func() error
	}{
		{name: "exec", run: func() error { _, err := db.ExecContext(context.Background(), `SELECT 1`); return err }},
		{name: "query", run: func() error { _, err := db.QueryContext(context.Background(), `SELECT 1`); return err }},
		{name: "transaction", run: func() error {
			return db.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error { return nil })
		}},
		{name: "migration", run: func() error { return db.Migrate(context.Background(), set) }},
		{name: "cursor", run: func() error { _, err := db.CompletedUpdate(context.Background()); return err }},
		{name: "outcome", run: func() error { _, _, err := db.TerminalOutcome(context.Background(), 1); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.run()
			if !errors.Is(err, ErrPersistenceClosed) {
				t.Fatalf("operation error = %v (%T), want ErrPersistenceClosed", err, err)
			}
			for _, unsafe := range []string{dir, databaseFilename, "SELECT", "runtime_cursors", "processed_updates"} {
				if strings.Contains(err.Error(), unsafe) {
					t.Fatalf("closed error exposed private storage detail %q: %v", unsafe, err)
				}
			}
		})
	}
	var value int
	if err := db.QueryRowContext(context.Background(), `SELECT 1`).Scan(&value); err == nil {
		t.Fatal("QueryRowContext() after Close() error = nil")
	} else {
		for _, unsafe := range []string{dir, databaseFilename, "SELECT"} {
			if strings.Contains(err.Error(), unsafe) {
				t.Fatalf("closed row error exposed private storage detail %q: %v", unsafe, err)
			}
		}
	}
}

func TestFailedMigrationCanRetryWithoutPartialStateOrPoolLeak(t *testing.T) {
	db := openPersistenceWithTimeout(t, t.TempDir(), testBusyTimeout)
	failing := newTestMigrationSet(t, fstest.MapFS{
		"0001_stable.sql": {Data: []byte(`CREATE TABLE retry_stable (value INTEGER NOT NULL);`)},
		"0002_fails.sql":  {Data: []byte(`CREATE TABLE retry_partial (value INTEGER); THIS IS NOT SQL;`)},
	}, "0001_stable.sql", "0002_fails.sql")
	working := newTestMigrationSet(t, fstest.MapFS{
		"0001_stable.sql": {Data: []byte(`CREATE TABLE retry_stable (value INTEGER NOT NULL);`)},
		"0002_retry.sql":  {Data: []byte(`CREATE TABLE retry_complete (value INTEGER NOT NULL);`)},
	}, "0001_stable.sql", "0002_retry.sql")

	for range 20 {
		err := db.Migrate(context.Background(), failing)
		if !errors.Is(err, ErrMigration) {
			t.Fatalf("failed Migrate() error = %v, want ErrMigration", err)
		}
		if got := db.sql.Stats().InUse; got != 0 {
			t.Fatalf("connections in use after failed migration = %d, want 0", got)
		}
		assertTableExists(t, db, "retry_partial", false)
		assertLedgerVersions(t, db, []int{1})
	}

	if err := db.Migrate(context.Background(), working); err != nil {
		t.Fatalf("retry Migrate() error = %v", err)
	}
	assertTableExists(t, db, "retry_complete", true)
	assertLedgerVersions(t, db, []int{1, 2})
	stats := db.sql.Stats()
	if stats.InUse != 0 || stats.OpenConnections > maxOpenConnections {
		t.Fatalf("pool stats after retry = %+v, want no in-use or excess connections", stats)
	}
}

func openPersistenceWithTimeout(t *testing.T, dir string, timeout time.Duration) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{
		StorageDir:        dir,
		StorageFilesystem: config.FilesystemLocal,
		BusyTimeout:       timeout,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
