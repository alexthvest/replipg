package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGroupsPersistenceAndConstraints(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "game.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = db.SetAdmin(ctx, -1, 10, true); err != nil {
		t.Fatal(err)
	}

	if _, err = db.SetLanguage(ctx, -1, "ru"); err != nil {
		t.Fatal(err)
	}

	if _, err = db.SetLanguage(ctx, -2, "bad"); err == nil {
		t.Fatal("invalid language accepted")
	}

	if _, err = db.db.ExecContext(ctx, "INSERT INTO game_admins VALUES(-999,1)"); err == nil {
		t.Fatal("foreign key disabled")
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = db.Close()
	}()

	state, err := db.Group(ctx, -1)
	if err != nil || state.Language != "ru" || len(state.Admins) != 1 || state.Admins[0] != 10 {
		t.Fatalf("reopen: %+v %v", state, err)
	}

	state, err = db.Group(ctx, -2)
	if err != nil || state.Language != "en" || len(state.Admins) != 0 {
		t.Fatalf("rollback/isolation: %+v %v", state, err)
	}

	all, err := db.LoadGroups(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("load: %+v %v", all, err)
	}

	if _, err = db.db.ExecContext(ctx, "PRAGMA user_version=999"); err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err = Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("newer schema: %v", err)
	}
}

func TestMigrationRollback(t *testing.T) {
	ctx := context.Background()
	raw, err := openDriver(filepath.Join(t.TempDir(), "rollback.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = raw.Close()
	}()

	db := &Database{db: raw}
	source := fstest.MapFS{"migrations/001_test.sql": {Data: []byte("CREATE TABLE first(id INTEGER); INVALID SQL;")}}
	if err = db.migrate(ctx, source); err == nil {
		t.Fatal("bad migration succeeded")
	}

	var count, version int
	if err = raw.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='first'").Scan(&count); err != nil {
		t.Fatal(err)
	}

	if err = raw.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}

	if count != 0 || version != 0 {
		t.Fatalf("partial migration: %d %d", count, version)
	}
}
