package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type lifecycleJobHandler func(context.Context, runtimecontract.WorkScope, scheduler.Job) scheduler.JobOutcome

func (h lifecycleJobHandler) Execute(ctx context.Context, scope runtimecontract.WorkScope, job scheduler.Job) scheduler.JobOutcome {
	return h(ctx, scope, job)
}

// lifecycleTransport keeps the actual Bot API adapter in the composition while
// exposing exact request ownership and a post-readiness fatal polling fault.
type lifecycleTransport struct {
	deadlineTransport
	fault     <-chan struct{}
	polling   chan struct{}
	cancelled chan error
	admit     func(context.Context) error
}

func (f *lifecycleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, "/getUpdates") {
		return f.deadlineTransport.RoundTrip(req)
	}

	f.active.Add(1)
	defer f.active.Add(-1)

	if f.requests.Add(1) == 1 {
		close(f.polling)
	}
	select {
	case <-req.Context().Done():
		f.cancelled <- f.admit(context.Background())
		return nil, req.Context().Err()
	case <-f.fault:
		return &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":409,"description":"private upstream conflict"}`)), Request: req}, nil
	}
}

func TestRuntimeLifecycleDrainsScheduledClaimAcrossIntakeFailureAndCancellation(t *testing.T) {
	for _, critical := range []bool{false, true} {
		t.Run(map[bool]string{false: "root_cancellation", true: "critical_intake_exit"}[critical], func(t *testing.T) {
			cfg := testConfig(t)
			cfg.HandlerTimeout, cfg.ClaimLease, cfg.ShutdownDeadline = time.Minute, time.Minute, time.Minute
			cfg.BusyTimeout = 5 * time.Second
			clock := testsupport.NewManualClock(time.Now())
			fault := make(chan struct{})
			transport := &lifecycleTransport{fault: fault, polling: make(chan struct{}), cancelled: make(chan error, 4)}
			h := &deadlineRuntime{clock: clock, done: make(chan struct{})}
			entered := make(chan context.Context, 1)
			returned := make(chan struct{})
			var executions, updates atomic.Int32

			payload, err := runtimecontract.NewVersionedPayload(1, []byte("opaque"))
			if err != nil {
				t.Fatal(err)
			}

			retry := runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: time.Second}
			jobRequest := func(id runtimecontract.JobID) runtimecontract.JobRequest {
				return runtimecontract.JobRequest{ID: id, Type: "lifecycle", DueAt: clock.Now().Add(-time.Hour), Payload: payload, Retry: retry}
			}
			h.runtime, err = New(cfg, Options{Diagnostics: observability.NewJSONSink(io.Discard), TelegramBaseURL: "http://telegram.test", HTTPClient: &http.Client{Transport: transport},
				Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
					var openErr error

					h.db, openErr = persistence.Open(ctx, opts)
					return h.db, openErr
				},

				Register: func(r Registrations) error {
					if _, err := h.db.ExecContext(context.Background(), `CREATE TABLE lifecycle_feature(value INTEGER)`); err != nil {
						return err
					}

					if err := r.Registry.RegisterGroupMessages(deadlineHandler(func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
						updates.Add(1)
						return interaction.HandlerResult{}, nil
					})); err != nil {
						return err
					}

					if err := r.Outbox.Register("lifecycle", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
						return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: "scheduled"}}, nil
					}); err != nil {
						return err
					}

					if err := r.Scheduler.Register("lifecycle", 1, lifecycleJobHandler(func(ctx context.Context, scope runtimecontract.WorkScope, _ scheduler.Job) scheduler.JobOutcome {
						executions.Add(1)
						defer close(returned)

						if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO lifecycle_feature VALUES(1)`); err != nil {
							return scheduler.JobOutcome{Kind: scheduler.JobFailed}
						}

						if _, err := scope.EnqueueDelivery(ctx, runtimecontract.DeliveryRequest{Kind: "lifecycle", Payload: payload, Retry: retry}); err != nil {
							return scheduler.JobOutcome{Kind: scheduler.JobFailed}
						}

						if _, err := scope.Schedule(ctx, jobRequest("child")); err != nil {
							return scheduler.JobOutcome{Kind: scheduler.JobFailed}
						}

						entered <- ctx
						<-ctx.Done()
						return scheduler.JobOutcome{Kind: scheduler.JobCompleted}
					})); err != nil {
						return err
					}

					r.Scheduler.SealRegistrations()
					return h.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
						for _, id := range []runtimecontract.JobID{"first", "second"} {
							if _, err := r.Scheduler.Schedule(ctx, tx, jobRequest(id)); err != nil {
								return err
							}
						}
						return nil
					})
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			transport.admit = h.runtime.admit
			admissionClosed := make(chan error, 1)
			h.runtime.newShutdownTimer = func(d time.Duration) runtimeTimer {
				admissionClosed <- h.runtime.admit(context.Background())
				return clock.NewTimer(d)
			}
			ctx, cancel := context.WithCancel(context.Background())
			h.cancel = cancel
			go func() { h.err = h.runtime.Run(ctx); close(h.done) }()
			t.Cleanup(func() {
				cancel()
				wait, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()

				timerReady := make(chan struct{})
				go func() { _ = clock.WaitForTimers(wait, 1); close(timerReady) }()
				select {
				case <-h.done:
					return
				case <-timerReady:
					clock.Advance(cfg.ShutdownDeadline)
				case <-wait.Done():
				}

				select {
				case <-h.done:
				case <-wait.Done():
					t.Error("runtime did not join owned work during cleanup")
				}
			})

			select {
			case <-h.runtime.Ready():
			case <-h.done:
				t.Fatalf("runtime failed before readiness: %v", h.err)
			case <-time.After(5 * time.Second):
				t.Fatal("runtime did not reach readiness")
			}

			waitDeadlineSignal(t, transport.polling, "owned poll")
			wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()

			var jobCtx context.Context

			select {
			case jobCtx = <-entered:
			case <-wait.Done():
				t.Fatal("scheduled transaction did not start")
			}

			if critical {
				close(fault)
			} else {
				cancel()
				select {
				case admissionErr := <-transport.cancelled:
					if !errors.Is(admissionErr, context.Canceled) {
						t.Fatalf("poll canceled before admission closed: %v", admissionErr)
					}
				case <-wait.Done():
					t.Fatal("drain did not cancel poll admission")
				}
			}
			select {
			case admissionErr := <-admissionClosed:
				if !errors.Is(admissionErr, context.Canceled) {
					t.Fatalf("shutdown still admits work: %v", admissionErr)
				}
			case <-wait.Done():
				t.Fatal("shutdown did not close admission")
			}

			if err := clock.WaitForTimers(wait, 1); err != nil {
				t.Fatal(err)
			}

			clock.Advance(cfg.ShutdownDeadline - time.Nanosecond)
			if jobCtx.Err() != nil {
				t.Fatal("shutdown canceled scheduled work before deadline")
			}

			select {
			case <-h.done:
				t.Fatal("runtime returned while scheduled transaction is active")
			default:
			}

			clock.Advance(time.Nanosecond)
			waitDeadlineSignal(t, jobCtx.Done(), "scheduled work cancellation")
			waitDeadlineSignal(t, returned, "scheduled handler return")
			waitDeadlineSignal(t, h.done, "supervisor shutdown")

			if critical {
				var failure *StartupError

				if !errors.As(h.err, &failure) || failure.Stage != "intake" || failure.CorrelationID == "" || strings.Contains(h.err.Error(), "private upstream") {
					t.Fatalf("critical intake exit lost safe failure: %v", h.err)
				}

				// stopped checks successful teardown independently of the expected failure.
				h.err = nil
			}

			h.stopped(t, &transport.deadlineTransport)

			if executions.Load() != 1 || updates.Load() != 0 || transport.requests.Load() != 1 || transport.sends.Load() != 0 {
				t.Fatalf("new work admitted during shutdown: jobs=%d updates=%d polls=%d sends=%d", executions.Load(), updates.Load(), transport.requests.Load(), transport.sends.Load())
			}

			db := reopenDeadlineDB(t, cfg)
			var featureRows, deliveries, children int

			if err := db.QueryRowContext(wait, `SELECT (SELECT count(*) FROM lifecycle_feature),(SELECT count(*) FROM outbound_deliveries),(SELECT count(*) FROM scheduled_jobs WHERE id='child')`).Scan(&featureRows, &deliveries, &children); err != nil {
				t.Fatal(err)
			}

			if featureRows != 0 || deliveries != 0 || children != 0 {
				t.Fatalf("canceled scope effects survived: feature=%d deliveries=%d children=%d", featureRows, deliveries, children)
			}

			assertLifecycleJob(t, wait, db, "first", "claimed", 1)
			assertLifecycleJob(t, wait, db, "second", "pending", 0)
			var lease int64

			if err := db.QueryRowContext(wait, `SELECT lease_until FROM scheduled_jobs WHERE id='first'`).Scan(&lease); err != nil {
				t.Fatal(err)
			}

			// Use the persisted lease boundary, without wall-clock sleeps or edits to
			// runtime tables, to prove the interrupted claim remains recoverable.
			recoveryClock := testsupport.NewManualClock(time.UnixMilli(lease).Add(time.Millisecond))
			jobs := scheduler.New(db, recoveryClock)
			if err := jobs.Recover(wait); err != nil {
				t.Fatal(err)
			}

			assertLifecycleJob(t, wait, db, "first", "pending", 1)
			if _, err := db.ExecContext(wait, `INSERT INTO lifecycle_feature VALUES(2)`); err != nil {
				t.Fatalf("reopened database cannot write after canceled transaction: %v", err)
			}
		})
	}
}

func assertLifecycleJob(t *testing.T, ctx context.Context, db *persistence.DB, id, wantState string, wantAttempts int) {
	t.Helper()
	var state string
	var attempts int

	if err := db.QueryRowContext(ctx, `SELECT state,attempt_count FROM scheduled_jobs WHERE id=?`, id).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}

	if state != wantState || attempts != wantAttempts {
		t.Fatalf("job %s = %s/%d, want %s/%d", id, state, attempts, wantState, wantAttempts)
	}
}
