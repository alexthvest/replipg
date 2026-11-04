package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/alexthvest/replipg/internal/runtimecontract"

	"modernc.org/sqlite"
)

// IsStorageFailure distinguishes database failures from feature validation or
// domain errors. Driver-specific classification stays inside persistence.
// sql.ErrNoRows represents an expected lookup result rather than a failure.
func IsStorageFailure(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	var storageFailure runtimecontract.StorageFailure
	return errors.As(err, &storageFailure) || errors.As(err, &sqliteErr) || errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, sql.ErrTxDone) || errors.Is(err, ErrStorage) ||
		errors.Is(err, ErrTransaction) || errors.Is(err, ErrContention) ||
		errors.Is(err, ErrPersistenceClosed)
}

// ErrContention identifies storage operations that exhausted the configured
// bounded wait while SQLite remained busy or locked.
var ErrContention = errors.New("storage contention")

var (
	// ErrTransaction identifies a failed transaction lifecycle operation.
	ErrTransaction = errors.New("transaction failed")
	// ErrNilTransactionCallback identifies a missing transaction callback.
	ErrNilTransactionCallback = errors.New("transaction callback is required")
	// ErrStorage identifies a failed update-completion storage operation.
	ErrStorage = errors.New("storage operation failed")
	// ErrPersistenceClosed identifies an operation attempted after storage was released.
	ErrPersistenceClosed = errors.New("persistence is closed")
	// ErrUpdateAlreadyProcessed identifies an immutable duplicate update.
	ErrUpdateAlreadyProcessed = errors.New("update already processed")
	// ErrCursorRegression identifies an update at or below the durable cursor.
	ErrCursorRegression = errors.New("update would regress cursor")
	// ErrMigrationDefinition identifies an invalid immutable migration set.
	ErrMigrationDefinition = errors.New("invalid migration definition")
	// ErrMigration identifies a failed migration operation.
	ErrMigration = errors.New("migration failed")
	// ErrFutureSchema identifies a database schema newer than the binary.
	ErrFutureSchema = errors.New("database schema is newer than binary")
	// ErrCorruptSchema identifies an impossible migration ledger sequence.
	ErrCorruptSchema = errors.New("database schema ledger is corrupt")
)

// TransactionStage identifies the transaction lifecycle stage that failed.
type TransactionStage string

const (
	TransactionStageBegin    TransactionStage = "begin"
	TransactionStageCallback TransactionStage = "callback"
	TransactionStageCommit   TransactionStage = "commit"
	TransactionStageRollback TransactionStage = "rollback"
)

// TransactionError reports a transaction failure without SQL, paths, or
// driver error text.
type TransactionError struct {
	Stage TransactionStage
	cause error
}

func (e *TransactionError) Error() string        { return "transaction failed during " + string(e.Stage) }
func (e *TransactionError) Unwrap() error        { return e.cause }
func (e *TransactionError) Is(target error) bool { return target == ErrTransaction }

// StorageOperation is an allowlisted update-completion repository operation.
type StorageOperation string

const (
	StorageOperationReadCursor    StorageOperation = "read_cursor"
	StorageOperationCheckUpdate   StorageOperation = "check_update"
	StorageOperationRecordUpdate  StorageOperation = "record_update"
	StorageOperationAdvanceCursor StorageOperation = "advance_cursor"
	StorageOperationReadUpdate    StorageOperation = "read_update"
)

// StorageError reports a safe repository operation without retaining raw
// SQLite error text.
type StorageError struct {
	Operation StorageOperation
	cause     error
}

func (e *StorageError) Error() string {
	return "storage operation failed during " + string(e.Operation)
}
func (e *StorageError) Unwrap() error        { return e.cause }
func (e *StorageError) Is(target error) bool { return target == ErrStorage }

// UpdateOutcomeValidationError identifies an invalid field without its value.
type UpdateOutcomeValidationError struct {
	Field  string
	Reason string
}

func (e *UpdateOutcomeValidationError) Error() string {
	return "invalid update outcome " + e.Field + ": " + e.Reason
}

// UpdateConflictReason identifies why an outcome cannot be recorded.
type UpdateConflictReason string

const (
	UpdateConflictAlreadyProcessed UpdateConflictReason = "already_processed"
	UpdateConflictCursorRegression UpdateConflictReason = "cursor_regression"
)

// UpdateConflictError reports an immutable or monotonicity conflict.
type UpdateConflictError struct {
	UpdateID runtimecontract.UpdateID
	Reason   UpdateConflictReason
}

func (e *UpdateConflictError) Error() string {
	return "update completion conflict: " + string(e.Reason)
}
func (e *UpdateConflictError) Is(target error) bool {
	switch e.Reason {
	case UpdateConflictAlreadyProcessed:
		return target == ErrUpdateAlreadyProcessed
	case UpdateConflictCursorRegression:
		return target == ErrCursorRegression
	default:
		return false
	}
}

// MigrationDefinitionReason is a safe category for invalid migration input.
type MigrationDefinitionReason string

const (
	MigrationDefinitionInvalidPath    MigrationDefinitionReason = "invalid_path"
	MigrationDefinitionInvalidName    MigrationDefinitionReason = "invalid_name"
	MigrationDefinitionInvalidVersion MigrationDefinitionReason = "invalid_version"
	MigrationDefinitionDuplicate      MigrationDefinitionReason = "duplicate_version"
	MigrationDefinitionOutOfOrder     MigrationDefinitionReason = "out_of_order"
	MigrationDefinitionGap            MigrationDefinitionReason = "version_gap"
	MigrationDefinitionUnreadable     MigrationDefinitionReason = "unreadable"
	MigrationDefinitionEmpty          MigrationDefinitionReason = "empty"
)

// MigrationDefinitionError reports a safe set-validation failure without
// retaining migration SQL or a filesystem path.
type MigrationDefinitionError struct {
	Position int
	Reason   MigrationDefinitionReason
}

func newDefinitionError(position int, reason MigrationDefinitionReason) *MigrationDefinitionError {
	return &MigrationDefinitionError{Position: position, Reason: reason}
}

// Error implements error.
func (e *MigrationDefinitionError) Error() string {
	return "invalid migration definition at position " + strconv.Itoa(e.Position) + ": " + string(e.Reason)
}

// Is permits errors.Is(err, ErrMigrationDefinition).
func (e *MigrationDefinitionError) Is(target error) bool { return target == ErrMigrationDefinition }

// MigrationStage identifies the safe stage at which migration failed.
type MigrationStage string

const (
	MigrationStageInspect   MigrationStage = "inspect"
	MigrationStageBegin     MigrationStage = "begin"
	MigrationStageBootstrap MigrationStage = "bootstrap"
	MigrationStageApply     MigrationStage = "apply"
	MigrationStageRecord    MigrationStage = "record"
	MigrationStageCommit    MigrationStage = "commit"
	MigrationStageRollback  MigrationStage = "rollback"
)

// MigrationReason is a safe failure category.
type MigrationReason string

const (
	MigrationReasonLedger      MigrationReason = "ledger"
	MigrationReasonTransaction MigrationReason = "transaction"
	MigrationReasonStatement   MigrationReason = "statement"
)

// MigrationError reports a migration failure without retaining SQL or paths.
type MigrationError struct {
	Version int
	Stage   MigrationStage
	Reason  MigrationReason
	cause   error
}

// Error implements error.
func (e *MigrationError) Error() string {
	return "migration failed at version " + strconv.Itoa(e.Version) + " during " + string(e.Stage) + ": " + string(e.Reason)
}

// Unwrap exposes only pre-classified safe causes such as cancellation and
// contention; raw SQLite errors are never retained.
func (e *MigrationError) Unwrap() error { return e.cause }

// Is permits errors.Is(err, ErrMigration).
func (e *MigrationError) Is(target error) bool { return target == ErrMigration }

// SchemaCompatibilityReason categorizes migration-ledger incompatibility.
type SchemaCompatibilityReason string

const (
	SchemaCompatibilityFuture  SchemaCompatibilityReason = "future"
	SchemaCompatibilityCorrupt SchemaCompatibilityReason = "corrupt"
)

// SchemaCompatibilityError reports safe database/binary version facts.
type SchemaCompatibilityError struct {
	DatabaseVersion int
	BinaryVersion   int
	Reason          SchemaCompatibilityReason
}

// Error implements error.
func (e *SchemaCompatibilityError) Error() string {
	return "database schema is incompatible with binary: " + string(e.Reason)
}

// Is permits classification of future and corrupt schema errors.
func (e *SchemaCompatibilityError) Is(target error) bool {
	switch e.Reason {
	case SchemaCompatibilityFuture:
		return target == ErrFutureSchema
	case SchemaCompatibilityCorrupt:
		return target == ErrCorruptSchema
	default:
		return false
	}
}

// ContentionError reports bounded SQLite contention without exposing driver
// details, database paths, or statements.
type ContentionError struct{}

// Error implements error.
func (e *ContentionError) Error() string { return ErrContention.Error() }

// Is permits errors.Is(err, ErrContention).
func (e *ContentionError) Is(target error) bool { return target == ErrContention }

// OptionsError identifies an invalid persistence option without retaining its
// value.
type OptionsError struct {
	Field  string
	Reason string
}

// Error implements error.
func (e *OptionsError) Error() string {
	return "invalid persistence option " + e.Field + ": " + e.Reason
}

// OpenError identifies a database startup stage without exposing the database
// path, DSN, or an underlying driver message that may contain either.
type OpenError struct {
	Stage string
}

// Error implements error.
func (e *OpenError) Error() string { return "persistence startup failed during " + e.Stage }

func migrationOperationError(version int, stage MigrationStage, reason MigrationReason, ctx context.Context, err error) error {
	mapped := mapError(ctx, err)
	var safeCause error
	if errors.Is(mapped, context.Canceled) || errors.Is(mapped, context.DeadlineExceeded) || errors.Is(mapped, ErrContention) {
		safeCause = mapped
	}
	return &MigrationError{
		Version: version,
		Stage:   stage,
		Reason:  reason,
		cause:   safeCause,
	}
}

func transactionOperationError(stage TransactionStage, ctx context.Context, err error) error {
	return &TransactionError{Stage: stage, cause: safeOperationCause(ctx, err, stage == TransactionStageCallback)}
}

func storageOperationError(operation StorageOperation, ctx context.Context, err error) error {
	return &StorageError{Operation: operation, cause: safeOperationCause(ctx, err, false)}
}

func safeOperationCause(ctx context.Context, err error, preserveApplicationError bool) error {
	mapped := mapError(ctx, err)
	if errors.Is(mapped, context.Canceled) || errors.Is(mapped, context.DeadlineExceeded) || errors.Is(mapped, ErrContention) ||
		errors.Is(mapped, ErrUpdateAlreadyProcessed) || errors.Is(mapped, ErrCursorRegression) ||
		errors.Is(mapped, ErrNilTransactionCallback) || errors.Is(mapped, ErrStorage) {
		return mapped
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return nil
	}
	if preserveApplicationError {
		return err
	}
	return nil
}

func mapError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY and SQLITE_LOCKED, including extended codes.
			return &ContentionError{}
		}
	}
	return err
}
