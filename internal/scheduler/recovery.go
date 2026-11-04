package scheduler

import (
	"context"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Recover releases expired claims during startup without consuming or refunding
// attempts. Pending and retrying jobs retain their original eligibility times;
// live claims and terminal records remain unchanged. The caller owns startup
// ordering and must finish recovery before reporting readiness.
func (s *Service) Recover(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transactor, ok := s.db.(runtimecontract.Transactor)
	if !ok || isNil(transactor) {
		return ErrTransactionRequired
	}
	err := transactor.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := s.clock.Now().UTC().UnixMilli()
		_, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET
   state=CASE WHEN attempt_count>=max_attempts THEN 'permanent_failure' ELSE 'pending' END,
   reason_code=CASE WHEN attempt_count>=max_attempts THEN 'retry_exhausted' ELSE NULL END,
   terminal_at=CASE WHEN attempt_count>=max_attempts THEN ? ELSE NULL END,
   next_attempt_at=NULL,claim_owner=NULL,claim_correlation_id=NULL,lease_until=NULL,updated_at=?
   WHERE state='claimed' AND lease_until<=?`, now, now, now)
		return err
	})
	if err != nil {
		return storeError("recover", err)
	}
	return nil
}
