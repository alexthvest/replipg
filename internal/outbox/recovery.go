package outbox

import (
	"context"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Recover quarantines deliveries interrupted during a send without inventing a
// definitive network outcome. Existing attempt rows remain original audit facts.
// Call only during startup, before workers start and readiness is reported: an
// active worker's attempting delivery cannot be distinguished from an abandoned one.
func (s *Store) Recover(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transactor, ok := s.db.(persistence.Transactor)
	if !ok || isNilDBTX(s.db) {
		return ErrTransactionRequired
	}
	err := transactor.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `UPDATE outbound_deliveries SET
   state='reconciliation_required', next_attempt_at=NULL,
   reason_code='interrupted', updated_at=? WHERE state='attempting'`, s.now().UTC().UnixMilli())
		return err
	})
	if err != nil {
		return storageError(ctx, err)
	}
	return nil
}
