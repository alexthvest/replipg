package menus

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

// EditError preserves transport classifications without exposing Telegram types.
type EditError struct {
	RetryAfter  time.Duration
	Permanent   bool
	NotModified bool
}

func (e *EditError) Error() string {
	return "menu edit failed"
}

func Classify(err error) EditError {
	var result *EditError
	if errors.As(err, &result) {
		return *result
	}
	return EditError{}
}

type DeliveryPort interface {
	Claim(context.Context, int) ([]Job, error)
	Active(context.Context, Job) (bool, error)
	Complete(context.Context, Outcome) error
}

type Editor interface {
	Edit(context.Context, core.GroupID, int, core.View) error
}

// Deliver owns a bounded pool. Per-message claims stay in flight until completion;
// retry deadlines live on the owner, so a rate-limited message cannot block others.
func Deliver(ctx context.Context, owner DeliveryPort, editor Editor) {
	const capacity = 4
	results := make(chan Outcome, capacity)
	var joined sync.WaitGroup
	defer joined.Wait()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	active := 0
	for {
		select {
		case <-ctx.Done():
			return
		case outcome := <-results:
			active--
			_ = owner.Complete(ctx, outcome)
		case <-ticker.C:
			jobs, err := owner.Claim(ctx, capacity-active)
			if err != nil {
				continue
			}

			for _, job := range jobs {
				active++
				joined.Add(1)
				go func(job Job) {
					defer joined.Done()
					ok, err := owner.Active(ctx, job)
					if err == nil && ok {
						err = editor.Edit(ctx, job.Group, job.Message, job.View)
					}

					select {
					case results <- Outcome{
						Job: job,
						Err: err,
					}:
					case <-ctx.Done():
					}
				}(job)
			}
		}
	}
}
