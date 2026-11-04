package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSharingSchemaConstraints(t *testing.T) {
	db := migratedRuntimeDB(t)
	ctx := context.Background()
	token := strings.Repeat("a", 32)
	_, err := db.ExecContext(ctx, `INSERT INTO sharing_views(token,owner_id,chat_id,chat_kind,view_kind,locale,fingerprint_version,body_hash,created_at) VALUES(?,42,-1,'supergroup','inventory_page','en',1,?,1)`, token, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE sharing_views SET owner_id=0`, `UPDATE sharing_views SET chat_id=1`, `UPDATE sharing_views SET token='bad'`, `UPDATE sharing_views SET body_hash=NULL`, `UPDATE sharing_views SET body_hash=x'01'`, `UPDATE sharing_views SET retired=1`, `UPDATE sharing_views SET locale='fr'`, `UPDATE sharing_views SET view_kind='bad'`, `UPDATE sharing_views SET fingerprint_version=2`, `UPDATE sharing_views SET bound_message_id=0`} {
		assertStatementFails(t, db, q)
	}
	insert := `INSERT INTO sharing_actions(update_id,view_token,public_delivery_id,accepted_at,next_check_at) VALUES(?,?,?,1,1)`
	if _, err = db.ExecContext(ctx, insert, 1, token, "public1"); err != nil {
		t.Fatal(err)
	}
	assertStatementFails(t, db, insert, 2, token, "public2")
	assertStatementFails(t, db, insert, 1, token, "public2")
	assertStatementFails(t, db, insert, 3, strings.Repeat("f", 32), "orphan-public")
	assertStatementFails(t, db, `UPDATE sharing_actions SET public_state='succeeded'`)
	assertStatementFails(t, db, `UPDATE sharing_actions SET public_state='succeeded',public_outcome_at=2`)
	assertStatementFails(t, db, `UPDATE sharing_actions SET public_state='succeeded',public_outcome_at=2,public_purge_after=86400001`)
	if _, err = db.ExecContext(ctx, `UPDATE sharing_actions SET public_state='succeeded',public_outcome_at=2,public_purge_after=86400002 WHERE update_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, insert, 2, token, "public2"); err != nil {
		t.Fatal(err)
	}
	assertStatementFails(t, db, `UPDATE sharing_actions SET public_purged=1 WHERE update_id=2`)
	assertStatementFails(t, db, `UPDATE sharing_actions SET feedback_state='queued' WHERE update_id=1`)
	assertStatementFails(t, db, `UPDATE sharing_actions SET feedback_state='completed',feedback_delivery_id='feedback',feedback_outcome_at=3 WHERE update_id=1`)
	if _, err = db.ExecContext(ctx, `UPDATE sharing_views SET retired=1,body_hash=NULL`); err != nil {
		t.Fatal(err)
	}
	assertStatementFails(t, db, `UPDATE sharing_views SET body_hash=zeroblob(32)`)
	assertRowCount(t, db, "sharing_actions", "update_id=1 AND public_delivery_id='public1'", 1)
}

func TestSharingUpgradePreservesSchema4Data(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openTestDB(t, dir, testBusyTimeout)
	old, err := NewMigrationSet(runtimeMigrationFiles, "migrations/0001_runtime.sql", "migrations/0002_characters.sql", "migrations/0003_inventory.sql", "migrations/0004_localization.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,80,10,5,NULL,NULL,1)`, `INSERT INTO inventory_catalog VALUES('coin','inert',0,0,0)`, `INSERT INTO inventories VALUES('0123456789abcdef0123456789abcdef',1,1)`, `INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','coin',10)`, `INSERT INTO group_settings VALUES(-1,'ru',2,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',NULL,NULL,1,0,NULL,1)`, `INSERT INTO settings_controls VALUES('0123456789abcdef0123456789abcdef',-1,42,2,'en',100,0)`} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	tables := []string{"characters", "inventories", "inventory_items", "inventory_catalog", "group_settings", "settings_controls", "processed_updates"}
	before := map[string]string{}
	for _, table := range tables {
		before[table] = schemaRowsJSON(t, db, table)
	}
	if err = db.Migrate(ctx, runtimeMigrationsForTest(t)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTestDB(t, dir, testBusyTimeout)
	for _, table := range tables {
		if schemaRowsJSON(t, db, table) != before[table] {
			t.Fatalf("changed %s", table)
		}
	}
	assertTableExists(t, db, "sharing_views", true)
	assertTableExists(t, db, "sharing_actions", true)
	var compatibility *SchemaCompatibilityError
	if err = db.Migrate(ctx, old); !errors.As(err, &compatibility) || !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("old binary accepted schema5: %v", err)
	}
}
