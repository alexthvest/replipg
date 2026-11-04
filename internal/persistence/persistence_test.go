package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
)

const testBusyTimeout = 75 * time.Millisecond

func TestOpenConfiguresAndPersistsBoundedLocalSQLite(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir, testBusyTimeout)

	assertPragmas(t, db, int(testBusyTimeout.Milliseconds()))
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE task_2_1_probe (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}

	if _, err := db.ExecContext(context.Background(), `INSERT INTO task_2_1_probe(value) VALUES (?)`, "survives"); err != nil {
		t.Fatalf("insert probe value: %v", err)
	}

	assertSidecars(t, dir)
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	db = openTestDB(t, dir, testBusyTimeout)
	rows, err := db.QueryContext(context.Background(), `SELECT value FROM task_2_1_probe`)
	if err != nil {
		t.Fatalf("query reopened database: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("rows Close() error = %v", err)
		}
	}()

	if !rows.Next() {
		t.Fatalf("reopened row missing: %v", rows.Err())
	}

	var value string

	if err := rows.Scan(&value); err != nil || value != "survives" {
		t.Fatalf("reopened value = %q, err = %v", value, err)
	}
}

func TestOpenAppliesConnectionLocalPragmasToPool(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)

	connections := make([]interface{ Close() error }, 0, maxOpenConnections)
	for range maxOpenConnections {
		conn, err := db.sql.Conn(context.Background())
		if err != nil {
			t.Fatalf("Conn() error = %v", err)
		}

		connections = append(connections, conn)
		assertPragmasOn(t, conn, int(testBusyTimeout.Milliseconds()))
	}

	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Errorf("connection Close() error = %v", err)
		}
	}

	if got := db.sql.Stats().MaxOpenConnections; got != maxOpenConnections {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, maxOpenConnections)
	}
}

func TestImmediateWritesReturnTypedContentionWithinBound(t *testing.T) {
	dir := t.TempDir()
	holder := openTestDB(t, dir, testBusyTimeout)
	contender := openTestDB(t, dir, testBusyTimeout)

	if _, err := holder.ExecContext(context.Background(), `CREATE TABLE task_2_1_lock (value INTEGER)`); err != nil {
		t.Fatalf("create lock table: %v", err)
	}

	tx, err := holder.sql.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	started := time.Now()
	_, err = contender.ExecContext(context.Background(), `INSERT INTO task_2_1_lock(value) VALUES (1)`)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrContention) {
		t.Fatalf("ExecContext() error = %v, want ErrContention", err)
	}

	var contention *ContentionError

	if !errors.As(err, &contention) {
		t.Fatalf("ExecContext() error type = %T, want *ContentionError", err)
	}

	if elapsed > testBusyTimeout+500*time.Millisecond {
		t.Fatalf("contention elapsed = %v, exceeds bounded tolerance", elapsed)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = contender.ExecContext(cancelled, `INSERT INTO task_2_1_lock(value) VALUES (2)`)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrContention) {
		t.Fatalf("cancelled ExecContext() error = %v", err)
	}
}

func TestOpenRejectsUnsafeStorageBeforeCreatingDatabase(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(dir, "missing")
	tests := []struct {
		name string
		opts Options
	}{
		{name: "relative", opts: Options{StorageDir: "relative", StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second}},
		{name: "undeclared local", opts: Options{StorageDir: dir, StorageFilesystem: "network", BusyTimeout: time.Second}},
		{name: "missing", opts: Options{StorageDir: missing, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second}},
		{name: "file", opts: Options{StorageDir: file, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second}},
		{name: "zero timeout", opts: Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal}},
		{name: "excessive timeout", opts: Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: 31 * time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Open(context.Background(), tt.opts)
			if err == nil {
				t.Fatal("Open() error = nil")
			}

			var optionsErr *OptionsError

			if !errors.As(err, &optionsErr) {
				t.Fatalf("Open() error type = %T, want *OptionsError", err)
			}

			if strings.Contains(err.Error(), tt.opts.StorageDir) {
				t.Fatalf("Open() error exposes storage path: %v", err)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(missing, databaseFilename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe open created database or directory: %v", err)
	}
}

func assertPragmas(t *testing.T, db *DB, busyTimeoutMS int) {
	t.Helper()
	conn, err := db.sql.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("connection Close() error = %v", err)
		}
	}()

	assertPragmasOn(t, conn, busyTimeoutMS)
}

func assertPragmasOn(t *testing.T, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, busyTimeoutMS int) {
	t.Helper()
	checks := []struct {
		pragma string
		want   int
	}{
		{pragma: "synchronous", want: 2},
		{pragma: "foreign_keys", want: 1},
		{pragma: "busy_timeout", want: busyTimeoutMS},
	}
	var journal string

	if err := conn.QueryRowContext(context.Background(), `PRAGMA journal_mode`).Scan(&journal); err != nil || strings.ToLower(journal) != "wal" {
		t.Fatalf("journal_mode = %q, err = %v", journal, err)
	}

	for _, check := range checks {
		var got int

		if err := conn.QueryRowContext(context.Background(), `PRAGMA `+check.pragma).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s = %d, want %d, err = %v", check.pragma, got, check.want, err)
		}
	}
}

func assertSidecars(t *testing.T, dir string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(dir, databaseFilename+suffix)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected active SQLite file %q: %v", filepath.Base(path), err)
		}
	}
}

func openTestDB(t *testing.T, dir string, timeout time.Duration) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{
		StorageDir:        dir,
		StorageFilesystem: config.FilesystemLocal,
		BusyTimeout:       timeout,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("DB Close() error = %v", err)
		}
	})

	return db
}
