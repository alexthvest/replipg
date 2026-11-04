package outbox

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// PurgeResolved removes an old resolved delivery and its attempts using the
// caller's transaction. The feature must first verify projection, permanent
// abandonment of uncertain work, and its own retention deadline in that same
// transaction. This method does not commit or change delivery outcomes.
// Missing deliveries count as purged; active or too-young deliveries do not.
func (s *Store) PurgeResolved(ctx context.Context, tx runtimecontract.DBTX, kind runtimecontract.DeliveryKind, id runtimecontract.DeliveryID, cutoff time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if isNilDBTX(tx) {
		return false, invalid("transaction", runtimecontract.ReasonRequired)
	}
	if kind == "" {
		return false, invalid("delivery.kind", runtimecontract.ReasonRequired)
	}
	if id == "" {
		return false, invalid("delivery.id", runtimecontract.ReasonRequired)
	}
	s.mu.RLock()
	registered := false
	for key := range s.decoders {
		if key.kind == kind {
			registered = true
			break
		}
	}
	s.mu.RUnlock()
	if !registered {
		return false, &UnsupportedVersionError{}
	}
	var storedKind runtimecontract.DeliveryKind
	err := tx.QueryRowContext(ctx, `SELECT kind FROM outbound_deliveries WHERE id=?`, id).Scan(&storedKind)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, storageError(ctx, err)
	}
	if storedKind != kind {
		return false, &ConflictError{}
	}
	// Repeat eligibility in both writes so stale observations cannot remove
	// attempts or a delivery that has returned to active work. SQLite serializes
	// writes within the supplied transaction, including worker claims/resolution.
	_, err = tx.ExecContext(ctx, `DELETE FROM outbound_attempts WHERE delivery_id=? AND EXISTS (
  SELECT 1 FROM outbound_deliveries WHERE id=? AND kind=? AND (
   (state IN ('completed','permanent_failure') AND terminal_at<=?) OR
   (state='reconciliation_required' AND updated_at<=?)
  ))`, id, id, kind, cutoff.UnixMilli(), cutoff.UnixMilli())
	if err != nil {
		return false, storageError(ctx, err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM outbound_deliveries WHERE id=? AND kind=? AND (
  (state IN ('completed','permanent_failure') AND terminal_at<=?) OR
  (state='reconciliation_required' AND updated_at<=?)
 )`, id, kind, cutoff.UnixMilli(), cutoff.UnixMilli())
	if err != nil {
		return false, storageError(ctx, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, storageError(ctx, err)
	}
	return count == 1, nil
}
