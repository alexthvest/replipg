package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestTransactionCommitsFeatureStateOutcomeAndCursorAtomically(t *testing.T) {
	db := openMigratedTestDB(t, t.TempDir())
	createFeatureTable(t, db)
	outcome := validUpdateOutcome(11)

	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_2_4_feature(value) VALUES (?)`, "committed"); err != nil {
			return err
		}
		return db.RecordTerminal(ctx, tx, outcome)
	})
	if err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}

	assertFeatureCount(t, db, 1)
	if got, err := db.CompletedUpdate(context.Background()); err != nil || got != outcome.UpdateID {
		t.Fatalf("CompletedUpdate() = %d, %v, want %d, nil", got, err, outcome.UpdateID)
	}
	got, found, err := db.TerminalOutcome(context.Background(), outcome.UpdateID)
	if err != nil || !found || got != outcome {
		t.Fatalf("TerminalOutcome() = %+v, %v, %v, want %+v, true, nil", got, found, err, outcome)
	}
}

func TestTransactionCallbackFailureRollsBackAllChanges(t *testing.T) {
	db := openMigratedTestDB(t, t.TempDir())
	createFeatureTable(t, db)
	wantErr := errors.New("feature rejected update")

	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_2_4_feature(value) VALUES (?)`, "rolled back"); err != nil {
			return err
		}
		if err := db.RecordTerminal(ctx, tx, validUpdateOutcome(12)); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithinTransaction() error = %v, want wrapped callback error", err)
	}
	var txErr *TransactionError
	if !errors.As(err, &txErr) || txErr.Stage != TransactionStageCallback {
		t.Fatalf("WithinTransaction() error = %#v, want callback TransactionError", err)
	}
	assertFeatureCount(t, db, 0)
	if cursor, err := db.CompletedUpdate(context.Background()); err != nil || cursor != 0 {
		t.Fatalf("CompletedUpdate() = %d, %v, want 0, nil", cursor, err)
	}
	if _, found, err := db.TerminalOutcome(context.Background(), 12); err != nil || found {
		t.Fatalf("TerminalOutcome() found = %v, err = %v, want false, nil", found, err)
	}
}

func TestTransactionCallbackPanicRollsBackAndReleasesImmediateLock(t *testing.T) {
	dir := t.TempDir()
	holder := openMigratedTestDB(t, dir)
	contender := openMigratedTestDB(t, dir)
	createFeatureTable(t, holder)
	panicValue := &struct{ label string }{label: "feature panic"}

	recovered := invokePanickingTransaction(holder, panicValue)
	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want original %#v", recovered, panicValue)
	}
	assertFeatureCount(t, holder, 0)
	if cursor, err := holder.CompletedUpdate(context.Background()); err != nil || cursor != 0 {
		t.Fatalf("CompletedUpdate() = %d, %v, want 0, nil", cursor, err)
	}
	if _, found, err := holder.TerminalOutcome(context.Background(), 13); err != nil || found {
		t.Fatalf("TerminalOutcome() found = %v, err = %v, want false, nil", found, err)
	}

	started := time.Now()
	if err := contender.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error { return nil }); err != nil {
		t.Fatalf("transaction after panic error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > testBusyTimeout+500*time.Millisecond {
		t.Fatalf("transaction after panic took %v, lock was not promptly released", elapsed)
	}
}

func TestTransactionCommitFailureLeavesNoPartialChanges(t *testing.T) {
	db := openMigratedTestDB(t, t.TempDir())
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE task_2_4_parent (id INTEGER PRIMARY KEY);
		CREATE TABLE task_2_4_child (
			parent_id INTEGER NOT NULL,
			FOREIGN KEY (parent_id) REFERENCES task_2_4_parent(id) DEFERRABLE INITIALLY DEFERRED
		)`); err != nil {
		t.Fatalf("create deferred constraint tables: %v", err)
	}

	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_2_4_child(parent_id) VALUES (404)`)
		return err
	})
	var txErr *TransactionError
	if !errors.As(err, &txErr) || txErr.Stage != TransactionStageCommit || !errors.Is(err, ErrTransaction) {
		t.Fatalf("WithinTransaction() error = %#v, want commit TransactionError", err)
	}
	if got := err.Error(); got != "transaction failed during commit" {
		t.Fatalf("commit error exposed unsafe details: %q", got)
	}
	var count int
	if queryErr := db.sql.QueryRowContext(context.Background(), `SELECT count(*) FROM task_2_4_child`).Scan(&count); queryErr != nil || count != 0 {
		t.Fatalf("child count = %d, %v, want 0, nil", count, queryErr)
	}
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO task_2_4_parent(id) VALUES (404)`)
		return err
	}); err != nil {
		t.Fatalf("transaction after commit failure error = %v", err)
	}
	if got := db.sql.Stats().InUse; got != 0 {
		t.Fatalf("connections in use after commit failure recovery = %d, want 0", got)
	}
}

func TestTerminalOutcomeIsImmutableAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDB(t, dir)
	original := validUpdateOutcome(20)
	if err := recordOutcome(db, original); err != nil {
		t.Fatalf("record original: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db = openMigratedTestDB(t, dir)
	replacement := original
	replacement.Status = UpdateIgnored
	err := recordOutcome(db, replacement)
	if !errors.Is(err, ErrUpdateAlreadyProcessed) {
		t.Fatalf("record duplicate error = %v, want ErrUpdateAlreadyProcessed", err)
	}
	got, found, err := db.TerminalOutcome(context.Background(), original.UpdateID)
	if err != nil || !found || got != original {
		t.Fatalf("TerminalOutcome() = %+v, %v, %v, want original", got, found, err)
	}
	if cursor, err := db.CompletedUpdate(context.Background()); err != nil || cursor != original.UpdateID {
		t.Fatalf("CompletedUpdate() = %d, %v, want %d", cursor, err, original.UpdateID)
	}
}

func TestTerminalStatusesAndFailureCodesRoundTrip(t *testing.T) {
	db := openMigratedTestDB(t, t.TempDir())
	tests := []UpdateOutcome{
		validUpdateOutcome(21),
		{UpdateID: 22, Status: UpdateIgnored, CorrelationID: "corr-ignored", CompletedAt: time.Unix(1_700_000_000, 124_000_000).UTC()},
		{UpdateID: 23, Status: UpdateDecodeFailed, CorrelationID: "corr-decode", FailureCode: "invalid_shape", CompletedAt: time.Unix(1_700_000_000, 125_000_000).UTC()},
		{UpdateID: 24, Status: UpdateHandlerFailed, CorrelationID: "corr-handler", FailureCode: "handler_error", CompletedAt: time.Unix(1_700_000_000, 126_000_000).UTC()},
	}
	for _, want := range tests {
		if err := recordOutcome(db, want); err != nil {
			t.Fatalf("record status %q: %v", want.Status, err)
		}
		got, found, err := db.TerminalOutcome(context.Background(), want.UpdateID)
		if err != nil || !found || got != want {
			t.Fatalf("TerminalOutcome(%d) = %+v, %v, %v, want %+v, true, nil", want.UpdateID, got, found, err, want)
		}
	}
}

func TestRecordTerminalRejectsCursorRegressionWithoutMutation(t *testing.T) {
	db := openMigratedTestDB(t, t.TempDir())
	if err := recordOutcome(db, validUpdateOutcome(30)); err != nil {
		t.Fatalf("record initial: %v", err)
	}
	err := recordOutcome(db, validUpdateOutcome(29))
	if !errors.Is(err, ErrCursorRegression) {
		t.Fatalf("record regression error = %v, want ErrCursorRegression", err)
	}
	if cursor, err := db.CompletedUpdate(context.Background()); err != nil || cursor != 30 {
		t.Fatalf("CompletedUpdate() = %d, %v, want 30", cursor, err)
	}
	if _, found, err := db.TerminalOutcome(context.Background(), 29); err != nil || found {
		t.Fatalf("regressed outcome found = %v, err = %v", found, err)
	}
}

func TestUpdateOutcomeValidation(t *testing.T) {
	now := time.Unix(1_700_000_000, 123_000_000).UTC()
	tests := []struct {
		name    string
		outcome UpdateOutcome
		field   string
	}{
		{name: "update id", outcome: UpdateOutcome{Status: UpdateCompleted, CorrelationID: "corr", CompletedAt: now}, field: "update_id"},
		{name: "status", outcome: UpdateOutcome{UpdateID: 1, Status: "other", CorrelationID: "corr", CompletedAt: now}, field: "status"},
		{name: "correlation", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateCompleted, CompletedAt: now}, field: "correlation_id"},
		{name: "unsafe correlation", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateCompleted, CorrelationID: "private text", CompletedAt: now}, field: "correlation_id"},
		{name: "success failure code", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateCompleted, CorrelationID: "corr", FailureCode: "unexpected", CompletedAt: now}, field: "failure_code"},
		{name: "failed without code", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateHandlerFailed, CorrelationID: "corr", CompletedAt: now}, field: "failure_code"},
		{name: "unsafe failure code", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateHandlerFailed, CorrelationID: "corr", FailureCode: "private text", CompletedAt: now}, field: "failure_code"},
		{name: "timestamp", outcome: UpdateOutcome{UpdateID: 1, Status: UpdateIgnored, CorrelationID: "corr"}, field: "completed_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.outcome.Validate()
			var validation *UpdateOutcomeValidationError
			if !errors.As(err, &validation) || validation.Field != tt.field {
				t.Fatalf("Validate() error = %#v, want field %q", err, tt.field)
			}
			if errors.Is(err, context.Canceled) {
				t.Fatal("validation error has unrelated cause")
			}
		})
	}
	for _, status := range []UpdateStatus{UpdateCompleted, UpdateIgnored} {
		outcome := UpdateOutcome{UpdateID: 1, Status: status, CorrelationID: "corr", CompletedAt: now}
		if err := outcome.Validate(); err != nil {
			t.Errorf("status %q Validate() error = %v", status, err)
		}
	}
	for _, status := range []UpdateStatus{UpdateDecodeFailed, UpdateHandlerFailed} {
		outcome := UpdateOutcome{UpdateID: 1, Status: status, CorrelationID: "corr", FailureCode: "safe_code", CompletedAt: now}
		if err := outcome.Validate(); err != nil {
			t.Errorf("status %q Validate() error = %v", status, err)
		}
	}
}

func TestWithinTransactionHonorsCancellationAndImmediateLock(t *testing.T) {
	dir := t.TempDir()
	holder := openMigratedTestDB(t, dir)
	contender := openMigratedTestDB(t, dir)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := contender.WithinTransaction(cancelled, func(context.Context, runtimecontract.DBTX) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled WithinTransaction() = %v, called=%v", err, called)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	started := time.Now()
	err = contender.WithinTransaction(context.Background(), func(context.Context, runtimecontract.DBTX) error { return nil })
	if !errors.Is(err, ErrContention) {
		close(release)
		t.Fatalf("competing WithinTransaction() error = %v, want ErrContention", err)
	}
	if elapsed := time.Since(started); elapsed > testBusyTimeout+500*time.Millisecond {
		close(release)
		t.Fatalf("contention elapsed = %v, exceeds bound", elapsed)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("holder transaction error = %v", err)
	}
}

func validUpdateOutcome(id runtimecontract.UpdateID) UpdateOutcome {
	return UpdateOutcome{
		UpdateID:      id,
		Status:        UpdateCompleted,
		CorrelationID: "corr-2-4",
		CompletedAt:   time.Unix(1_700_000_000, 123_000_000).UTC(),
	}
}

func openMigratedTestDB(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(context.Background(), Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: testBusyTimeout})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := RuntimeMigrations()
	if err != nil {
		t.Fatalf("RuntimeMigrations() error = %v", err)
	}
	if err := db.Migrate(context.Background(), set); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func createFeatureTable(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE task_2_4_feature (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create feature table: %v", err)
	}
}

func assertFeatureCount(t *testing.T, db *DB, want int) {
	t.Helper()
	var got int
	if err := db.sql.QueryRowContext(context.Background(), `SELECT count(*) FROM task_2_4_feature`).Scan(&got); err != nil || got != want {
		t.Fatalf("feature count = %d, %v, want %d", got, err, want)
	}
}

func recordOutcome(db *DB, outcome UpdateOutcome) error {
	return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		return db.RecordTerminal(ctx, tx, outcome)
	})
}

func invokePanickingTransaction(db *DB, panicValue any) (recovered any) {
	defer func() { recovered = recover() }()
	_ = db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_2_4_feature(value) VALUES (?)`, "must roll back"); err != nil {
			return err
		}
		if err := db.RecordTerminal(ctx, tx, validUpdateOutcome(13)); err != nil {
			return err
		}
		panic(panicValue)
	})
	return recovered
}
