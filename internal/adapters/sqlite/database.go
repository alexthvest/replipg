// Package sqlite implements persistence ports using one durable SQLite connection.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Database struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Database, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("database directory: %w", err)
	}

	db, err := openDriver(path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	d := &Database{db: db}

	for _, statement := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=FULL",
	} {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}

	if err = d.migrate(ctx, migrations); err != nil {
		_ = db.Close()
		return nil, err
	}
	return d, nil
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) migrate(ctx context.Context, source fs.FS) error {
	names, err := fs.Glob(source, "migrations/*.sql")
	if err != nil {
		return err
	}

	sort.Strings(names)
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(names) {
		return fmt.Errorf("database schema %d is newer than supported %d; restore a compatible backup", version, len(names))
	}

	for i := version; i < len(names); i++ {
		data, readErr := fs.ReadFile(source, names[i])
		if readErr != nil {
			return readErr
		}

		if _, err = tx.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", names[i], err)
		}

		if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
