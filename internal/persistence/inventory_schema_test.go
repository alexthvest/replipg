package persistence

import (
	"context"
	"errors"
	"testing"
)

func TestInventorySchemaConstraints(t *testing.T) {
	db := migratedRuntimeDB(t)
	ctx := context.Background()
	statements := []string{
		`INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,100,10,5,NULL,NULL,1)`,
		`INSERT INTO inventory_catalog VALUES('stick','weapon',2,0,0),('armor','armor',0,2,0),('potion','consumable',0,0,25),('coin','inert',0,0,0)`,
		`INSERT INTO inventories VALUES('0123456789abcdef0123456789abcdef',1,0)`,
		`INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','stick',1),('0123456789abcdef0123456789abcdef','coin',2)`,
		`INSERT INTO inventory_equipment VALUES('0123456789abcdef0123456789abcdef','weapon','stick')`,
	}
	for _, sql := range statements {
		if _, err := db.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}

	for _, sql := range []string{
		`INSERT INTO inventory_catalog VALUES('bad','inert',1,0,0)`,
		`INSERT INTO inventory_catalog VALUES('bad','weapon',0,1,0)`,
		`INSERT INTO inventory_catalog VALUES('bad','consumable',0,0,0)`,
		`INSERT INTO inventory_catalog VALUES('bad.id','inert',0,0,0)`,
		`INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','potion',0)`,
		`INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','potion',1.5)`,
		`INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','undefined',1)`,
		`UPDATE inventories SET revision=0`,
		`UPDATE inventories SET starter_granted=2`,
		`INSERT INTO inventory_equipment VALUES('0123456789abcdef0123456789abcdef','armor','stick')`,
		`INSERT INTO inventory_equipment VALUES('0123456789abcdef0123456789abcdef','armor','armor')`,
		`UPDATE inventory_equipment SET item_id='coin'`,
		`DELETE FROM inventory_items WHERE item_id='stick'`,
		`DELETE FROM inventory_catalog WHERE item_id='stick'`,
	} {
		assertStatementFails(t, db, sql)
	}
}

func TestInventoryMigrationFromVersionTwoAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openTestDB(t, dir, testBusyTimeout)
	old, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql", "migrations/0002_characters.sql")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,80,10,5,NULL,NULL,1)`); err != nil {
		t.Fatal(err)
	}

	before := schemaRowsJSON(t, db, "characters")
	if err := db.Migrate(ctx, runtimeMigrationsForTest(t)); err != nil {
		t.Fatal(err)
	}

	assertLedgerVersions(t, db, []int{1, 2, 3, 4, 5})
	assertRowCount(t, db, "inventories", "1=1", 0)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openTestDB(t, dir, testBusyTimeout)
	if got := schemaRowsJSON(t, db, "characters"); got != before {
		t.Fatal("upgrade modified character")
	}

	var compatibility *SchemaCompatibilityError

	if err := db.Migrate(ctx, old); !errors.As(err, &compatibility) || compatibility.Reason != SchemaCompatibilityFuture {
		t.Fatalf("old schema accepted: %v", err)
	}
}

func TestInventoryMigrationFailureRollsBackOnlyNewSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	old, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql", "migrations/0002_characters.sql")
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE inventory_operations(sentinel TEXT); INSERT INTO inventory_operations VALUES('preserved')`); err != nil {
		t.Fatal(err)
	}

	var migration *MigrationError

	if err := db.Migrate(ctx, runtimeMigrationsForTest(t)); !errors.As(err, &migration) || migration.Version != 3 || migration.Stage != MigrationStageApply {
		t.Fatalf("migration failure=%v", err)
	}

	assertLedgerVersions(t, db, []int{1, 2})
	assertTableExists(t, db, "inventories", false)
	assertRowCount(t, db, "inventory_operations", "sentinel='preserved'", 1)
}

func TestInventoryReceiptAndControlConstraints(t *testing.T) {
	db := migratedRuntimeDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,100,10,5,NULL,NULL,1); INSERT INTO inventory_catalog VALUES('stick','weapon',1,0,0)`); err != nil {
		t.Fatal(err)
	}

	for _, sql := range []string{
		`INSERT INTO inventory_operations VALUES('op','0123456789abcdef0123456789abcdef','consume','{}',1,NULL,25)`,
		`INSERT INTO inventory_operations VALUES('op','0123456789abcdef0123456789abcdef','consume','{}',1,'heal_self_fixed_hp',NULL)`,
		`INSERT INTO inventory_operations VALUES('op','0123456789abcdef0123456789abcdef','grant','{}',1,'heal_self_fixed_hp',25)`,
		`INSERT INTO inventory_operations VALUES('op','0123456789abcdef0123456789abcdef','grant','{}',0,NULL,NULL)`,
		`INSERT INTO inventory_controls VALUES('abcdefghijklmnopqrstuv',42,-1,'0123456789abcdef0123456789abcdef','detail',NULL,NULL,0,1,1000)`,
		`INSERT INTO inventory_controls VALUES('abcdefghijklmnopqrstuv',42,-1,'0123456789abcdef0123456789abcdef','equip','stick',NULL,0,1,1000)`,
		`INSERT INTO inventory_controls VALUES('short',42,-1,'0123456789abcdef0123456789abcdef','page',NULL,NULL,0,1,1000)`,
	} {
		assertStatementFails(t, db, sql)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO inventory_operations VALUES('op','0123456789abcdef0123456789abcdef','consume','{}',1,'heal_self_fixed_hp',25)`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO inventory_operations VALUES('grant','0123456789abcdef0123456789abcdef','grant','{}',1,NULL,NULL); INSERT INTO inventory_controls VALUES('abcdefghijklmnopqrstuv',42,-1,'0123456789abcdef0123456789abcdef','page',NULL,NULL,0,1,1000)`); err != nil {
		t.Fatal(err)
	}
}
