package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRuntimeMigrationsApplyProductionSchemaIdempotentlyAndSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	set := runtimeMigrationsForTest(t)
	db := openTestDB(t, dir, testBusyTimeout)

	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	assertRuntimeSchemaObjects(t, db)
	assertLedgerVersions(t, db, []int{1, 2, 3})

	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	assertLedgerVersions(t, db, []int{1, 2, 3})

	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db = openTestDB(t, dir, testBusyTimeout)
	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() after reopen error = %v", err)
	}
	assertRuntimeSchemaObjects(t, db)
	assertLedgerVersions(t, db, []int{1, 2, 3})
}

func TestRuntimeSchemaColumnsAndRelationships(t *testing.T) {
	db := migratedRuntimeDB(t)
	expected := map[string][]schemaColumn{
		"runtime_cursors": {
			{"source", "TEXT", 1, 1}, {"last_completed_id", "INTEGER", 1, 0}, {"updated_at", "INTEGER", 1, 0},
		},
		"processed_updates": {
			{"update_id", "INTEGER", 1, 1}, {"status", "TEXT", 1, 0}, {"correlation_id", "TEXT", 1, 0}, {"failure_code", "TEXT", 0, 0}, {"completed_at", "INTEGER", 1, 0},
		},
		"outbound_deliveries": {
			{"id", "TEXT", 1, 1}, {"kind", "TEXT", 1, 0}, {"content_version", "INTEGER", 1, 0}, {"content", "BLOB", 1, 0}, {"destination_chat_id", "INTEGER", 1, 0}, {"target_user_id", "INTEGER", 0, 0}, {"authorization_kind", "TEXT", 0, 0}, {"callback_query_id", "TEXT", 0, 0}, {"incoming_ephemeral_message_id", "INTEGER", 0, 0}, {"authorization_expires_at", "INTEGER", 0, 0}, {"replace_callback_message", "INTEGER", 1, 0}, {"state", "TEXT", 1, 0}, {"max_attempts", "INTEGER", 1, 0}, {"initial_delay_ms", "INTEGER", 1, 0}, {"max_delay_ms", "INTEGER", 1, 0}, {"attempt_count", "INTEGER", 1, 0}, {"next_attempt_at", "INTEGER", 0, 0}, {"reason_code", "TEXT", 0, 0}, {"created_at", "INTEGER", 1, 0}, {"updated_at", "INTEGER", 1, 0}, {"terminal_at", "INTEGER", 0, 0},
		},
		"outbound_attempts": {
			{"id", "TEXT", 1, 1}, {"delivery_id", "TEXT", 1, 0}, {"correlation_id", "TEXT", 1, 0}, {"started_at", "INTEGER", 1, 0}, {"finished_at", "INTEGER", 0, 0}, {"outcome", "TEXT", 0, 0}, {"reason_code", "TEXT", 0, 0},
		},
		"scheduled_jobs": {
			{"id", "TEXT", 1, 1}, {"job_type", "TEXT", 1, 0}, {"payload_version", "INTEGER", 1, 0}, {"payload", "BLOB", 1, 0}, {"due_at", "INTEGER", 1, 0}, {"state", "TEXT", 1, 0}, {"max_attempts", "INTEGER", 1, 0}, {"initial_delay_ms", "INTEGER", 1, 0}, {"max_delay_ms", "INTEGER", 1, 0}, {"attempt_count", "INTEGER", 1, 0}, {"next_attempt_at", "INTEGER", 0, 0}, {"claim_owner", "TEXT", 0, 0}, {"claim_correlation_id", "TEXT", 0, 0}, {"lease_until", "INTEGER", 0, 0}, {"reason_code", "TEXT", 0, 0}, {"created_at", "INTEGER", 1, 0}, {"updated_at", "INTEGER", 1, 0}, {"terminal_at", "INTEGER", 0, 0},
		},
	}
	for table, want := range expected {
		if got := tableColumns(t, db, table); !slices.Equal(got, want) {
			t.Errorf("%s columns = %#v, want %#v", table, got, want)
		}
	}

	rows, err := db.sql.QueryContext(context.Background(), `PRAGMA foreign_key_list('outbound_attempts')`)
	if err != nil {
		t.Fatalf("inspect attempt foreign keys: %v", err)
	}
	defer closeRows(t, rows)
	if !rows.Next() {
		t.Fatal("outbound_attempts delivery foreign key missing")
	}
	var id, seq int
	var table, from, to, onUpdate, onDelete, match string
	if err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
		t.Fatalf("scan attempt foreign key: %v", err)
	}
	if table != "outbound_deliveries" || from != "delivery_id" || to != "id" || onDelete != "RESTRICT" {
		t.Fatalf("attempt foreign key = table %q from %q to %q delete %q", table, from, to, onDelete)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra attempt foreign key or row error: %v", rows.Err())
	}
}

func TestRuntimeSchemaDeliveryRoutingAttemptOutcomesAndExhaustion(t *testing.T) {
	db := migratedRuntimeDB(t)

	routingCases := []struct {
		name                                      string
		target, auth, callback, incoming, expires any
		replace                                   int
	}{
		{name: "public"},
		{name: "callback", target: int64(42), auth: "callback", callback: "callback-id", expires: int64(200), replace: 1},
		{name: "incoming ephemeral", target: int64(42), auth: "incoming_ephemeral", incoming: int64(7), expires: int64(200)},
		{name: "administrator", target: int64(42), auth: "administrator"},
	}
	for index, value := range routingCases {
		insertRoutedDelivery(t, db, fmt.Sprintf("routing-%d", index), value.target, value.auth, value.callback, value.incoming, value.expires, value.replace, "pending", 0, 3)
	}

	invalidRouting := []struct {
		name                                      string
		destination                               int64
		target, auth, callback, incoming, expires any
		replace                                   int
	}{
		{name: "nongroup destination", destination: 10},
		{name: "public with target", destination: -10, target: int64(42)},
		{name: "authorization without target", destination: -10, auth: "callback", callback: "callback-id", expires: int64(200)},
		{name: "callback missing reference", destination: -10, target: int64(42), auth: "callback", expires: int64(200)},
		{name: "callback mixed incoming", destination: -10, target: int64(42), auth: "callback", callback: "callback-id", incoming: int64(7), expires: int64(200)},
		{name: "incoming missing expiry", destination: -10, target: int64(42), auth: "incoming_ephemeral", incoming: int64(7)},
		{name: "administrator with transient reference", destination: -10, target: int64(42), auth: "administrator", callback: "callback-id"},
		{name: "replacement outside callback", destination: -10, target: int64(42), auth: "administrator", replace: 1},
		{name: "invalid authorization", destination: -10, target: int64(42), auth: "secret"},
	}
	for index, value := range invalidRouting {
		t.Run(value.name, func(t *testing.T) {
			assertStatementFails(t, db, routedDeliveryInsertSQL, fmt.Sprintf("invalid-routing-%d", index), "kind", 1, []byte("content"), value.destination, value.target, value.auth, value.callback, value.incoming, value.expires, value.replace, "pending", 3, 1, 10, 0, int64(1), nil, 1, 1, nil)
		})
	}

	insertRoutedDelivery(t, db, "attempt-parent", nil, nil, nil, nil, nil, 0, "pending", 0, 3)
	attemptCases := []struct {
		outcome string
		reason  any
	}{
		{"accepted", nil},
		{"retryable_failure", "transient"},
		{"permanent_failure", "rejected"},
		{"indeterminate", "unknown"},
		{"cancelled", nil},
	}
	for index, value := range attemptCases {
		if _, err := db.ExecContext(context.Background(), attemptInsertSQL, fmt.Sprintf("attempt-%d", index), "attempt-parent", fmt.Sprintf("correlation-%d", index), 10, 20, value.outcome, value.reason); err != nil {
			t.Errorf("insert attempt outcome %q: %v", value.outcome, err)
		}
	}
	assertStatementFails(t, db, attemptInsertSQL, "attempt-invalid", "attempt-parent", "correlation", 10, 20, "made_up", nil)
	assertStatementFails(t, db, attemptInsertSQL, "attempt-missing-reason", "attempt-parent", "correlation", 10, 20, "permanent_failure", nil)
	assertStatementFails(t, db, attemptInsertSQL, "attempt-unfinished-outcome", "attempt-parent", "correlation", 10, nil, "accepted", nil)
	assertStatementFails(t, db, attemptInsertSQL, "attempt-unfinished-reason", "attempt-parent", "correlation", 10, nil, nil, "unexpected")
	assertStatementFails(t, db, attemptInsertSQL, "attempt-finished-no-outcome", "attempt-parent", "correlation", 10, 20, nil, nil)
	assertStatementFails(t, db, attemptInsertSQL, "attempt-accepted-reason", "attempt-parent", "correlation", 10, 20, "accepted", "unexpected")

	assertStatementFails(t, db, routedDeliveryInsertSQL, "exhausted-pending", "kind", 1, []byte("content"), -10, nil, nil, nil, nil, nil, 0, "pending", 3, 1, 10, 3, int64(1), nil, 1, 1, nil)
	assertStatementFails(t, db, routedDeliveryInsertSQL, "exhausted-retrying", "kind", 1, []byte("content"), -10, nil, nil, nil, nil, nil, 0, "retrying", 3, 1, 10, 3, int64(1), nil, 1, 1, nil)
	insertRoutedDelivery(t, db, "exhausted-attempting", nil, nil, nil, nil, nil, 0, "attempting", 3, 3)
	insertRoutedDelivery(t, db, "exhausted-terminal", nil, nil, nil, nil, nil, 0, "permanent_failure", 3, 3)
	assertStatementFails(t, db, jobInsertSQL, "exhausted-job-pending", "type", 1, []byte("payload"), 10, "pending", 3, 1, 10, 3, nil, nil, nil, nil, nil, 1, 1, nil)
	assertStatementFails(t, db, jobInsertSQL, "exhausted-job-retrying", "type", 1, []byte("payload"), 10, "retrying", 3, 1, 10, 3, int64(20), nil, nil, nil, nil, 1, 1, nil)
	insertJobWithAttempts(t, db, "exhausted-job-claimed", "claimed", 3, nil, "worker", "correlation", int64(20), nil, nil)
	insertJobWithAttempts(t, db, "exhausted-job-terminal", "permanent_failure", 3, nil, nil, nil, nil, "exhausted", int64(30))
}

func TestRuntimeSchemaAcceptsOnlyAuthoritativeStates(t *testing.T) {
	db := migratedRuntimeDB(t)
	processed := []struct {
		state, reason string
	}{
		{"completed", ""}, {"ignored", ""}, {"decode_failed", "decode"}, {"handler_failed", "handler"},
	}
	for index, value := range processed {
		_, err := db.ExecContext(context.Background(), `INSERT INTO processed_updates(update_id, status, correlation_id, failure_code, completed_at) VALUES (?, ?, ?, NULLIF(?, ''), ?)`, index+1, value.state, fmt.Sprintf("correlation-%d", index), value.reason, 1000+index)
		if err != nil {
			t.Errorf("insert processed state %q: %v", value.state, err)
		}
	}
	assertStatementFails(t, db, `INSERT INTO processed_updates(update_id, status, correlation_id, completed_at) VALUES (99, 'failed', 'correlation', 1)`)
	assertStatementFails(t, db, `INSERT INTO processed_updates(update_id, status, correlation_id, completed_at) VALUES (100, 'handler_failed', 'correlation', 1)`)

	deliveryStates := []struct {
		state    string
		next     any
		reason   any
		terminal any
	}{
		{"pending", int64(10), nil, nil}, {"attempting", nil, nil, nil}, {"retrying", int64(20), nil, nil}, {"reconciliation_required", nil, "uncertain", nil}, {"completed", nil, nil, int64(30)}, {"permanent_failure", nil, "exhausted", int64(40)},
	}
	for index, value := range deliveryStates {
		insertDelivery(t, db, fmt.Sprintf("delivery-%d", index), value.state, value.next, value.reason, value.terminal)
	}
	assertStatementFails(t, db, deliveryInsertSQL, "delivery-invalid", "kind", 1, []byte("content"), "cancelled", 3, 1, 10, 0, nil, nil, 1, 1, nil)
	assertStatementFails(t, db, deliveryInsertSQL, "delivery-no-due", "kind", 1, []byte("content"), "pending", 3, 1, 10, 0, nil, nil, 1, 1, nil)
	assertStatementFails(t, db, deliveryInsertSQL, "delivery-no-reason", "kind", 1, []byte("content"), "permanent_failure", 3, 1, 10, 0, nil, nil, 1, 1, int64(2))
	assertStatementFails(t, db, deliveryInsertSQL, "delivery-bad-retry", "kind", 0, []byte("content"), "pending", 0, 0, 0, -1, int64(1), nil, 1, 1, nil)

	jobStates := []struct {
		state                                             string
		next, owner, correlation, lease, reason, terminal any
	}{
		{"pending", nil, nil, nil, nil, nil, nil},
		{"claimed", nil, "worker", "correlation", int64(20), nil, nil},
		{"retrying", int64(20), nil, nil, nil, nil, nil},
		{"completed", nil, nil, nil, nil, nil, int64(30)},
		{"cancelled", nil, nil, nil, nil, nil, int64(30)},
		{"permanent_failure", nil, nil, nil, nil, "exhausted", int64(30)},
	}
	for index, value := range jobStates {
		insertJob(t, db, fmt.Sprintf("job-%d", index), value.state, value.next, value.owner, value.correlation, value.lease, value.reason, value.terminal)
	}
	assertStatementFails(t, db, jobInsertSQL, "job-invalid", "type", 1, []byte("payload"), 10, "attempting", 3, 1, 10, 0, nil, nil, nil, nil, nil, 1, 1, nil)
	assertStatementFails(t, db, jobInsertSQL, "job-incomplete-claim", "type", 1, []byte("payload"), 10, "claimed", 3, 1, 10, 0, nil, "worker", nil, int64(20), nil, 1, 1, nil)
	assertStatementFails(t, db, jobInsertSQL, "job-early-retry", "type", 1, []byte("payload"), 10, "retrying", 3, 1, 10, 0, nil, nil, nil, nil, nil, 1, 1, nil)
	assertStatementFails(t, db, jobInsertSQL, "job-no-reason", "type", 1, []byte("payload"), 10, "permanent_failure", 3, 1, 10, 0, nil, nil, nil, nil, nil, 1, 1, int64(2))
}

func TestRuntimeSchemaRejectsInvalidRelationshipsAndRollsBackPrefixes(t *testing.T) {
	db := migratedRuntimeDB(t)

	tx, err := db.sql.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), deliveryInsertSQL, "prefix-delivery", "kind", 1, []byte("content"), "pending", 3, 1, 10, 0, int64(1), nil, 1, 1, nil); err != nil {
		t.Fatalf("insert valid transaction prefix: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), `INSERT INTO outbound_attempts(id, delivery_id, correlation_id, started_at) VALUES ('orphan-attempt', 'missing-delivery', 'correlation', 1)`); err == nil {
		t.Fatal("orphan attempt insert succeeded")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	assertRowCount(t, db, "outbound_deliveries", "id = 'prefix-delivery'", 0)
	assertRowCount(t, db, "outbound_attempts", "id = 'orphan-attempt'", 0)

	insertDelivery(t, db, "owned-delivery", "pending", int64(1), nil, nil)
	if _, err := db.ExecContext(context.Background(), `INSERT INTO outbound_attempts(id, delivery_id, correlation_id, started_at) VALUES ('owned-attempt', 'owned-delivery', 'correlation', 1)`); err != nil {
		t.Fatalf("insert owned attempt: %v", err)
	}
	assertStatementFails(t, db, `DELETE FROM outbound_deliveries WHERE id = 'owned-delivery'`)
	assertRowCount(t, db, "outbound_deliveries", "id = 'owned-delivery'", 1)
	assertRowCount(t, db, "outbound_attempts", "id = 'owned-attempt'", 1)

	tx, err = db.sql.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), jobInsertSQL, "prefix-job", "type", 1, []byte("payload"), 10, "pending", 3, 1, 10, 0, nil, nil, nil, nil, nil, 1, 1, nil); err != nil {
		t.Fatalf("insert valid check prefix: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), jobInsertSQL, "invalid-job", "type", 1, []byte("payload"), 10, "unknown", 3, 1, 10, 0, nil, nil, nil, nil, nil, 1, 1, nil); err == nil {
		t.Fatal("invalid job state insert succeeded")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	assertRowCount(t, db, "scheduled_jobs", "id IN ('prefix-job', 'invalid-job')", 0)

	tx, err = db.sql.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), routedDeliveryInsertSQL, "routing-prefix", "kind", 1, []byte("content"), -10, nil, nil, nil, nil, nil, 0, "pending", 3, 1, 10, 0, int64(1), nil, 1, 1, nil); err != nil {
		t.Fatalf("insert valid routing prefix: %v", err)
	}
	if _, err := tx.ExecContext(context.Background(), routedDeliveryInsertSQL, "routing-mixed", "kind", 1, []byte("content"), -10, int64(42), "administrator", "callback-id", nil, nil, 0, "pending", 3, 1, 10, 0, int64(1), nil, 1, 1, nil); err == nil {
		t.Fatal("mixed routing facts insert succeeded")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	assertRowCount(t, db, "outbound_deliveries", "id IN ('routing-prefix', 'routing-mixed')", 0)
}

func TestRuntimeSchemaDueIndexesExcludeOpaqueData(t *testing.T) {
	db := migratedRuntimeDB(t)
	want := map[string][]string{
		"outbound_deliveries_pending_due_idx": {"state", "next_attempt_at"},
		"outbound_deliveries_retry_due_idx":   {"state", "next_attempt_at"},
		"outbound_attempts_delivery_idx":      {"delivery_id", "started_at"},
		"scheduled_jobs_pending_due_idx":      {"state", "due_at"},
		"scheduled_jobs_retry_due_idx":        {"state", "next_attempt_at"},
	}
	for index, columns := range want {
		got := indexColumns(t, db, index)
		if !slices.Equal(got, columns) {
			t.Errorf("index %s columns = %v, want %v", index, got, columns)
		}
		for _, excluded := range []string{"content", "payload", "destination_chat_id", "target_user_id", "authorization_kind", "callback_query_id", "incoming_ephemeral_message_id", "authorization_expires_at", "replace_callback_message"} {
			if slices.Contains(got, excluded) {
				t.Errorf("index %s includes status-excluded field %q: %v", index, excluded, got)
			}
		}
	}
	assertTableIndexes(t, db, "outbound_deliveries", []string{"outbound_deliveries_pending_due_idx", "outbound_deliveries_retry_due_idx", "sqlite_autoindex_outbound_deliveries_1"})
	assertTableIndexes(t, db, "scheduled_jobs", []string{"scheduled_jobs_pending_due_idx", "scheduled_jobs_retry_due_idx", "sqlite_autoindex_scheduled_jobs_1"})

	for _, check := range []struct{ query, index string }{
		{`SELECT id, state, next_attempt_at FROM outbound_deliveries WHERE state = 'pending' AND next_attempt_at <= 100 ORDER BY next_attempt_at`, "outbound_deliveries_pending_due_idx"},
		{`SELECT id, state, next_attempt_at FROM outbound_deliveries WHERE state = 'retrying' AND next_attempt_at <= 100 ORDER BY next_attempt_at`, "outbound_deliveries_retry_due_idx"},
		{`SELECT id, state, due_at FROM scheduled_jobs WHERE state = 'pending' AND due_at <= 100 ORDER BY due_at`, "scheduled_jobs_pending_due_idx"},
		{`SELECT id, state, next_attempt_at FROM scheduled_jobs WHERE state = 'retrying' AND next_attempt_at <= 100 ORDER BY next_attempt_at`, "scheduled_jobs_retry_due_idx"},
	} {
		var detail string
		if err := db.sql.QueryRowContext(context.Background(), `EXPLAIN QUERY PLAN `+check.query).Scan(new(int), new(int), new(int), &detail); err != nil {
			t.Fatalf("explain due query: %v", err)
		}
		if !strings.Contains(detail, check.index) {
			t.Errorf("query plan %q does not use %s", detail, check.index)
		}
		for _, excluded := range []string{"content", "payload", "destination_chat_id", "target_user_id", "authorization_kind", "callback_query_id", "incoming_ephemeral_message_id", "authorization_expires_at", "replace_callback_message"} {
			if strings.Contains(check.query, excluded) {
				t.Errorf("status-facing due query loads excluded field %q: %s", excluded, check.query)
			}
		}
	}
}

type schemaColumn struct {
	name, columnType    string
	notNull, primaryKey int
}

const deliveryInsertSQL = `INSERT INTO outbound_deliveries(id, kind, content_version, content, destination_chat_id, replace_callback_message, state, max_attempts, initial_delay_ms, max_delay_ms, attempt_count, next_attempt_at, reason_code, created_at, updated_at, terminal_at) VALUES (?, ?, ?, ?, -10, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const routedDeliveryInsertSQL = `INSERT INTO outbound_deliveries(id, kind, content_version, content, destination_chat_id, target_user_id, authorization_kind, callback_query_id, incoming_ephemeral_message_id, authorization_expires_at, replace_callback_message, state, max_attempts, initial_delay_ms, max_delay_ms, attempt_count, next_attempt_at, reason_code, created_at, updated_at, terminal_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
const attemptInsertSQL = `INSERT INTO outbound_attempts(id, delivery_id, correlation_id, started_at, finished_at, outcome, reason_code) VALUES (?, ?, ?, ?, ?, ?, ?)`
const jobInsertSQL = `INSERT INTO scheduled_jobs(id, job_type, payload_version, payload, due_at, state, max_attempts, initial_delay_ms, max_delay_ms, attempt_count, next_attempt_at, claim_owner, claim_correlation_id, lease_until, reason_code, created_at, updated_at, terminal_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func runtimeMigrationsForTest(t *testing.T) MigrationSet {
	t.Helper()
	set, err := RuntimeMigrations()
	if err != nil {
		t.Fatalf("RuntimeMigrations() error = %v", err)
	}
	return set
}

func migratedRuntimeDB(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t, t.TempDir(), testBusyTimeout)
	if err := db.Migrate(context.Background(), runtimeMigrationsForTest(t)); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func assertRuntimeSchemaObjects(t *testing.T, db *DB) {
	t.Helper()
	want := []string{"character_operations", "characters", "inventories", "inventory_catalog", "inventory_controls", "inventory_equipment", "inventory_items", "inventory_operations", "outbound_attempts", "outbound_deliveries", "processed_updates", "runtime_cursors", "scheduled_jobs", "schema_migrations"}
	rows, err := db.sql.QueryContext(context.Background(), `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("inspect runtime tables: %v", err)
	}
	defer closeRows(t, rows)
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan runtime table: %v", err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate runtime tables: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("runtime tables = %v, want %v", got, want)
	}
}

func tableColumns(t *testing.T, db *DB, table string) []schemaColumn {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	defer closeRows(t, rows)
	var columns []schemaColumn
	for rows.Next() {
		var cid int
		var column schemaColumn
		var defaultValue any
		if err := rows.Scan(&cid, &column.name, &column.columnType, &column.notNull, &defaultValue, &column.primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return columns
}

func insertDelivery(t *testing.T, db *DB, id, state string, nextAttempt, reason, terminal any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), deliveryInsertSQL, id, "kind", 1, []byte("content"), state, 3, 1, 10, 0, nextAttempt, reason, 1, 1, terminal); err != nil {
		t.Errorf("insert delivery state %q: %v", state, err)
	}
}

func insertRoutedDelivery(t *testing.T, db *DB, id string, target, auth, callback, incoming, expires any, replace int, state string, attemptCount, maxAttempts int) {
	t.Helper()
	var nextAttempt any
	if state == "pending" || state == "retrying" {
		nextAttempt = int64(10)
	}
	var reason, terminal any
	if state == "reconciliation_required" || state == "permanent_failure" {
		reason = "reason"
	}
	if state == "completed" || state == "permanent_failure" {
		terminal = int64(20)
	}
	if _, err := db.ExecContext(context.Background(), routedDeliveryInsertSQL, id, "kind", 1, []byte("content"), -10, target, auth, callback, incoming, expires, replace, state, maxAttempts, 1, 10, attemptCount, nextAttempt, reason, 1, 1, terminal); err != nil {
		t.Errorf("insert routed delivery %q: %v", id, err)
	}
}

func insertJob(t *testing.T, db *DB, id, state string, next, owner, correlation, lease, reason, terminal any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), jobInsertSQL, id, "type", 1, []byte("payload"), 10, state, 3, 1, 10, 0, next, owner, correlation, lease, reason, 1, 1, terminal); err != nil {
		t.Errorf("insert job state %q: %v", state, err)
	}
}

func insertJobWithAttempts(t *testing.T, db *DB, id, state string, attemptCount int, next, owner, correlation, lease, reason, terminal any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), jobInsertSQL, id, "type", 1, []byte("payload"), 10, state, 3, 1, 10, attemptCount, next, owner, correlation, lease, reason, 1, 1, terminal); err != nil {
		t.Errorf("insert job %q: %v", id, err)
	}
}

func assertStatementFails(t *testing.T, db *DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), statement, args...); err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", statement)
	}
}

func assertRowCount(t *testing.T, db *DB, table, condition string, want int) {
	t.Helper()
	var got int
	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE `+condition).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s matching rows = %d, want %d", table, got, want)
	}
}

func indexColumns(t *testing.T, db *DB, index string) []string {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), `PRAGMA index_info(`+index+`)`)
	if err != nil {
		t.Fatalf("inspect index %s: %v", index, err)
	}
	defer closeRows(t, rows)
	var columns []string
	for rows.Next() {
		var seq, cid int
		var name string
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			t.Fatalf("scan index %s: %v", index, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index %s: %v", index, err)
	}
	return columns
}

func assertTableIndexes(t *testing.T, db *DB, table string, want []string) {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), `PRAGMA index_list(`+table+`)`)
	if err != nil {
		t.Fatalf("inspect %s indexes: %v", table, err)
	}
	defer closeRows(t, rows)
	var got []string
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan %s index: %v", table, err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s indexes: %v", table, err)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s indexes = %v, want %v", table, got, want)
	}
}

func closeRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	if err := rows.Close(); err != nil {
		t.Errorf("rows Close() error = %v", err)
	}
}
