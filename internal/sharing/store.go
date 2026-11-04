// Package sharing implements durable, explicitly authorized menu sharing.
package sharing

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

// ErrStoreConflict indicates a stale revision or conflicting action reservation.
var ErrStoreConflict = errors.New("sharing storage conflict")

type storeError struct{ operation string }

func (e storeError) Error() string { return "sharing storage: " + e.operation }

func (storeError) StorageFailure() {}

func storeFailure(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return storeError{op}
}

type viewRecord struct {
	Token              string
	Owner              runtimecontract.UserID
	Destination        runtimecontract.GroupDestination
	Kind               contract.ViewKind
	Locale             localization.Locale
	FingerprintVersion int
	Retired            bool
	BodyHash           []byte
	BoundMessageID     int64
	UsableUntil        time.Time
	CreatedAt          time.Time
	LastSuccessAt      time.Time
	UnknownUntil       time.Time
	Revision           int64
}

type actionRecord struct {
	UpdateID           runtimecontract.UpdateID
	ViewToken          string
	PublicDeliveryID   runtimecontract.DeliveryID
	PublicState        string
	AcceptedAt         time.Time
	PublicOutcomeAt    time.Time
	NextCheckAt        time.Time
	CallbackQueryID    string
	CallbackDeadline   time.Time
	FeedbackDeliveryID runtimecontract.DeliveryID
	FeedbackState      string
	FeedbackOutcomeAt  time.Time
	PublicPurgeAfter   time.Time
	FeedbackPurgeAfter time.Time
	PublicPurged       bool
	FeedbackPurged     bool
	Revision           int64
}

const viewColumns = `token,owner_id,chat_id,chat_kind,view_kind,locale,fingerprint_version,retired,body_hash,bound_message_id,usable_until,created_at,last_success_at,unknown_until,revision`

const actionColumns = `update_id,view_token,public_delivery_id,public_state,accepted_at,public_outcome_at,next_check_at,callback_query_id,callback_deadline,feedback_delivery_id,feedback_state,feedback_outcome_at,public_purge_after,feedback_purge_after,public_purged,feedback_purged,revision`

type storeScanner interface{ Scan(...any) error }

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return t.UTC().UnixMilli()
}

func nullableID(n int64) any {
	if n == 0 {
		return nil
	}

	return n
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func storedTime(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}

	return time.UnixMilli(n.Int64).UTC()
}

func scanView(row storeScanner) (viewRecord, error) {
	var v viewRecord
	var bound, until, last, unknown sql.NullInt64
	var created int64

	err := row.Scan(&v.Token, &v.Owner, &v.Destination.ChatID, &v.Destination.Kind, &v.Kind, &v.Locale, &v.FingerprintVersion, &v.Retired, &v.BodyHash, &bound, &until, &created, &last, &unknown, &v.Revision)

	v.BoundMessageID = bound.Int64
	v.UsableUntil = storedTime(until)
	v.CreatedAt = time.UnixMilli(created).UTC()
	v.LastSuccessAt = storedTime(last)
	v.UnknownUntil = storedTime(unknown)
	return v, err
}

func scanAction(row storeScanner) (actionRecord, error) {
	var a actionRecord
	var accepted int64
	var outcome, next, deadline, feedbackOutcome, publicPurge, feedbackPurge sql.NullInt64
	var callback, feedback sql.NullString

	err := row.Scan(&a.UpdateID, &a.ViewToken, &a.PublicDeliveryID, &a.PublicState, &accepted, &outcome, &next, &callback, &deadline, &feedback, &a.FeedbackState, &feedbackOutcome, &publicPurge, &feedbackPurge, &a.PublicPurged, &a.FeedbackPurged, &a.Revision)

	a.AcceptedAt = time.UnixMilli(accepted).UTC()
	a.PublicOutcomeAt = storedTime(outcome)
	a.NextCheckAt = storedTime(next)
	a.CallbackQueryID = callback.String
	a.CallbackDeadline = storedTime(deadline)
	a.FeedbackDeliveryID = runtimecontract.DeliveryID(feedback.String)
	a.FeedbackOutcomeAt = storedTime(feedbackOutcome)
	a.PublicPurgeAfter = storedTime(publicPurge)
	a.FeedbackPurgeAfter = storedTime(feedbackPurge)
	return a, err
}

func readView(ctx context.Context, tx runtimecontract.DBTX, token string) (viewRecord, error) {
	v, err := scanView(tx.QueryRowContext(ctx, `SELECT `+viewColumns+` FROM sharing_views WHERE token=?`, token))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		err = storeFailure("read view", err)
	}
	return v, err
}

func insertView(ctx context.Context, tx runtimecontract.DBTX, v viewRecord) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sharing_views(`+viewColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`, v.Token, v.Owner, v.Destination.ChatID, v.Destination.Kind, v.Kind, string(v.Locale), v.FingerprintVersion, v.Retired, v.BodyHash, nullableID(v.BoundMessageID), nullableTime(v.UsableUntil), v.CreatedAt.UTC().UnixMilli(), nullableTime(v.LastSuccessAt), nullableTime(v.UnknownUntil))
	if err != nil {
		return storeFailure("insert view", err)
	}

	return nil
}

// updateView changes only mutable metadata, fencing concurrent/stale writers.
func updateView(ctx context.Context, tx runtimecontract.DBTX, v viewRecord) error {
	r, err := tx.ExecContext(ctx, `UPDATE sharing_views SET bound_message_id=?,last_success_at=?,unknown_until=?,revision=revision+1 WHERE token=? AND revision=? AND (bound_message_id IS NULL OR bound_message_id=?) AND retired=? AND body_hash IS ?`, nullableID(v.BoundMessageID), nullableTime(v.LastSuccessAt), nullableTime(v.UnknownUntil), v.Token, v.Revision, nullableID(v.BoundMessageID), v.Retired, v.BodyHash)
	return changedStoreRow(r, err, "update view")
}

// retireExpiredViews clears only expired inventory fingerprints once outstanding
// public and feedback work is projected. Tokens and replay identities survive.
func retireExpiredViews(ctx context.Context, tx runtimecontract.DBTX, now time.Time, limit int) error {
	if limit < 1 || limit > 100 {
		limit = 100
	}

	_, err := tx.ExecContext(ctx, `UPDATE sharing_views SET retired=1,body_hash=NULL,revision=revision+1 WHERE token IN (SELECT v.token FROM sharing_views v WHERE v.retired=0 AND v.view_kind IN ('inventory_page','item_details') AND v.usable_until<=? AND NOT EXISTS (SELECT 1 FROM sharing_actions a WHERE a.view_token=v.token AND (a.public_state='pending' OR a.feedback_state IN ('waiting','queued'))) ORDER BY v.usable_until,v.token LIMIT ?)`, now.UTC().UnixMilli(), limit)
	if err != nil {
		return storeFailure("retire views", err)
	}

	return nil
}

func readAction(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.UpdateID) (actionRecord, error) {
	a, err := scanAction(tx.QueryRowContext(ctx, `SELECT `+actionColumns+` FROM sharing_actions WHERE update_id=?`, id))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		err = storeFailure("read action", err)
	}
	return a, err
}

func insertAction(ctx context.Context, tx runtimecontract.DBTX, a actionRecord) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO sharing_actions(update_id,view_token,public_delivery_id,accepted_at,next_check_at,callback_query_id,callback_deadline) VALUES(?,?,?,?,?,?,?)`, a.UpdateID, a.ViewToken, a.PublicDeliveryID, a.AcceptedAt.UTC().UnixMilli(), nullableTime(a.NextCheckAt), nullableString(a.CallbackQueryID), nullableTime(a.CallbackDeadline))
	if err != nil {
		return storeFailure("insert action", err)
	}

	return nil
}

// updateAction preserves immutable acceptance/replay identities. Public terminal
// states, feedback terminal states and purge acknowledgements cannot be reversed.
func updateAction(ctx context.Context, tx runtimecontract.DBTX, a actionRecord) error {
	r, err := tx.ExecContext(ctx, `UPDATE sharing_actions SET
 public_state=?,public_outcome_at=?,next_check_at=?,callback_query_id=?,callback_deadline=?,
 feedback_delivery_id=?,feedback_state=?,feedback_outcome_at=?,public_purge_after=?,
 feedback_purge_after=?,public_purged=?,feedback_purged=?,revision=revision+1
 WHERE update_id=? AND revision=?
 AND (public_state='pending' OR (public_state=? AND public_outcome_at IS ? AND public_purge_after IS ?))
 AND ((feedback_state='waiting' AND ? IN ('waiting','queued','skipped'))
 OR (feedback_state='queued' AND ? IN ('queued','completed','rejected','unknown') AND feedback_delivery_id=?)
 OR (feedback_state=? AND feedback_outcome_at IS ? AND feedback_purge_after IS ? AND feedback_delivery_id IS ?))
 AND (?<>'waiting' OR (callback_query_id IS ? AND callback_deadline IS ?))
 AND public_purged<=? AND feedback_purged<=?`,
		a.PublicState, nullableTime(a.PublicOutcomeAt), nullableTime(a.NextCheckAt), nullableString(a.CallbackQueryID), nullableTime(a.CallbackDeadline),
		nullableString(string(a.FeedbackDeliveryID)), a.FeedbackState, nullableTime(a.FeedbackOutcomeAt), nullableTime(a.PublicPurgeAfter),
		nullableTime(a.FeedbackPurgeAfter), a.PublicPurged, a.FeedbackPurged, a.UpdateID, a.Revision,
		a.PublicState, nullableTime(a.PublicOutcomeAt), nullableTime(a.PublicPurgeAfter),
		a.FeedbackState, a.FeedbackState, nullableString(string(a.FeedbackDeliveryID)),
		a.FeedbackState, nullableTime(a.FeedbackOutcomeAt), nullableTime(a.FeedbackPurgeAfter), nullableString(string(a.FeedbackDeliveryID)),
		a.FeedbackState, nullableString(a.CallbackQueryID), nullableTime(a.CallbackDeadline), a.PublicPurged, a.FeedbackPurged)
	return changedStoreRow(r, err, "update action")
}

func changedStoreRow(r sql.Result, err error, op string) error {
	if err != nil {
		return storeFailure(op, err)
	}

	n, err := r.RowsAffected()
	if err != nil {
		return storeFailure(op, err)
	}

	if n != 1 {
		return ErrStoreConflict
	}

	return nil
}

// actionsForView returns unresolved work for eligibility refresh, including
// feedback/cleanup work independently of the public reservation.
func actionsForView(ctx context.Context, tx runtimecontract.DBTX, token string) ([]actionRecord, error) {
	return queryActions(ctx, tx, `SELECT `+actionColumns+` FROM sharing_actions WHERE view_token=? AND next_check_at IS NOT NULL ORDER BY update_id`, token)
}

// dueActions is bounded and ordered by rescheduled due time to prevent starvation.
func dueActions(ctx context.Context, tx runtimecontract.DBTX, now time.Time, limit int) ([]actionRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	return queryActions(ctx, tx, `SELECT `+actionColumns+` FROM sharing_actions WHERE next_check_at<=? ORDER BY next_check_at,update_id LIMIT ?`, now.UTC().UnixMilli(), limit)
}

func queryActions(ctx context.Context, tx runtimecontract.DBTX, q string, args ...any) ([]actionRecord, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, storeFailure("list actions", err)
	}

	var result []actionRecord

	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			_ = rows.Close()
			return nil, storeFailure("scan action", err)
		}

		result = append(result, a)
	}

	iteration := rows.Err()
	closed := rows.Close()
	if iteration != nil {
		return nil, storeFailure("iterate actions", iteration)
	}

	if closed != nil {
		return nil, storeFailure("close actions", closed)
	}

	return result, nil
}
