// Package persistence owns the SQLite database lifecycle and driver boundary.
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	_ "modernc.org/sqlite"
)

const (
	databaseFilename   = "replipg.db"
	maxOpenConnections = 4
	minBusyTimeout     = time.Millisecond
	maxBusyTimeout     = 30 * time.Second
)

// Options is the validated-storage subset consumed by the persistence kernel.
// Open defensively revalidates it before performing filesystem or database I/O.
type Options struct {
	StorageDir        string
	StorageFilesystem config.FilesystemKind
	BusyTimeout       time.Duration
}

// OptionsFromConfig extracts the persistence-owned portion of an already
// validated runtime configuration.
func OptionsFromConfig(cfg config.Config) Options {
	return Options{
		StorageDir:        cfg.StorageDir,
		StorageFilesystem: cfg.StorageFilesystem,
		BusyTimeout:       cfg.BusyTimeout,
	}
}

// DB is the SQLite persistence boundary.
type DB struct {
	sql       *sql.DB
	closeOnce sync.Once
	closeErr  error
	closed    atomic.Bool
}

// Open opens and verifies the fixed runtime SQLite database inside the
// declared local storage directory. It never creates the directory itself.
func Open(ctx context.Context, opts Options) (*DB, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}

	databasePath := filepath.Join(opts.StorageDir, databaseFilename)
	if err := rejectUnsafeDatabaseEntry(databasePath); err != nil {
		return nil, err
	}

	raw, err := sql.Open("sqlite", sqliteDSN(databasePath, opts.BusyTimeout))
	if err != nil {
		return nil, &OpenError{Stage: "open"}
	}
	raw.SetMaxOpenConns(maxOpenConnections)
	raw.SetMaxIdleConns(maxOpenConnections)
	raw.SetConnMaxLifetime(0)
	raw.SetConnMaxIdleTime(0)

	db := &DB{sql: raw}
	if err := db.verify(ctx, opts.BusyTimeout); err != nil {
		_ = raw.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(err, ErrContention) {
			return nil, err
		}
		return nil, &OpenError{Stage: "verification"}
	}
	return db, nil
}

// ExecContext executes a statement at the persistence boundary and maps
// bounded SQLite lock exhaustion to ErrContention.
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := db.available(ctx); err != nil {
		return nil, err
	}
	result, err := db.sql.ExecContext(ctx, query, args...)
	return result, mapError(ctx, err)
}

// QueryContext executes a query at the persistence boundary and maps errors
// encountered while starting it. Callers must close the returned rows.
func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := db.available(ctx); err != nil {
		return nil, err
	}
	rows, err := db.sql.QueryContext(ctx, query, args...)
	return rows, mapError(ctx, err)
}

// QueryRowContext executes a single-row query. Scan errors are mapped by the
// repository method that owns the query because sql.Row defers execution.
func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.sql.QueryRowContext(ctx, query, args...)
}

// Close releases the database pool. Repeated calls return the first result.
func (db *DB) Close() error {
	db.closeOnce.Do(func() {
		db.closed.Store(true)
		db.closeErr = db.sql.Close()
	})
	return db.closeErr
}

func (db *DB) available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if db.closed.Load() {
		return ErrPersistenceClosed
	}
	return nil
}

func validateOptions(opts Options) error {
	if opts.StorageFilesystem != config.FilesystemLocal {
		return &OptionsError{Field: "StorageFilesystem", Reason: "must declare local storage"}
	}
	if strings.IndexByte(opts.StorageDir, 0) >= 0 ||
		!filepath.IsAbs(opts.StorageDir) ||
		filepath.Clean(opts.StorageDir) != opts.StorageDir ||
		opts.StorageDir == string(filepath.Separator) {
		return &OptionsError{Field: "StorageDir", Reason: "must be an absolute clean non-root path"}
	}
	if opts.BusyTimeout < minBusyTimeout || opts.BusyTimeout > maxBusyTimeout {
		return &OptionsError{Field: "BusyTimeout", Reason: "must be within supported bounds"}
	}
	info, err := os.Stat(opts.StorageDir)
	if err != nil {
		return &OptionsError{Field: "StorageDir", Reason: "must be an existing accessible directory"}
	}
	if !info.IsDir() {
		return &OptionsError{Field: "StorageDir", Reason: "must be a directory"}
	}
	return nil
}

func rejectUnsafeDatabaseEntry(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return &OptionsError{Field: "StorageDir", Reason: "database entry is not accessible"}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return &OptionsError{Field: "StorageDir", Reason: "database entry must be a regular file"}
	}
	return nil
}

func sqliteDSN(path string, busyTimeout time.Duration) string {
	busyMilliseconds := int64((busyTimeout + time.Millisecond - 1) / time.Millisecond)
	query := make(url.Values, 5)
	query.Set("_busy_timeout", strconv.FormatInt(busyMilliseconds, 10))
	query.Set("_foreign_keys", "1")
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "FULL")
	query.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func (db *DB) verify(ctx context.Context, busyTimeout time.Duration) error {
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return mapError(ctx, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.PingContext(ctx); err != nil {
		return mapError(ctx, err)
	}

	var journalMode string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return mapError(ctx, err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return errors.New("unexpected journal mode")
	}

	wantBusyMilliseconds := int((busyTimeout + time.Millisecond - 1) / time.Millisecond)
	settings := []struct {
		pragma string
		want   int
	}{
		{pragma: "synchronous", want: 2},
		{pragma: "foreign_keys", want: 1},
		{pragma: "busy_timeout", want: wantBusyMilliseconds},
	}
	for _, setting := range settings {
		var got int
		if err := conn.QueryRowContext(ctx, `PRAGMA `+setting.pragma).Scan(&got); err != nil {
			return mapError(ctx, err)
		}
		if got != setting.want {
			return errors.New("unexpected SQLite setting")
		}
	}
	return nil
}
