// Package runtimecontract defines the acyclic values and capabilities shared
// by runtime services and gameplay features.
package runtimecontract

import (
	"context"
	"database/sql"
)

// DBTX is the database execution capability common to a database connection
// and a transaction. Callers receive it from a transaction owner and must not
// assume a concrete database or driver implementation.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// StorageFailure marks sanitized storage errors whose underlying cause may be a
// row scan, iteration, or cleanup failure rather than a driver-specific error.
// Transaction owners keep these failures retryable without importing features.
type StorageFailure interface {
	StorageFailure()
}
