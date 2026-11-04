package persistence

import (
	"context"
	"errors"
	"testing"
)

func TestLocalizationSchemaConstraints(t *testing.T) {
	db := migratedRuntimeDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO group_settings VALUES(-1,'en',1,?,NULL,NULL,0,0,NULL,0)`, string(makeHex())); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE group_settings SET locale='fr'`, `UPDATE group_settings SET revision=0`, `UPDATE group_settings SET desired_menu_hash='bad'`, `UPDATE group_settings SET applied_revision=1`, `UPDATE group_settings SET menu_attempts=-1`, `INSERT INTO settings_controls VALUES('short',-1,1,1,'en',100,0)`, `UPDATE group_settings SET chat_id=1`} {
		assertStatementFails(t, db, q)
	}
}
func makeHex() []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return b
}

func TestLocalizationUpgradePreservesGameplayAndReopens(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openTestDB(t, dir, testBusyTimeout)
	old, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql", "migrations/0002_characters.sql", "migrations/0003_inventory.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,80,10,5,NULL,NULL,1)`, `INSERT INTO inventory_catalog VALUES('coin','inert',0,0,0)`, `INSERT INTO inventories VALUES('0123456789abcdef0123456789abcdef',1,1)`, `INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','coin',10)`} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"characters", "inventories", "inventory_items", "inventory_catalog"}
	before := map[string]string{}
	for _, table := range tables {
		before[table] = schemaRowsJSON(t, db, table)
	}
	if err = db.Migrate(ctx, runtimeMigrationsForTest(t)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO group_settings VALUES(-1,'ru',2,?,NULL,NULL,1,0,NULL,1); INSERT INTO settings_controls VALUES('0123456789abcdef0123456789abcdef',-1,42,2,'en',100,0)`, string(makeHex())); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTestDB(t, dir, testBusyTimeout)
	for _, table := range tables {
		if got := schemaRowsJSON(t, db, table); got != before[table] {
			t.Fatalf("upgrade changed %s", table)
		}
	}
	assertRowCount(t, db, "settings_controls", "actor_id=42", 1)
	assertRowCount(t, db, "group_settings", "locale='ru' AND revision=2", 1)
	var compatibility *SchemaCompatibilityError
	if err = db.Migrate(ctx, old); !errors.As(err, &compatibility) || compatibility.Reason != SchemaCompatibilityFuture {
		t.Fatalf("schema-3 binary accepted schema-4: %v", err)
	}
}
