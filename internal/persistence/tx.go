package persistence

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Transactor owns the lifecycle of a short context-bound transaction.
type Transactor interface {
	WithinTransaction(context.Context, func(context.Context, runtimecontract.DBTX) error) error
}

var _ Transactor = (*DB)(nil)

// WithinTransaction executes fn in a database transaction.
func (db *DB) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	if err := db.available(ctx); err != nil {
		return err
	}
	if fn == nil {
		return &TransactionError{Stage: TransactionStageCallback, cause: ErrNilTransactionCallback}
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return transactionOperationError(TransactionStageBegin, ctx, err)
	}
	finalized := false
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		if !finalized {
			rollbackAfterPanic(tx)
			finalized = true
		}
		panic(panicValue)
	}()

	wrapped := transactionDBTX{tx: tx}
	if err := fn(ctx, wrapped); err != nil {
		callbackErr := transactionOperationError(TransactionStageCallback, ctx, err)
		rollbackErr := tx.Rollback()
		finalized = true
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return transactionOperationError(TransactionStageRollback, ctx, rollbackErr)
		}
		return callbackErr
	}
	err = tx.Commit()
	finalized = true
	if err != nil {
		return transactionOperationError(TransactionStageCommit, ctx, err)
	}
	return nil
}

func rollbackAfterPanic(tx *sql.Tx) {
	defer func() { _ = recover() }()
	_ = tx.Rollback()
}

type transactionDBTX struct{ tx *sql.Tx }

func (tx transactionDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := tx.tx.ExecContext(ctx, query, args...)
	return result, mapError(ctx, err)
}

func (tx transactionDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := tx.tx.QueryContext(ctx, query, args...)
	return rows, mapError(ctx, err)
}

func (tx transactionDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, query, args...)
}
