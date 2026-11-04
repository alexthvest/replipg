package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestShutdownDeadlineCancelsThenJoinsOwnedWork(t *testing.T) {
	cfg := testConfig(t)
	cfg.ShutdownDeadline = time.Minute
	r, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(io.Discard)})
	if err != nil {
		t.Fatal(err)
	}

	clock := testsupport.NewManualClock(time.Now())
	r.newShutdownTimer = func(d time.Duration) runtimeTimer { return clock.NewTimer(d) }
	work, cancel := context.WithCancel(context.Background())
	defer cancel()

	var owned sync.WaitGroup

	owned.Add(1)
	joined := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once

	defer releaseOnce.Do(func() { close(release) })

	go func() { defer owned.Done(); <-work.Done(); close(joined); <-release }()
	done := make(chan struct{})
	go func() { r.drain(work, &owned, nil, func() {}, cancel); close(done) }()
	wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()

	if err := clock.WaitForTimers(wait, 1); err != nil {
		t.Fatal(err)
	}

	clock.Advance(cfg.ShutdownDeadline - time.Nanosecond)
	if work.Err() != nil {
		t.Fatal("work canceled before deadline")
	}

	clock.Advance(time.Nanosecond)
	select {
	case <-joined:
	case <-wait.Done():
		t.Fatal("deadline did not cancel work")
	}

	select {
	case <-done:
		t.Fatal("drain returned before owned work joined")
	default:
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-wait.Done():
		t.Fatal("owned work not joined")
	}

	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("deadline timer leaked: %d", got)
	}
}

// deadlineTransport is a synchronous Bot API boundary: each blocked RoundTrip
// returns only after its request context is canceled. Unlike a remote HTTP
// handler, this gives an exact join point for the runtime-owned requests.
type deadlineTransport struct {
	update      <-chan struct{}
	sendRelease <-chan struct{}
	sendEntered chan context.Context
	requests    atomic.Int32
	sends       atomic.Int32
	active      atomic.Int32
}

func (f *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.active.Add(1)
	defer f.active.Add(-1)

	var body string

	switch {
	case strings.HasSuffix(req.URL.Path, "/getMe"):
		body = `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`
	case strings.HasSuffix(req.URL.Path, "/deleteWebhook"):
		body = `{"ok":true,"result":true}`
	case strings.HasSuffix(req.URL.Path, "/getUpdates"):
		if f.requests.Add(1) == 1 && f.update != nil {
			select {
			case <-f.update:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}

			body = `{"ok":true,"result":[{"update_id":41,"message":{"message_id":7,"from":{"id":8,"is_bot":false},"chat":{"id":-123,"type":"group"},"text":"hello"}}]}`
		} else {
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
	case strings.HasSuffix(req.URL.Path, "/sendMessage"):
		f.sends.Add(1)
		f.sendEntered <- req.Context()
		select {
		case <-f.sendRelease:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}

		body = `{"ok":true,"result":{"message_id":9,"chat":{"id":-123,"type":"group"},"date":1,"text":"sent"}}`
	default:
		return nil, errors.New("unexpected fake Bot API request")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

type deadlineHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (h deadlineHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	return h(ctx, scope, event)
}

type deadlineRuntime struct {
	runtime *Runtime
	clock   *testsupport.ManualClock
	db      *persistence.DB
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

func startDeadlineRuntime(t *testing.T, cfg config.Config, transport *deadlineTransport, register func(Registrations, *persistence.DB) error) *deadlineRuntime {
	t.Helper()
	h := &deadlineRuntime{clock: testsupport.NewManualClock(time.Now()), done: make(chan struct{})}
	options := Options{Diagnostics: observability.NewJSONSink(io.Discard), TelegramBaseURL: "http://telegram.test", HTTPClient: &http.Client{Transport: transport},
		Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
			db, err := persistence.Open(ctx, opts)
			h.db = db
			return db, err
		},

		Register: func(r Registrations) error {
			if register != nil {
				return register(r, h.db)
			}

			return nil
		},
	}
	var err error

	h.runtime, err = New(cfg, options)
	if err != nil {
		t.Fatal(err)
	}

	h.runtime.newShutdownTimer = func(d time.Duration) runtimeTimer { return h.clock.NewTimer(d) }
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.err = h.runtime.Run(ctx); close(h.done) }()
	t.Cleanup(func() {
		cancel()
		// Cancellation can precede timer creation on a failed assertion. Wait for
		// either shutdown completion or timer creation before advancing cleanup.
		wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()

		timerReady := make(chan struct{})
		go func() { _ = h.clock.WaitForTimers(wait, 1); close(timerReady) }()
		select {
		case <-h.done:
		case <-timerReady:
			h.clock.Advance(cfg.ShutdownDeadline)
		case <-wait.Done():
		}

		select {
		case <-h.done:
		case <-wait.Done():
			t.Error("runtime did not join during test cleanup")
		}
	})

	waitDeadlineSignal(t, h.runtime.Ready(), "runtime readiness")
	return h
}

func waitDeadlineSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func (h *deadlineRuntime) draining(t *testing.T) {
	t.Helper()
	h.cancel()
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()

	if err := h.clock.WaitForTimers(wait, 1); err != nil {
		t.Fatal(err)
	}

	if err := h.runtime.admit(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown admits work: %v", err)
	}

	select {
	case <-h.done:
		t.Fatal("runtime returned before admitted work finished")
	default:
	}
}

func (h *deadlineRuntime) stopped(t *testing.T, transport *deadlineTransport) {
	t.Helper()
	waitDeadlineSignal(t, h.done, "runtime shutdown")
	if h.err != nil {
		t.Fatal(h.err)
	}

	if got := h.clock.PendingTimers(); got != 0 {
		t.Fatalf("shutdown timer leaked: %d", got)
	}

	if got := transport.active.Load(); got != 0 {
		t.Fatalf("Bot API requests not joined: %d", got)
	}

	if _, err := h.db.ExecContext(context.Background(), "SELECT 1"); !errors.Is(err, persistence.ErrPersistenceClosed) {
		t.Fatalf("database not closed: %v", err)
	}

}

func reopenDeadlineDB(t *testing.T, cfg config.Config) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.OptionsFromConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestRuntimeDeadlineRollsBackHandlerAndPreservesCompletedWork(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "within_deadline", true: "deadline"}[expire], func(t *testing.T) {
			cfg := testConfig(t)
			cfg.HandlerTimeout, cfg.ClaimLease, cfg.ShutdownDeadline = time.Minute, time.Minute, time.Minute
			update := make(chan struct{})
			release := make(chan struct{})
			entered := make(chan context.Context, 1)
			returned := make(chan struct{})
			transport := &deadlineTransport{update: update}
			h := startDeadlineRuntime(t, cfg, transport, func(r Registrations, db *persistence.DB) error {
				if _, err := db.ExecContext(context.Background(), `CREATE TABLE deadline_feature(value INTEGER)`); err != nil {
					return err
				}

				return r.Registry.RegisterGroupMessages(deadlineHandler(func(ctx context.Context, scope runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
					defer close(returned)

					if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO deadline_feature(value) VALUES(1)`); err != nil {
						return interaction.HandlerResult{}, err
					}

					entered <- ctx
					select {
					case <-release:
						return interaction.HandlerResult{}, nil
					case <-ctx.Done():
						return interaction.HandlerResult{}, ctx.Err()
					}
				}))
			})
			close(update)
			var handlerCtx context.Context

			select {
			case handlerCtx = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not start")
			}

			h.draining(t)
			if handlerCtx.Err() != nil {
				t.Fatal("root cancellation canceled admitted handler")
			}

			h.clock.Advance(cfg.ShutdownDeadline - time.Nanosecond)
			if handlerCtx.Err() != nil {
				t.Fatal("handler canceled before deadline")
			}

			if expire {
				h.clock.Advance(time.Nanosecond)
				waitDeadlineSignal(t, handlerCtx.Done(), "handler cancellation")
			} else {
				close(release)
			}
			h.stopped(t, transport)
			waitDeadlineSignal(t, returned, "handler return")
			db := reopenDeadlineDB(t, cfg)
			var count int

			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM deadline_feature`).Scan(&count); err != nil {
				t.Fatal(err)
			}

			want, cursorWant := 1, runtimecontract.UpdateID(41)
			if expire {
				want, cursorWant = 0, 0
			}

			if count != want {
				t.Fatalf("feature rows = %d, want %d", count, want)
			}

			cursor, err := db.CompletedUpdate(context.Background())
			if err != nil || cursor != cursorWant {
				t.Fatalf("durable cursor = %d, %v; want %d", cursor, err, cursorWant)
			}

			_, found, err := db.TerminalOutcome(context.Background(), 41)
			if err != nil || found == expire {
				t.Fatalf("terminal outcome found = %v, error = %v", found, err)
			}

			if got := transport.requests.Load(); got != 1 {
				t.Fatalf("new intake after draining: %d polls", got)
			}
		})
	}
}

func TestRuntimeDeadlinePreservesAttemptingSendForStartupReconciliation(t *testing.T) {
	cfg := testConfig(t)
	cfg.ShutdownDeadline = time.Minute
	transport := &deadlineTransport{sendEntered: make(chan context.Context, 1)}
	var deliveryID runtimecontract.DeliveryID

	register := func(r Registrations, db *persistence.DB) error {
		err := r.Outbox.Register("deadline", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
			return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: "durable response"}}, nil
		})
		if err != nil {
			return err
		}

		if deliveryID != "" {
			return nil
		}

		payload, err := runtimecontract.NewVersionedPayload(1, []byte("payload"))
		if err != nil {
			return err
		}

		return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			var enqueueErr error

			deliveryID, enqueueErr = r.Outbox.Enqueue(ctx, tx, runtimecontract.DeliveryRequest{Kind: "deadline", Payload: payload, Retry: runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Millisecond, MaxDelay: time.Second}})
			return enqueueErr
		})
	}
	h := startDeadlineRuntime(t, cfg, transport, register)
	var sendCtx context.Context

	select {
	case sendCtx = <-transport.sendEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("durable send did not start")
	}

	h.draining(t)
	if sendCtx.Err() != nil {
		t.Fatal("root cancellation interrupted in-flight send")
	}

	h.clock.Advance(cfg.ShutdownDeadline - time.Nanosecond)
	if sendCtx.Err() != nil {
		t.Fatal("send canceled before deadline")
	}

	h.clock.Advance(time.Nanosecond)
	waitDeadlineSignal(t, sendCtx.Done(), "send cancellation")
	h.stopped(t, transport)
	db := reopenDeadlineDB(t, cfg)
	var state string

	if err := db.QueryRowContext(context.Background(), `SELECT state FROM outbound_deliveries WHERE id=?`, deliveryID).Scan(&state); err != nil {
		t.Fatal(err)
	}

	if state != "attempting" {
		t.Fatalf("interrupted send state = %s", state)
	}

	var unfinished int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_attempts WHERE delivery_id=? AND finished_at IS NULL`, deliveryID).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}

	if unfinished != 1 {
		t.Fatalf("unfinished durable attempts = %d", unfinished)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A fresh runtime must quarantine ambiguity before its registration callback
	// and readiness, and the ordinary worker must never resend quarantined work.
	restartedTransport := &deadlineTransport{sendEntered: make(chan context.Context, 1)}
	restarted := startDeadlineRuntime(t, cfg, restartedTransport, func(r Registrations, db *persistence.DB) error {
		var recovered string

		if err := db.QueryRowContext(context.Background(), `SELECT state FROM outbound_deliveries WHERE id=?`, deliveryID).Scan(&recovered); err != nil {
			return err
		}

		if recovered != "reconciliation_required" {
			return errors.New("ambiguous send not recovered before registration")
		}

		return register(r, db)
	})
	restarted.cancel()
	restarted.stopped(t, restartedTransport)
	if got := transport.sends.Load(); got != 1 {
		t.Fatalf("original sends = %d", got)
	}

	if got := restartedTransport.sends.Load(); got != 0 {
		t.Fatalf("recovered ambiguous work resent %d times", got)
	}

	recoveredDB := reopenDeadlineDB(t, cfg)
	if err := recoveredDB.QueryRowContext(context.Background(), `SELECT state FROM outbound_deliveries WHERE id=?`, deliveryID).Scan(&state); err != nil {
		t.Fatal(err)
	}

	if state != "reconciliation_required" {
		t.Fatalf("recovered state = %s", state)
	}
}
