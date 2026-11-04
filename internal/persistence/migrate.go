package persistence

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/0001_runtime.sql migrations/0002_characters.sql migrations/0003_inventory.sql migrations/0004_localization.sql migrations/0005_menu_sharing.sql
var runtimeMigrationFiles embed.FS

var migrationFilename = regexp.MustCompile(`^(\d{4})_[a-z0-9][a-z0-9_]*\.sql$`)

const createMigrationLedger = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at INTEGER NOT NULL
)`

type migration struct {
	version int
	sql     string
}

// MigrationSet is an immutable, validated sequence of migration SQL.
type MigrationSet struct {
	migrations []migration
}

// RuntimeMigrations returns the explicitly ordered, embedded runtime schema.
func RuntimeMigrations() (MigrationSet, error) {
	return NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql", "migrations/0002_characters.sql", "migrations/0003_inventory.sql", "migrations/0004_localization.sql", "migrations/0005_menu_sharing.sql")
}

// NewMigrationSet reads and validates an explicitly ordered migration set.
// Paths follow fs.FS conventions, while basenames use NNNN_description.sql.
func NewMigrationSet(fsys fs.FS, filenames ...string) (MigrationSet, error) {
	migrations := make([]migration, 0, len(filenames))
	for index, filename := range filenames {
		if fsys == nil || !fs.ValidPath(filename) {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionInvalidPath)
		}

		match := migrationFilename.FindStringSubmatch(path.Base(filename))
		if match == nil {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionInvalidName)
		}

		version, err := strconv.Atoi(match[1])
		if err != nil || version < 1 {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionInvalidVersion)
		}

		contents, err := fs.ReadFile(fsys, filename)
		if err != nil {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionUnreadable)
		}

		if len(contents) == 0 {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionEmpty)
		}

		migrations = append(migrations, migration{version: version, sql: string(contents)})
	}

	seen := make(map[int]struct{}, len(migrations))
	for index, migration := range migrations {
		if _, duplicate := seen[migration.version]; duplicate {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionDuplicate)
		}

		seen[migration.version] = struct{}{}
		if index > 0 && migration.version < migrations[index-1].version {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionOutOfOrder)
		}
	}

	for index, migration := range migrations {
		if migration.version != index+1 {
			return MigrationSet{}, newDefinitionError(index+1, MigrationDefinitionGap)
		}
	}
	return MigrationSet{migrations: migrations}, nil
}

// Migrate applies each pending migration and its ledger record atomically.
func (db *DB) Migrate(ctx context.Context, set MigrationSet) error {
	if err := db.available(ctx); err != nil {
		return err
	}

	maxVersion := len(set.migrations)
	versions, ledgerExists, err := db.readMigrationLedger(ctx, maxVersion)
	if err != nil {
		return err
	}

	if err := validateLedger(versions, maxVersion); err != nil {
		return err
	}

	if !ledgerExists && len(set.migrations) == 0 {
		if err := db.bootstrapMigrationLedger(ctx); err != nil {
			return err
		}
	}

	for index := len(versions); index < len(set.migrations); index++ {
		if err := db.applyMigration(ctx, set.migrations[index]); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) readMigrationLedger(ctx context.Context, binaryVersion int) (versions []int, ledgerExists bool, resultErr error) {
	exists, err := db.validateMigrationLedgerSchema(ctx, binaryVersion)
	if err != nil {
		return nil, false, err
	}

	if !exists {
		return nil, false, nil
	}

	rows, err := db.sql.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
	}
	defer func() {
		if err := rows.Close(); err != nil && resultErr == nil {
			resultErr = migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
		}
	}()

	for rows.Next() {
		var version int

		if err := rows.Scan(&version); err != nil {
			return nil, true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
		}

		versions = append(versions, version)
	}

	if err := rows.Err(); err != nil {
		return nil, true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
	}

	return versions, true, nil
}

type migrationLedgerColumn struct {
	cid        int
	name       string
	columnType string
	notNull    int
	primaryKey int
	hidden     int
}

func (db *DB) validateMigrationLedgerSchema(ctx context.Context, binaryVersion int) (exists bool, resultErr error) {
	var objectType string

	err := db.sql.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = 'schema_migrations'`).Scan(&objectType)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
	}

	if objectType != "table" {
		return true, corruptSchemaCompatibility(binaryVersion)
	}

	rows, err := db.sql.QueryContext(ctx, `PRAGMA table_xinfo('schema_migrations')`)
	if err != nil {
		return true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
	}
	defer func() {
		if err := rows.Close(); err != nil && resultErr == nil {
			resultErr = migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
		}
	}()

	columns := make([]migrationLedgerColumn, 0, 2)
	for rows.Next() {
		var column migrationLedgerColumn
		var defaultValue any

		if err := rows.Scan(&column.cid, &column.name, &column.columnType, &column.notNull, &defaultValue, &column.primaryKey, &column.hidden); err != nil {
			return true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
		}

		if defaultValue != nil {
			return true, corruptSchemaCompatibility(binaryVersion)
		}

		columns = append(columns, column)
	}

	if err := rows.Err(); err != nil {
		return true, migrationOperationError(0, MigrationStageInspect, MigrationReasonLedger, ctx, err)
	}

	if len(columns) != 2 ||
		!matchesLedgerColumn(columns[0], 0, "version", 0, 1) ||
		!matchesLedgerColumn(columns[1], 1, "applied_at", 1, 0) {
		return true, corruptSchemaCompatibility(binaryVersion)
	}

	return true, nil
}

func matchesLedgerColumn(column migrationLedgerColumn, cid int, name string, notNull, primaryKey int) bool {
	return column.cid == cid &&
		column.name == name &&
		strings.EqualFold(strings.TrimSpace(column.columnType), "INTEGER") &&
		column.notNull == notNull &&
		column.primaryKey == primaryKey &&
		column.hidden == 0
}

func corruptSchemaCompatibility(binaryVersion int) *SchemaCompatibilityError {
	return &SchemaCompatibilityError{
		BinaryVersion: binaryVersion,
		Reason:        SchemaCompatibilityCorrupt,
	}
}

func validateLedger(versions []int, binaryVersion int) error {
	if len(versions) > 0 && versions[len(versions)-1] > binaryVersion {
		return &SchemaCompatibilityError{
			DatabaseVersion: versions[len(versions)-1],
			BinaryVersion:   binaryVersion,
			Reason:          SchemaCompatibilityFuture,
		}
	}

	for index, version := range versions {
		if version != index+1 {
			return &SchemaCompatibilityError{
				DatabaseVersion: version,
				BinaryVersion:   binaryVersion,
				Reason:          SchemaCompatibilityCorrupt,
			}
		}
	}
	return nil
}

func (db *DB) bootstrapMigrationLedger(ctx context.Context) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return migrationOperationError(0, MigrationStageBegin, MigrationReasonLedger, ctx, err)
	}

	if _, err := tx.ExecContext(ctx, createMigrationLedger); err != nil {
		return rollbackMigration(tx, migrationOperationError(0, MigrationStageBootstrap, MigrationReasonLedger, ctx, err), ctx)
	}

	if err := tx.Commit(); err != nil {
		return rollbackMigration(tx, migrationOperationError(0, MigrationStageCommit, MigrationReasonLedger, ctx, err), ctx)
	}

	return nil
}

func (db *DB) applyMigration(ctx context.Context, migration migration) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return migrationOperationError(migration.version, MigrationStageBegin, MigrationReasonTransaction, ctx, err)
	}

	if _, err := tx.ExecContext(ctx, createMigrationLedger); err != nil {
		return rollbackMigration(tx, migrationOperationError(migration.version, MigrationStageBootstrap, MigrationReasonLedger, ctx, err), ctx)
	}

	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return rollbackMigration(tx, migrationOperationError(migration.version, MigrationStageApply, MigrationReasonStatement, ctx, err), ctx)
	}

	appliedAt := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, migration.version, appliedAt); err != nil {
		return rollbackMigration(tx, migrationOperationError(migration.version, MigrationStageRecord, MigrationReasonLedger, ctx, err), ctx)
	}

	if err := tx.Commit(); err != nil {
		return rollbackMigration(tx, migrationOperationError(migration.version, MigrationStageCommit, MigrationReasonTransaction, ctx, err), ctx)
	}

	return nil
}

func rollbackMigration(tx *sql.Tx, primary error, ctx context.Context) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		var migrationErr *MigrationError

		version := 0
		if errors.As(primary, &migrationErr) {
			version = migrationErr.Version
		}
		return migrationOperationError(version, MigrationStageRollback, MigrationReasonTransaction, ctx, err)
	}

	return primary
}
