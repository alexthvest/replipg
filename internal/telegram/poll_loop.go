package telegram

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// ErrIncompleteUpdate prevents Telegram acknowledgement without durable evidence.
var ErrIncompleteUpdate = errors.New("telegram: update has no durable completion")

const (
	pollInitialBackoff = 250 * time.Millisecond
	pollMaxBackoff     = 30 * time.Second
	pollMissingIDLimit = 3
)

// Running closes when Run owns the initialized session, before its first poll.
// The lifecycle owner must also monitor Run's return value for readiness loss.
func (p *Poller) Running() <-chan struct{} { return p.running }

// Run synchronously owns one session until cancellation, shutdown, or failure.
// The caller owns its goroutine and waits for Run before releasing dependencies.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	if p.state != pollerInitialized || p.runOwned {
		p.mu.Unlock()
		return ErrPollerState
	}
	if p.options.ProcessUpdate == nil {
		p.mu.Unlock()
		return ErrInvalidPoller
	}
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return err
	}
	pollCtx, cancel := context.WithCancel(ctx)
	p.pollCancel = cancel
	p.runOwned = true
	close(p.running)
	p.mu.Unlock()
	defer cancel()
	defer p.StopAccepting()
	return p.run(pollCtx, ctx)
}

func (p *Poller) run(pollCtx, workCtx context.Context) error {
	backoff := pollInitialBackoff
	missing := 0
	for {
		if err := pollCtx.Err(); err != nil {
			return err
		}
		request, err := p.NextRequest()
		if err != nil {
			return err
		}
		updates, err := p.api.GetUpdates(pollCtx, request)
		// StopAccepting cancels only polling; admitted processing keeps workCtx.
		if _, stateErr := p.NextRequest(); stateErr != nil {
			return stateErr
		}
		if err != nil {
			if pollCtx.Err() != nil {
				return pollCtx.Err()
			}
			if !retryPolling(err) {
				return err
			}
			delay := backoff
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
				delay = apiErr.RetryAfter
			}
			if err = p.waitPoll(pollCtx, delay); err != nil {
				return err
			}
			backoff = nextPollBackoff(backoff)
			continue
		}
		if err = pollCtx.Err(); err != nil {
			return err
		}
		if len(updates) > 1 {
			return fmt.Errorf("%w: response exceeded requested limit", ErrInvalidPoller)
		}
		if len(updates) == 0 {
			backoff = pollInitialBackoff
			continue
		}
		decoded, err := DecodeUpdate(updates[0])
		if err != nil {
			missing++
			if missing >= pollMissingIDLimit {
				return ErrUpdateID
			}
			if err = p.waitPoll(pollCtx, backoff); err != nil {
				return err
			}
			backoff = nextPollBackoff(backoff)
			continue
		}
		missing = 0
		backoff = pollInitialBackoff
		// Completed duplicates never enter state-changing feature processing.
		if int64(decoded.UpdateID) < request.Offset {
			continue
		}
		if decoded.UpdateID == runtimecontract.UpdateID(math.MaxInt64) {
			return ErrInvalidCursor
		}
		if _, err = p.NextRequest(); err != nil {
			return err
		}
		processErr := p.options.ProcessUpdate(workCtx, decoded)
		// A handler failure can itself have a durable terminal record. Conversely,
		// a successful callback is insufficient evidence of a committed transaction.
		completed, err := p.cursor.CompletedUpdate(workCtx)
		if err != nil {
			return fmt.Errorf("telegram: verify completion: %w", err)
		}
		if completed < runtimecontract.UpdateID(request.Offset-1) || completed == runtimecontract.UpdateID(math.MaxInt64) {
			return ErrInvalidCursor
		}
		if completed < decoded.UpdateID {
			if processErr != nil {
				return processErr
			}
			return ErrIncompleteUpdate
		}
		p.mu.Lock()
		if p.state != pollerInitialized {
			p.mu.Unlock()
			return ErrPollerState
		}
		p.request.Offset = int64(completed) + 1
		p.mu.Unlock()
	}
}

func retryPolling(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return true
	}
	return apiErr.Kind == ErrorTransient || apiErr.Kind == ErrorRateLimit || apiErr.Kind == ErrorCancellation
}

func nextPollBackoff(previous time.Duration) time.Duration {
	return min(previous*2, pollMaxBackoff)
}

func (p *Poller) waitPoll(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.options.Sleep != nil {
		return p.options.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
