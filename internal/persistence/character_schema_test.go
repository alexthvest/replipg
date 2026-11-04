package persistence

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const characterInsertSQL = `INSERT INTO characters(id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision) VALUES(?,?,?,?,?,?,?,?,?)`
const operationInsertSQL = `INSERT INTO character_operations(operation_id,character_id,kind,amount,applied_revision) VALUES(?,?,?,?,?)`
const schemaCharacterID = "0123456789abcdef0123456789abcdef"

func TestCharacterSchemaColumnsAndConstraints(t *testing.T) {
	db := migratedRuntimeDB(t)
	expected := map[string][]schemaColumn{
		"characters":           {{"id", "TEXT", 1, 1}, {"user_id", "INTEGER", 1, 0}, {"max_health", "INTEGER", 1, 0}, {"health", "INTEGER", 1, 0}, {"attack", "INTEGER", 1, 0}, {"defense", "INTEGER", 1, 0}, {"recover_at_ms", "INTEGER", 0, 0}, {"recovery_health", "INTEGER", 0, 0}, {"revision", "INTEGER", 1, 0}},
		"character_operations": {{"operation_id", "TEXT", 1, 1}, {"character_id", "TEXT", 1, 0}, {"kind", "TEXT", 1, 0}, {"amount", "INTEGER", 1, 0}, {"applied_revision", "INTEGER", 1, 0}},
	}
	for table, want := range expected {
		if got := tableColumns(t, db, table); !slices.Equal(got, want) {
			t.Fatalf("%s columns = %#v, want %#v", table, got, want)
		}
	}
	if _, err := db.ExecContext(context.Background(), characterInsertSQL, schemaCharacterID, 42, 100, 100, 10, 0, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name   string
		column int
		value  any
	}{
		{"null id", 0, nil}, {"short id", 0, "abc"}, {"uppercase id", 0, strings.Repeat("A", 32)}, {"nonhex id", 0, strings.Repeat("z", 32)}, {"embedded nul id", 0, strings.Repeat("a", 32) + "\x00"},
		{"zero owner", 1, 0}, {"duplicate owner", 1, 42}, {"fractional owner", 1, 43.5}, {"zero maximum", 2, 0}, {"fractional maximum", 2, 100.5},
		{"negative health", 3, -1}, {"excess health", 3, 101}, {"fractional health", 3, 50.5}, {"negative attack", 4, -1}, {"fractional attack", 4, 1.5}, {"negative defense", 5, -1}, {"fractional defense", 5, 1.5},
		{"active deadline", 6, 1000}, {"fractional deadline", 6, 1000.5}, {"active recovery", 7, 10}, {"zero revision", 8, 0}, {"fractional revision", 8, 1.5},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			args := []any{strings.Repeat("a", 32), 43, 100, 50, 10, 0, nil, nil, 1}
			args[tc.column] = tc.value
			assertStatementFails(t, db, characterInsertSQL, args...)
		})
	}
	states := []struct {
		name             string
		deadline, health any
	}{{"missing deadline", nil, 20}, {"missing recovery", 1000, nil}, {"both missing", nil, nil}, {"zero recovery", 1000, 0}, {"excess recovery", 1000, 101}, {"fractional recovery", 1000, 20.5}}
	for _, tc := range states {
		t.Run(tc.name, func(t *testing.T) {
			assertStatementFails(t, db, characterInsertSQL, strings.Repeat("b", 32), 44, 100, 0, 0, 0, tc.deadline, tc.health, 1)
		})
	}
	if _, err := db.ExecContext(context.Background(), characterInsertSQL, strings.Repeat("c", 32), 45, 100, 0, 0, 0, 1000, 20, 2); err != nil {
		t.Fatal(err)
	}
	assertStatementFails(t, db, characterInsertSQL, schemaCharacterID, 46, 100, 100, 0, 0, nil, nil, 1)
}

func TestCharacterOperationSchemaIntegrity(t *testing.T) {
	db := migratedRuntimeDB(t)
	if _, err := db.ExecContext(context.Background(), characterInsertSQL, schemaCharacterID, 42, 100, 100, 10, 0, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"damage", "heal"} {
		if _, err := db.ExecContext(context.Background(), operationInsertSQL, kind, schemaCharacterID, kind, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []struct {
		name   string
		column int
		value  any
	}{
		{"null identity", 0, nil}, {"empty identity", 0, ""}, {"long identity", 0, strings.Repeat("x", 129)}, {"long multibyte identity", 0, strings.Repeat("é", 65)}, {"duplicate identity", 0, "damage"},
		{"missing character", 1, strings.Repeat("f", 32)}, {"unknown kind", 2, "other"}, {"negative amount", 3, -1}, {"fractional amount", 3, 0.5}, {"invalid revision", 4, 0}, {"fractional revision", 4, 1.5},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			args := []any{"operation", schemaCharacterID, "damage", 1, 1}
			args[tc.column] = tc.value
			assertStatementFails(t, db, operationInsertSQL, args...)
		})
	}
	if _, err := db.ExecContext(context.Background(), operationInsertSQL, strings.Repeat("x", 128), schemaCharacterID, "heal", 0, 1); err != nil {
		t.Fatal(err)
	}
	assertStatementFails(t, db, `DELETE FROM characters WHERE id=?`, schemaCharacterID)
	assertRowCount(t, db, "character_operations", "1=1", 3)
}

func TestCharacterMigrationUpgradePreservesRuntimeAndReopens(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openTestDB(t, dir, testBusyTimeout)
	original, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, original); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO runtime_cursors VALUES('telegram',123,456); INSERT INTO processed_updates VALUES(123,'completed','correlation',NULL,456)`); err != nil {
		t.Fatal(err)
	}
	insertDelivery(t, db, "preserved-delivery", "pending", int64(10), nil, nil)
	insertJob(t, db, "preserved-job", "pending", nil, nil, nil, nil, nil, nil)
	if _, err := db.ExecContext(ctx, attemptInsertSQL, "preserved-attempt", "preserved-delivery", "correlation", 1, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Compare every stored runtime column before and after upgrade and reopen.
	tables := []string{"runtime_cursors", "processed_updates", "outbound_deliveries", "outbound_attempts", "scheduled_jobs"}
	before := make(map[string]string)
	for _, table := range tables {
		before[table] = schemaRowsJSON(t, db, table)
	}
	if err := db.Migrate(ctx, runtimeMigrationsForTest(t)); err != nil {
		t.Fatal(err)
	}
	assertLedgerVersions(t, db, []int{1, 2, 3})
	assertRowCount(t, db, "characters", "1=1", 0)
	if _, err := db.ExecContext(ctx, characterInsertSQL, schemaCharacterID, 42, 100, 0, 10, 0, 1000, 20, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, operationInsertSQL, "persisted", schemaCharacterID, "damage", 100, 2); err != nil {
		t.Fatal(err)
	}
	characterBefore := schemaRowsJSON(t, db, "characters")
	operationsBefore := schemaRowsJSON(t, db, "character_operations")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTestDB(t, dir, testBusyTimeout)
	if err := db.Migrate(ctx, runtimeMigrationsForTest(t)); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if got := schemaRowsJSON(t, db, table); got != before[table] {
			t.Errorf("%s changed: %s -> %s", table, before[table], got)
		}
	}
	if got := schemaRowsJSON(t, db, "characters"); got != characterBefore {
		t.Errorf("character changed on reopen")
	}
	if got := schemaRowsJSON(t, db, "character_operations"); got != operationsBefore {
		t.Errorf("receipts changed on reopen")
	}
	var compatibility *SchemaCompatibilityError
	if err := db.Migrate(ctx, original); !errors.As(err, &compatibility) || compatibility.Reason != SchemaCompatibilityFuture {
		t.Fatalf("old binary migration = %v", err)
	}
	assertLedgerVersions(t, db, []int{1, 2, 3})
}

func schemaRowsJSON(t *testing.T, db *DB, table string) string {
	t.Helper()
	var got string
	// Test-owned table names; JSON array preserves all columns and SQLite values.
	columns := tableColumns(t, db, table)
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		names = append(names, "quote("+column.name+")")
	}
	if err := db.sql.QueryRowContext(context.Background(), `SELECT json_group_array(json_array(`+strings.Join(names, ",")+`)) FROM `+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCharacterSchemaRejectedStateRollsBackWholeTransaction(t *testing.T) {
	db := migratedRuntimeDB(t)
	ctx := context.Background()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, characterInsertSQL, schemaCharacterID, 42, 100, 100, 10, 0, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, operationInsertSQL, "prefix", schemaCharacterID, "heal", 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE characters SET health=0 WHERE id=?`, schemaCharacterID); err == nil {
		t.Fatal("partial incapacitation accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, db, "characters", "1=1", 0)
	assertRowCount(t, db, "character_operations", "1=1", 0)
}

func TestCharacterMigrationFailureRollsBackSchemaAndLedger(t *testing.T) {
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	ctx := context.Background()
	original, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, original); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE character_operations(sentinel TEXT); INSERT INTO character_operations VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}
	err = db.Migrate(ctx, runtimeMigrationsForTest(t))
	var migrationErr *MigrationError
	if !errors.As(err, &migrationErr) || migrationErr.Version != 2 || migrationErr.Stage != MigrationStageApply {
		t.Fatalf("migration failure = %v", err)
	}
	assertTableExists(t, db, "characters", false)
	assertLedgerVersions(t, db, []int{1})
	assertRowCount(t, db, "character_operations", "sentinel='preserved'", 1)
}
