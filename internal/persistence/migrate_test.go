package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestMigrateAppliesOrderedSetOnceAndPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	set := newTestMigrationSet(t, fstest.MapFS{
		"migrations/0001_create_probe.sql": {Data: []byte(`
			CREATE TABLE migration_probe (value TEXT NOT NULL);
			INSERT INTO migration_probe(value) VALUES ('first');
		`)},
		"migrations/0002_add_probe.sql": {Data: []byte(`
			INSERT INTO migration_probe(value) VALUES ('second');
		`)},
	}, "migrations/0001_create_probe.sql", "migrations/0002_add_probe.sql")

	db := openTestDB(t, dir, testBusyTimeout)
	started := time.Now().UTC().UnixMilli()
	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	finished := time.Now().UTC().UnixMilli()
	assertMigrationState(t, db, 2, 2, started, finished)
	assertMigrationLedgerPragmaShape(t, db)

	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	assertMigrationState(t, db, 2, 2, started, finished)

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db = openTestDB(t, dir, testBusyTimeout)
	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() after reopen error = %v", err)
	}

	assertMigrationState(t, db, 2, 2, started, finished)
}

func TestMigrateRollsBackFailedVersionAndLedgerRecord(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_stable.sql": {Data: []byte(`CREATE TABLE stable_probe (value INTEGER NOT NULL);`)},
		"0002_fails.sql": {Data: []byte(`
			CREATE TABLE rolled_back_probe (value INTEGER NOT NULL);
			INSERT INTO rolled_back_probe(value) VALUES (1);
			THIS IS NOT SQL;
		`)},
	}, "0001_stable.sql", "0002_fails.sql")

	err := db.Migrate(context.Background(), set)
	var migrationErr *MigrationError

	if !errors.As(err, &migrationErr) {
		t.Fatalf("Migrate() error type = %T, want *MigrationError", err)
	}

	if migrationErr.Version != 2 || migrationErr.Stage != MigrationStageApply || migrationErr.Reason != MigrationReasonStatement {
		t.Fatalf("Migrate() error = %#v", migrationErr)
	}

	if !errors.Is(err, ErrMigration) {
		t.Fatalf("Migrate() error = %v, want ErrMigration", err)
	}

	assertTableExists(t, db, "stable_probe", true)
	assertTableExists(t, db, "rolled_back_probe", false)
	assertLedgerVersions(t, db, []int{1})
	if got := err.Error(); got == "" || strings.Contains(got, "THIS IS NOT SQL") || strings.Contains(got, "fails.sql") {
		t.Fatalf("Migrate() exposed unsafe details: %q", got)
	}
}

func TestMigrateRollsBackLedgerBootstrapWithFirstFailedVersion(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_fails.sql": {Data: []byte(`
			CREATE TABLE rolled_back_first_probe (value INTEGER NOT NULL);
			INSERT INTO rolled_back_first_probe(value) VALUES (1);
			THIS IS NOT SQL;
		`)},
	}, "0001_fails.sql")

	err := db.Migrate(context.Background(), set)
	var migrationErr *MigrationError

	if !errors.As(err, &migrationErr) || migrationErr.Version != 1 {
		t.Fatalf("Migrate() error = %v (%T), want version 1 MigrationError", err, err)
	}

	assertTableExists(t, db, "rolled_back_first_probe", false)
	assertTableExists(t, db, "schema_migrations", false)
}

func TestMigrateRejectsFutureSchemaBeforePendingMutation(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (1, 1), (2, 2), (3, 3);
	`); err != nil {
		t.Fatalf("prepare future ledger: %v", err)
	}

	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_noop.sql":    {Data: []byte(`SELECT 1;`)},
		"0002_pending.sql": {Data: []byte(`CREATE TABLE must_not_exist (value INTEGER);`)},
	}, "0001_noop.sql", "0002_pending.sql")

	err := db.Migrate(context.Background(), set)
	var compatibilityErr *SchemaCompatibilityError

	if !errors.As(err, &compatibilityErr) || !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("Migrate() error = %v (%T), want future SchemaCompatibilityError", err, err)
	}

	if compatibilityErr.DatabaseVersion != 3 || compatibilityErr.BinaryVersion != 2 {
		t.Fatalf("compatibility error = %#v", compatibilityErr)
	}

	assertTableExists(t, db, "must_not_exist", false)
	assertLedgerVersions(t, db, []int{1, 2, 3})
}

func TestMigrateRejectsCorruptLedger(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (2, 2);
	`); err != nil {
		t.Fatalf("prepare corrupt ledger: %v", err)
	}

	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_noop.sql":    {Data: []byte(`SELECT 1;`)},
		"0002_pending.sql": {Data: []byte(`CREATE TABLE must_not_exist (value INTEGER);`)},
	}, "0001_noop.sql", "0002_pending.sql")

	err := db.Migrate(context.Background(), set)
	var compatibilityErr *SchemaCompatibilityError

	if !errors.As(err, &compatibilityErr) || !errors.Is(err, ErrCorruptSchema) {
		t.Fatalf("Migrate() error = %v (%T), want corrupt SchemaCompatibilityError", err, err)
	}

	assertTableExists(t, db, "must_not_exist", false)
}

func TestMigrateRejectsMalformedLedgerShapeBeforePendingMutation(t *testing.T) {
	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_noop.sql":    {Data: []byte(`SELECT 1;`)},
		"0002_pending.sql": {Data: []byte(`CREATE TABLE must_not_exist (value INTEGER);`)},
	}, "0001_noop.sql", "0002_pending.sql")
	tests := []struct {
		name  string
		setup string
	}{
		{
			name: "missing applied_at",
			setup: `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY);
				INSERT INTO schema_migrations(version) VALUES (1);`,
		},
		{
			name: "nullable applied_at",
			setup: `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER);
				INSERT INTO schema_migrations(version, applied_at) VALUES (1, 1);`,
		},
		{
			name: "wrong applied_at type",
			setup: `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
				INSERT INTO schema_migrations(version, applied_at) VALUES (1, '1');`,
		},
		{
			name:  "missing version",
			setup: `CREATE TABLE schema_migrations(applied_at INTEGER NOT NULL);`,
		},
		{
			name: "version is not primary key",
			setup: `CREATE TABLE schema_migrations(version INTEGER NOT NULL, applied_at INTEGER NOT NULL);
				INSERT INTO schema_migrations(version, applied_at) VALUES (1, 1);`,
		},
		{
			name: "wrong version type",
			setup: `CREATE TABLE schema_migrations(version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL);
				INSERT INTO schema_migrations(version, applied_at) VALUES ('1', 1);`,
		},
		{
			name: "extra column",
			setup: `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL, extra TEXT);
				INSERT INTO schema_migrations(version, applied_at, extra) VALUES (1, 1, 'unchanged');`,
		},
		{
			name:  "conflicting view",
			setup: `CREATE VIEW schema_migrations AS SELECT 1 AS version, 1 AS applied_at;`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t, t.TempDir(), testBusyTimeout)
			if _, err := db.ExecContext(context.Background(), tt.setup); err != nil {
				t.Fatalf("prepare malformed ledger: %v", err)
			}

			beforeType, beforeSQL := schemaObjectDefinition(t, db, "schema_migrations")
			beforeRows := schemaMigrationObjectRows(t, db)

			err := db.Migrate(context.Background(), set)
			var compatibilityErr *SchemaCompatibilityError

			if !errors.As(err, &compatibilityErr) || !errors.Is(err, ErrCorruptSchema) {
				t.Fatalf("Migrate() error = %v (%T), want corrupt SchemaCompatibilityError", err, err)
			}

			if compatibilityErr.Reason != SchemaCompatibilityCorrupt {
				t.Fatalf("Migrate() compatibility reason = %q, want %q", compatibilityErr.Reason, SchemaCompatibilityCorrupt)
			}

			assertTableExists(t, db, "must_not_exist", false)
			afterType, afterSQL := schemaObjectDefinition(t, db, "schema_migrations")
			afterRows := schemaMigrationObjectRows(t, db)

			if afterType != beforeType || afterSQL != beforeSQL {
				t.Fatalf("schema_migrations changed: before (%q, %q), after (%q, %q)", beforeType, beforeSQL, afterType, afterSQL)
			}

			if afterRows != beforeRows {
				t.Fatalf("schema_migrations row count = %d, want unchanged %d", afterRows, beforeRows)
			}
		})
	}
}

func TestNewMigrationSetRejectsDefinitionsBeforeDatabaseMutation(t *testing.T) {
	files := fstest.MapFS{
		"0001_one.sql":   {Data: []byte(`SELECT 1;`)},
		"0002_two.sql":   {Data: []byte(`SELECT 2;`)},
		"0003_three.sql": {Data: []byte(`SELECT 3;`)},
	}
	tests := []struct {
		name       string
		fsys       fstest.MapFS
		paths      []string
		wantReason MigrationDefinitionReason
	}{
		{name: "invalid filename", fsys: fstest.MapFS{"one.sql": {Data: []byte(`SELECT 1;`)}}, paths: []string{"one.sql"}, wantReason: MigrationDefinitionInvalidName},
		{name: "duplicate", fsys: files, paths: []string{"0001_one.sql", "0001_one.sql"}, wantReason: MigrationDefinitionDuplicate},
		{name: "out of order", fsys: files, paths: []string{"0002_two.sql", "0001_one.sql"}, wantReason: MigrationDefinitionOutOfOrder},
		{name: "gap", fsys: files, paths: []string{"0001_one.sql", "0003_three.sql"}, wantReason: MigrationDefinitionGap},
		{name: "missing file", fsys: files, paths: []string{"0001_missing.sql"}, wantReason: MigrationDefinitionUnreadable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t, t.TempDir(), testBusyTimeout)
			_, err := NewMigrationSet(tt.fsys, tt.paths...)
			var definitionErr *MigrationDefinitionError

			if !errors.As(err, &definitionErr) || !errors.Is(err, ErrMigrationDefinition) {
				t.Fatalf("NewMigrationSet() error = %v (%T), want MigrationDefinitionError", err, err)
			}

			if definitionErr.Reason != tt.wantReason {
				t.Fatalf("NewMigrationSet() reason = %q, want %q", definitionErr.Reason, tt.wantReason)
			}

			assertTableExists(t, db, "schema_migrations", false)
			for _, unsafe := range []string{"one.sql", "two.sql", "three.sql", "missing.sql", "SELECT"} {
				if strings.Contains(err.Error(), unsafe) {
					t.Fatalf("definition error exposes path or SQL: %v", err)
				}
			}
		})
	}
}

func TestMigratePreservesCancellationAndContentionClassification(t *testing.T) {
	set := newTestMigrationSet(t, fstest.MapFS{
		"0001_probe.sql": {Data: []byte(`CREATE TABLE contention_probe (value INTEGER);`)},
	}, "0001_probe.sql")

	t.Run("cancellation", func(t *testing.T) {
		db := openTestDB(t, t.TempDir(), testBusyTimeout)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := db.Migrate(ctx, set)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrContention) {
			t.Fatalf("Migrate() error = %v, want cancellation only", err)
		}

		assertTableExists(t, db, "schema_migrations", false)
	})

	t.Run("contention", func(t *testing.T) {
		dir := t.TempDir()
		holder := openTestDB(t, dir, testBusyTimeout)
		contender := openTestDB(t, dir, testBusyTimeout)
		if _, err := holder.ExecContext(context.Background(), `CREATE TABLE lock_probe (value INTEGER)`); err != nil {
			t.Fatalf("create lock probe: %v", err)
		}

		tx, err := holder.sql.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatalf("BeginTx() error = %v", err)
		}
		defer func() { _ = tx.Rollback() }()

		err = contender.Migrate(context.Background(), set)
		var migrationErr *MigrationError

		if !errors.As(err, &migrationErr) || !errors.Is(err, ErrContention) {
			t.Fatalf("Migrate() error = %v (%T), want MigrationError wrapping ErrContention", err, err)
		}
	})
}

func newTestMigrationSet(t *testing.T, fsys fstest.MapFS, paths ...string) MigrationSet {
	t.Helper()
	set, err := NewMigrationSet(fsys, paths...)
	if err != nil {
		t.Fatalf("NewMigrationSet() error = %v", err)
	}

	return set
}

func assertMigrationState(t *testing.T, db *DB, wantRows, wantVersions int, minTime, maxTime int64) {
	t.Helper()
	var rows int

	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM migration_probe`).Scan(&rows); err != nil {
		t.Fatalf("count probe rows: %v", err)
	}

	if rows != wantRows {
		t.Fatalf("probe rows = %d, want %d", rows, wantRows)
	}

	var versions int
	var minApplied, maxApplied int64

	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*), MIN(applied_at), MAX(applied_at) FROM schema_migrations`).Scan(&versions, &minApplied, &maxApplied); err != nil {
		t.Fatalf("query ledger: %v", err)
	}

	if versions != wantVersions || minApplied < minTime || maxApplied > maxTime {
		t.Fatalf("ledger = count %d, times [%d,%d], want count %d within [%d,%d]", versions, minApplied, maxApplied, wantVersions, minTime, maxTime)
	}
}

func assertLedgerVersions(t *testing.T, db *DB, want []int) {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("ledger rows Close() error = %v", err)
		}
	}()

	var got []int

	for rows.Next() {
		var version int

		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}

		got = append(got, version)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ledger: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("ledger versions = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ledger versions = %v, want %v", got, want)
		}
	}
}

func assertTableExists(t *testing.T, db *DB, table string, want bool) {
	t.Helper()
	var count int

	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("inspect table: %v", err)
	}

	if got := count == 1; got != want {
		t.Fatalf("table %q exists = %v, want %v", table, got, want)
	}
}

func schemaObjectDefinition(t *testing.T, db *DB, name string) (objectType, definition string) {
	t.Helper()
	if err := db.sql.QueryRowContext(context.Background(), `SELECT type, sql FROM sqlite_schema WHERE name = ?`, name).Scan(&objectType, &definition); err != nil {
		t.Fatalf("inspect schema object: %v", err)
	}

	return objectType, definition
}

func schemaMigrationObjectRows(t *testing.T, db *DB) int {
	t.Helper()
	var count int

	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema migration rows: %v", err)
	}

	return count
}

func assertMigrationLedgerPragmaShape(t *testing.T, db *DB) {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), `PRAGMA table_info('schema_migrations')`)
	if err != nil {
		t.Fatalf("inspect migration ledger shape: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("migration ledger shape rows Close() error = %v", err)
		}
	}()

	wantNotNull := map[string]int{"version": 0, "applied_at": 1}
	seen := 0
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any

		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan migration ledger shape: %v", err)
		}

		want, ok := wantNotNull[name]

		if !ok || notNull != want {
			t.Fatalf("PRAGMA table_info column %q notnull = %d, want %d (known=%v)", name, notNull, want, ok)
		}

		seen++
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration ledger shape: %v", err)
	}

	if seen != len(wantNotNull) {
		t.Fatalf("PRAGMA table_info columns = %d, want %d", seen, len(wantNotNull))
	}
}
