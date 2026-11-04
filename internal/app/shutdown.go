package app

import (
	"context"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/telegram"
)

// runtimeTimer keeps deadline ownership local while allowing deterministic
// lifecycle tests to advance time without sleeping.
type runtimeTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type systemTimer struct{ *time.Timer }

func (t systemTimer) C() <-chan time.Time { return t.Timer.C }

// admit linearizes worker and dispatch admission against the draining transition.
func (r *Runtime) admit(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.draining {
		return context.Canceled
	}

	return ctx.Err()
}

// drain closes admission without cancelling work that is already running.
// The health server has separate ownership so probes remain available while
// workers finish. Handlers and delivery boundaries must cooperate with context
// cancellation: the deadline cancels their work, then joins their cleanup before
// resources close; Go cannot forcibly terminate an uncooperative goroutine.
func (r *Runtime) drain(ctx context.Context, owned *sync.WaitGroup, poller *telegram.Poller, stopAdmission, cancelWork context.CancelFunc) {
	r.mu.Lock()
	r.draining = true
	r.lifecycle(ctx, health.Draining)
	r.mu.Unlock()
	if poller != nil {
		poller.StopAccepting()
	}
	stopAdmission()
	finished := make(chan struct{})
	go func() { owned.Wait(); close(finished) }()
	timer := r.newShutdownTimer(r.cfg.ShutdownDeadline)
	defer timer.Stop()

	select {
	case <-finished:
	case <-timer.C():
		cancelWork()
		<-finished
	}

	cancelWork()
}
