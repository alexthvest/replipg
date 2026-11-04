package settings

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type WorkerOptions struct {
	Now                                         func() time.Time
	InitialDelay, MaximumDelay, RefreshInterval time.Duration
}
type MenuWorker struct {
	mu      sync.Mutex
	tx      runtimecontract.Transactor
	store   *Store
	writer  runtimecontract.GroupMenuWriter
	options WorkerOptions
}

func NewMenuWorker(tx runtimecontract.Transactor, s *Store, w runtimecontract.GroupMenuWriter, o WorkerOptions) (*MenuWorker, error) {
	if tx == nil || s == nil || s.menu == nil || w == nil || o.Now == nil {
		return nil, ErrInvalid
	}
	if o.InitialDelay == 0 {
		o.InitialDelay = time.Second
	}
	if o.MaximumDelay == 0 {
		o.MaximumDelay = 15 * time.Minute
	}
	if o.RefreshInterval == 0 {
		o.RefreshInterval = 15 * time.Minute
	}
	if o.InitialDelay < 0 || o.MaximumDelay < o.InitialDelay || o.RefreshInterval < 0 {
		return nil, ErrInvalid
	}
	return &MenuWorker{tx: tx, store: s, writer: w, options: o}, nil
}
func (w *MenuWorker) RunOnce(ctx context.Context) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var v Snapshot
	found := false
	err := w.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		v, err = scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM group_settings WHERE menu_next_attempt_at<=? ORDER BY menu_next_attempt_at,chat_id LIMIT 1`, w.options.Now().UnixMilli()))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return storage("menu snapshot", err)
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return false, err
	}
	commands, err := w.store.menu.Commands(v.Locale)
	if err != nil {
		return true, err
	}
	publishErr := w.writer.ReplaceGroupMenu(ctx, v.ChatID, commands)
	if errors.Is(publishErr, context.Canceled) || errors.Is(publishErr, context.DeadlineExceeded) {
		return true, publishErr
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	now := w.options.Now()
	err = w.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if publishErr == nil {
			_, err := tx.ExecContext(ctx, `UPDATE group_settings SET applied_menu_hash=?,applied_revision=?,menu_attempts=0,menu_failure_code=NULL,menu_next_attempt_at=? WHERE chat_id=? AND revision=? AND desired_menu_hash=?`, v.DesiredHash, v.Revision, now.Add(w.options.RefreshInterval).UnixMilli(), v.ChatID, v.Revision, v.DesiredHash)
			if err != nil {
				return storage("menu acknowledgement", err)
			}
			return nil
		}
		attempts := v.Attempts
		if attempts < math.MaxInt64 {
			attempts++
		}
		delay := w.options.InitialDelay
		for n := int64(1); n < attempts && delay < w.options.MaximumDelay; n++ {
			if delay > w.options.MaximumDelay/2 {
				delay = w.options.MaximumDelay
			} else {
				delay *= 2
			}
		}
		if delay > w.options.MaximumDelay {
			delay = w.options.MaximumDelay
		}
		code := "transient"
		var remote *runtimecontract.MenuError
		if errors.As(publishErr, &remote) {
			switch remote.Kind {
			case runtimecontract.RateLimited:
				code = "rate_limited"
			case runtimecontract.Forbidden:
				code = "forbidden"
				delay = w.options.MaximumDelay
			case runtimecontract.Invalid:
				code = "invalid"
				delay = w.options.MaximumDelay
			case runtimecontract.Indeterminate:
				code = "indeterminate"
			}
			if remote.RetryAfter > delay {
				delay = remote.RetryAfter
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE group_settings SET applied_menu_hash=NULL,applied_revision=NULL,menu_attempts=?,menu_failure_code=?,menu_next_attempt_at=? WHERE chat_id=? AND revision=? AND desired_menu_hash=?`, attempts, code, now.Add(delay).UnixMilli(), v.ChatID, v.Revision, v.DesiredHash)
		if err != nil {
			return storage("menu failure", err)
		}
		return nil
	})
	return true, err
}
