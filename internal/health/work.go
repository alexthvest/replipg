package health

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var ErrWorkStatusUnavailable = errors.New("durable work status unavailable")

// DeliveryCounts preserves every authoritative outbound state without content.
type DeliveryCounts struct {
	Pending                int64 `json:"pending"`
	Attempting             int64 `json:"attempting"`
	Retrying               int64 `json:"retrying"`
	ReconciliationRequired int64 `json:"reconciliation_required"`
	Completed              int64 `json:"completed"`
	PermanentFailure       int64 `json:"permanent_failure"`
}

// JobCounts preserves every authoritative scheduler state without payloads.
type JobCounts struct {
	Pending          int64 `json:"pending"`
	Claimed          int64 `json:"claimed"`
	Retrying         int64 `json:"retrying"`
	Completed        int64 `json:"completed"`
	Cancelled        int64 `json:"cancelled"`
	PermanentFailure int64 `json:"permanent_failure"`
}

// WorkCounts is a privacy-safe aggregate snapshot of both durable queues.
type WorkCounts struct {
	Deliveries DeliveryCounts `json:"deliveries"`
	Jobs       JobCounts      `json:"jobs"`
}

// WorkStatus reads only state/count columns using a single consistent query.
type WorkStatus struct{ db runtimecontract.DBTX }

func NewWorkStatus(db runtimecontract.DBTX) *WorkStatus { return &WorkStatus{db: db} }

// Snapshot returns complete counts or a safe failure with no partial snapshot.
func (s *WorkStatus) Snapshot(ctx context.Context) (WorkCounts, error) {
	if err := ctx.Err(); err != nil {
		return WorkCounts{}, err
	}
	if s == nil || s.db == nil {
		return WorkCounts{}, ErrWorkStatusUnavailable
	}
	rows, err := s.db.QueryContext(ctx, `SELECT 'delivery',state,count(*) FROM outbound_deliveries GROUP BY state UNION ALL SELECT 'job',state,count(*) FROM scheduled_jobs GROUP BY state`)
	if err != nil {
		return WorkCounts{}, statusError(ctx)
	}
	defer func() { _ = rows.Close() }()
	var result WorkCounts
	for rows.Next() {
		var kind, state string
		var count int64
		if err := rows.Scan(&kind, &state, &count); err != nil || count < 0 {
			return WorkCounts{}, statusError(ctx)
		}
		var target *int64
		switch kind {
		case "delivery":
			switch state {
			case "pending":
				target = &result.Deliveries.Pending
			case "attempting":
				target = &result.Deliveries.Attempting
			case "retrying":
				target = &result.Deliveries.Retrying
			case "reconciliation_required":
				target = &result.Deliveries.ReconciliationRequired
			case "completed":
				target = &result.Deliveries.Completed
			case "permanent_failure":
				target = &result.Deliveries.PermanentFailure
			}
		case "job":
			switch state {
			case "pending":
				target = &result.Jobs.Pending
			case "claimed":
				target = &result.Jobs.Claimed
			case "retrying":
				target = &result.Jobs.Retrying
			case "completed":
				target = &result.Jobs.Completed
			case "cancelled":
				target = &result.Jobs.Cancelled
			case "permanent_failure":
				target = &result.Jobs.PermanentFailure
			}
		}
		if target == nil {
			return WorkCounts{}, ErrWorkStatusUnavailable
		}
		*target = count
	}
	if err := rows.Err(); err != nil {
		return WorkCounts{}, statusError(ctx)
	}
	if err := rows.Close(); err != nil {
		return WorkCounts{}, statusError(ctx)
	}
	return result, nil
}
func statusError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrWorkStatusUnavailable
}
