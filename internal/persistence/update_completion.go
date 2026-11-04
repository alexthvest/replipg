package persistence

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const telegramCursorSource = "telegram"

// CursorStore owns durable inbound-update completion state.
type CursorStore interface {
	CompletedUpdate(context.Context) (runtimecontract.UpdateID, error)
	RecordTerminal(context.Context, runtimecontract.DBTX, UpdateOutcome) error
}

var _ CursorStore = (*DB)(nil)

// UpdateStatus identifies a durable terminal update outcome.
type UpdateStatus string

const (
	UpdateCompleted     UpdateStatus = "completed"
	UpdateIgnored       UpdateStatus = "ignored"
	UpdateDecodeFailed  UpdateStatus = "decode_failed"
	UpdateHandlerFailed UpdateStatus = "handler_failed"
)

// UpdateOutcome is the privacy-safe durable result of processing an update.
type UpdateOutcome struct {
	UpdateID      runtimecontract.UpdateID
	Status        UpdateStatus
	CorrelationID runtimecontract.CorrelationID
	FailureCode   string
	CompletedAt   time.Time
}

// Validate rejects invalid terminal records without retaining their values in
// the returned error.
func (o UpdateOutcome) Validate() error {
	if o.UpdateID <= 0 {
		return &UpdateOutcomeValidationError{Field: "update_id", Reason: "out_of_range"}
	}
	switch o.Status {
	case UpdateCompleted, UpdateIgnored, UpdateDecodeFailed, UpdateHandlerFailed:
	default:
		return &UpdateOutcomeValidationError{Field: "status", Reason: "unsupported"}
	}
	if o.CorrelationID == "" {
		return &UpdateOutcomeValidationError{Field: "correlation_id", Reason: "required"}
	}
	if !isSafeIdentifier(string(o.CorrelationID), 128) {
		return &UpdateOutcomeValidationError{Field: "correlation_id", Reason: "invalid"}
	}
	failure := o.Status == UpdateDecodeFailed || o.Status == UpdateHandlerFailed
	if failure && o.FailureCode == "" {
		return &UpdateOutcomeValidationError{Field: "failure_code", Reason: "required"}
	}
	if failure && !isSafeIdentifier(o.FailureCode, 64) {
		return &UpdateOutcomeValidationError{Field: "failure_code", Reason: "invalid"}
	}
	if !failure && o.FailureCode != "" {
		return &UpdateOutcomeValidationError{Field: "failure_code", Reason: "inconsistent"}
	}
	if o.CompletedAt.IsZero() {
		return &UpdateOutcomeValidationError{Field: "completed_at", Reason: "required"}
	}
	return nil
}

// CompletedUpdate returns the durable contiguous completion cursor.
func (db *DB) CompletedUpdate(ctx context.Context) (runtimecontract.UpdateID, error) {
	if err := db.available(ctx); err != nil {
		return 0, err
	}
	var id int64
	err := db.sql.QueryRowContext(ctx,
		`SELECT last_completed_id FROM runtime_cursors WHERE source = ?`, telegramCursorSource,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, storageOperationError(StorageOperationReadCursor, ctx, err)
	}
	return runtimecontract.UpdateID(id), nil
}

// RecordTerminal records an immutable outcome and advances the cursor.
func (db *DB) RecordTerminal(ctx context.Context, tx runtimecontract.DBTX, outcome UpdateOutcome) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	if isNilDBTX(tx) {
		return &UpdateOutcomeValidationError{Field: "transaction", Reason: "required"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var existing int64
	err := tx.QueryRowContext(ctx, `SELECT update_id FROM processed_updates WHERE update_id = ?`, int64(outcome.UpdateID)).Scan(&existing)
	if err == nil {
		return &UpdateConflictError{UpdateID: outcome.UpdateID, Reason: UpdateConflictAlreadyProcessed}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return storageOperationError(StorageOperationCheckUpdate, ctx, err)
	}

	var cursor int64
	err = tx.QueryRowContext(ctx,
		`SELECT last_completed_id FROM runtime_cursors WHERE source = ?`, telegramCursorSource,
	).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return storageOperationError(StorageOperationReadCursor, ctx, err)
	}
	if err == nil && outcome.UpdateID <= runtimecontract.UpdateID(cursor) {
		return &UpdateConflictError{UpdateID: outcome.UpdateID, Reason: UpdateConflictCursorRegression}
	}

	var failureCode any
	if outcome.FailureCode != "" {
		failureCode = outcome.FailureCode
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO processed_updates(update_id, status, correlation_id, failure_code, completed_at)
		VALUES (?, ?, ?, ?, ?)`,
		int64(outcome.UpdateID), string(outcome.Status), string(outcome.CorrelationID), failureCode,
		outcome.CompletedAt.UTC().UnixMilli(),
	); err != nil {
		return storageOperationError(StorageOperationRecordUpdate, ctx, err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO runtime_cursors(source, last_completed_id, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(source) DO UPDATE SET
			last_completed_id = excluded.last_completed_id,
			updated_at = excluded.updated_at
		WHERE excluded.last_completed_id > runtime_cursors.last_completed_id`,
		telegramCursorSource, int64(outcome.UpdateID), outcome.CompletedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return storageOperationError(StorageOperationAdvanceCursor, ctx, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return storageOperationError(StorageOperationAdvanceCursor, ctx, err)
	}
	if affected != 1 {
		return &UpdateConflictError{UpdateID: outcome.UpdateID, Reason: UpdateConflictCursorRegression}
	}
	return nil
}

// TerminalOutcome looks up an immutable processed-update record.
func (db *DB) TerminalOutcome(ctx context.Context, id runtimecontract.UpdateID) (UpdateOutcome, bool, error) {
	if err := db.available(ctx); err != nil {
		return UpdateOutcome{}, false, err
	}
	if id <= 0 {
		return UpdateOutcome{}, false, &UpdateOutcomeValidationError{Field: "update_id", Reason: "out_of_range"}
	}
	var (
		status        string
		correlationID string
		failureCode   sql.NullString
		completedAt   int64
	)
	err := db.sql.QueryRowContext(ctx, `
		SELECT status, correlation_id, failure_code, completed_at
		FROM processed_updates WHERE update_id = ?`, int64(id),
	).Scan(&status, &correlationID, &failureCode, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateOutcome{}, false, nil
	}
	if err != nil {
		return UpdateOutcome{}, false, storageOperationError(StorageOperationReadUpdate, ctx, err)
	}
	return UpdateOutcome{
		UpdateID:      id,
		Status:        UpdateStatus(status),
		CorrelationID: runtimecontract.CorrelationID(correlationID),
		FailureCode:   failureCode.String,
		CompletedAt:   time.UnixMilli(completedAt).UTC(),
	}, true, nil
}

func isNilDBTX(tx runtimecontract.DBTX) bool {
	if tx == nil {
		return true
	}
	value := reflect.ValueOf(tx)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func isSafeIdentifier(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}
