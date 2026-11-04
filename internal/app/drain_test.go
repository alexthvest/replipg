package app

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
)

type drainRunner struct {
	entered chan context.Context
	release chan struct{}
	calls   atomic.Int32
}

func (w *drainRunner) RunOnce(ctx context.Context) (bool, error) {
	w.calls.Add(1)
	select {
	case w.entered <- ctx:
	default:
	}

	select {
	case <-w.release:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (*drainRunner) WaitNext(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestWorkerDrainFinishesAdmittedWorkWithoutAnotherClaim(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(map[bool]string{false: "outbox", true: "scheduler"}[scheduled], func(t *testing.T) {
			r, err := New(testConfig(t), Options{Diagnostics: observability.NewJSONSink(io.Discard)})
			if err != nil {
				t.Fatal(err)
			}

			work, cancelWork := context.WithCancel(context.Background())
			defer cancelWork()

			admission, stop := context.WithCancel(context.Background())
			defer stop()

			w := &drainRunner{entered: make(chan context.Context, 1), release: make(chan struct{})}
			done := make(chan error, 1)
			go func() {
				if scheduled {
					done <- r.runScheduler(work, w, admission)
				} else {
					done <- r.runOutbox(work, w, admission)
				}
			}()
			current := <-w.entered
			stop()
			if current.Err() != nil {
				t.Fatal("admitted work cancelled when admission stopped")
			}

			close(w.release)
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				cancelWork()
				<-done
				t.Fatal("worker continued claims after admission stopped")
			}

			if got := w.calls.Load(); got != 1 {
				t.Fatalf("new claims after admission stopped: %d", got)
			}
		})
	}
}

func TestDrainClosesAdmissionAndPreservesCurrentWork(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "deadline"}[deadline], func(t *testing.T) {
			cfg := testConfig(t)
			cfg.ShutdownDeadline = time.Second
			r, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(io.Discard)})
			if err != nil {
				t.Fatal(err)
			}

			r.lifecycle(context.Background(), observability.StateReady)
			work, cancel := context.WithCancel(context.Background())
			defer cancel()

			release := make(chan struct{})
			completed := make(chan struct{})
			var owned sync.WaitGroup

			owned.Add(1)
			go func() {
				defer owned.Done()

				select {
				case <-release:
				case <-work.Done():
				}

				close(completed)
			}()
			admissionStopped := make(chan struct{})
			done := make(chan struct{})
			go func() {
				r.drain(work, &owned, nil, func() {
					if err := r.admit(context.Background()); !errors.Is(err, context.Canceled) {
						t.Errorf("shutdown admits work: %v", err)
					}

					if work.Err() != nil {
						t.Error("current work cancelled before drain")
					}

					close(admissionStopped)
				}, cancel)
				close(done)
			}()
			<-admissionStopped
			if !deadline {
				close(release)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("drain failed to finish within deadline")
			}

			<-completed
		})
	}
}

func TestDrainSupervisorPreservesUnexpectedFailures(t *testing.T) {
	for _, failure := range []error{nil, context.Canceled, errors.New("private worker failure")} {
		r, err := New(testConfig(t), Options{Diagnostics: observability.NewJSONSink(io.Discard)})
		if err != nil {
			t.Fatal(err)
		}

		r.draining = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var owned sync.WaitGroup

		results := make(chan error, 1)
		r.startRequired(ctx, &owned, results, "outbox_worker", func() error { return failure })
		owned.Wait()
		select {
		case result := <-results:
			if failure == nil || errors.Is(failure, context.Canceled) {
				t.Fatalf("expected graceful return was critical: %v", result)
			}

			var safe *StartupError

			if !errors.As(result, &safe) || safe.Stage != "outbox_worker" || safe.CorrelationID == "" {
				t.Fatalf("failure lost safe attribution: %v", result)
			}
		default:
			if failure != nil && !errors.Is(failure, context.Canceled) {
				t.Fatal("unexpected failure was suppressed during drain")
			}
		}
	}
}
