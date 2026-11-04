package app

import (
	"context"
	"errors"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/testsupport"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type featureWorkerFunc func(context.Context) (bool, error)

func (f featureWorkerFunc) RunOnce(ctx context.Context) (bool, error) { return f(ctx) }
func TestWorkerNamesValidated(t *testing.T) {
	factory := func(runtimecontract.Transactor, runtimecontract.GroupMenuWriter) (Worker, error) {
		return featureWorkerFunc(func(context.Context) (bool, error) { return false, nil }), nil
	}
	for _, workers := range [][]WorkerFactory{{{Name: "", New: factory}}, {{Name: "menu"}}, {{Name: "menu", New: factory}, {Name: "menu", New: factory}}} {
		if _, err := New(testConfig(t), Options{Workers: workers}); err == nil {
			t.Fatal("invalid factories accepted")
		}
	}
}
func TestFeatureWorkerAdmissionAndFailures(t *testing.T) {
	r, err := New(testConfig(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	expected := errors.New("storage")
	if err := r.runFeatureWorker(context.Background(), featureWorkerFunc(func(context.Context) (bool, error) { return false, expected }), context.Background()); !errors.Is(err, expected) {
		t.Fatal(err)
	}
	admission, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := r.runFeatureWorker(context.Background(), featureWorkerFunc(func(context.Context) (bool, error) { called = true; return false, nil }), admission); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestFeatureFactoriesAndLivePortsBeforeAdmission(t *testing.T) {
	for _, mode := range []string{"error", "nil", "panic", "typed_nil"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			transport := &deadlineTransport{}
			registered := false
			r, err := New(cfg, Options{TelegramBaseURL: "http://telegram.invalid", HTTPClient: &http.Client{Transport: transport}, Diagnostics: observability.NewJSONSink(io.Discard), Listen: func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }, Register: func(ports Registrations) error {
				if ports.Membership == nil || ports.Menus == nil {
					return errors.New("missing live ports")
				}
				registered = true
				return nil
			}, Workers: []WorkerFactory{{Name: "menus", New: func(tx runtimecontract.Transactor, menus runtimecontract.GroupMenuWriter) (Worker, error) {
				if !registered || tx == nil || menus == nil {
					t.Error("factory dependencies/order")
				}
				switch mode {
				case "error":
					return nil, errors.New("private")
				case "panic":
					panic("private")
				case "typed_nil":
					var worker *drainRunner
					return worker, nil
				}
				return nil, nil
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			err = r.Run(context.Background())
			var failure *StartupError
			if !errors.As(err, &failure) || failure.Stage != "feature_worker_factory" {
				t.Fatal(err)
			}
			if transport.requests.Load() != 0 {
				t.Fatal("factory failure admitted polling")
			}
			select {
			case <-r.Ready():
				t.Fatal("factory failure became ready")
			default:
			}
		})
	}
}

func TestFeatureWorkerSupervisionAndDrain(t *testing.T) {
	for _, mode := range []string{"drain", "deadline", "critical", "panic"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.ShutdownDeadline = time.Minute
			clock := testsupport.NewManualClock(time.Now())
			transport := &deadlineTransport{}
			worker := &drainRunner{entered: make(chan context.Context, 1), release: make(chan struct{})}
			fault := make(chan struct{})
			r, err := New(cfg, Options{TelegramBaseURL: "http://telegram.invalid", HTTPClient: &http.Client{Transport: transport}, Diagnostics: observability.NewJSONSink(io.Discard), Listen: func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }, Workers: []WorkerFactory{{Name: "menus", New: func(runtimecontract.Transactor, runtimecontract.GroupMenuWriter) (Worker, error) {
				if mode == "critical" || mode == "panic" {
					return featureWorkerFunc(func(context.Context) (bool, error) {
						<-fault
						if mode == "panic" {
							panic("private")
						}
						return false, errors.New("private")
					}), nil
				}
				return worker, nil
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			r.newShutdownTimer = func(d time.Duration) runtimeTimer { return clock.NewTimer(d) }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- r.Run(ctx) }()
			wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			select {
			case <-r.Ready():
			case <-wait.Done():
				t.Fatal("not ready")
			}
			if mode == "critical" || mode == "panic" {
				close(fault)
			} else {
				var active context.Context
				select {
				case active = <-worker.entered:
				case <-wait.Done():
					t.Fatal("worker not started")
				}
				cancel()
				if err := clock.WaitForTimers(wait, 1); err != nil {
					t.Fatal(err)
				}
				if active.Err() != nil {
					t.Fatal("in-flight publication canceled at admission shutdown")
				}
				if mode == "deadline" {
					clock.Advance(cfg.ShutdownDeadline)
				} else {
					close(worker.release)
				}
			}
			select {
			case err := <-done:
				if mode == "critical" || mode == "panic" {
					var failure *StartupError
					if !errors.As(err, &failure) || failure.Stage != "menus" {
						t.Fatal(err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-wait.Done():
				t.Fatal("worker not joined")
			}
			if worker.calls.Load() > 1 {
				t.Fatal("new iteration admitted during shutdown")
			}
			if transport.active.Load() != 0 {
				t.Fatal("transport requests leaked")
			}
			if clock.PendingTimers() != 0 {
				t.Fatal("shutdown timer leaked")
			}
		})
	}
}

type timeoutPreparingHandler struct {
	prepare func(context.Context, interaction.Event) (interaction.Handler, error)
}

func (h timeoutPreparingHandler) Prepare(ctx context.Context, e interaction.Event) (interaction.Handler, error) {
	return h.prepare(ctx, e)
}
func (timeoutPreparingHandler) Handle(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
	panic("unprepared")
}
func TestPreparationSharesHandlerDeadlineAndLeavesUpdateIncomplete(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: cfg.StorageDir, StorageFilesystem: cfg.StorageFilesystem, BusyTimeout: cfg.BusyTimeout})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}
	registry := interaction.NewRegistry()
	var prepareDeadline time.Time
	h := timeoutPreparingHandler{prepare: func(ctx context.Context, _ interaction.Event) (interaction.Handler, error) {
		var ok bool
		prepareDeadline, ok = ctx.Deadline()
		if !ok {
			t.Error("preparation missing deadline")
		}
		return timeoutInteractionHandler(func(ctx context.Context, _ runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
			deadline, _ := ctx.Deadline()
			if !deadline.Equal(prepareDeadline) {
				t.Error("transaction got a separate timeout")
			}
			<-ctx.Done()
			return interaction.HandlerResult{}, ctx.Err()
		}), nil
	}}
	if err = registry.RegisterCommand("prepare", h); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Diagnostics: observability.NewJSONSink(io.Discard)})
	bounded := boundedRouter{UpdateRouter: router, timeout: 20 * time.Millisecond}
	event := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 7, ChatKind: runtimecontract.ChatKindGroup}, Name: "prepare"}
	outcome, err := bounded.Dispatch(ctx, event)
	if !errors.Is(err, context.DeadlineExceeded) || outcome.Status != "" {
		t.Fatalf("%+v %v", outcome, err)
	}
	cursor, err := db.CompletedUpdate(ctx)
	if err != nil || cursor != 0 {
		t.Fatalf("%v %v", cursor, err)
	}
	// Cancellation inside preparation must not acquire a transaction or complete the cursor.
	registry2 := interaction.NewRegistry()
	if err = registry2.RegisterCommand("prepare", timeoutPreparingHandler{prepare: func(ctx context.Context, _ interaction.Event) (interaction.Handler, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	registry2.Seal()
	bounded.UpdateRouter = interaction.NewRouter(registry2, db, db, nil, nil, interaction.RouterOptions{Diagnostics: observability.NewJSONSink(io.Discard)})
	outcome, err = bounded.Dispatch(ctx, event)
	if !errors.Is(err, context.DeadlineExceeded) || outcome.Status != "" {
		t.Fatalf("preparation timeout %+v %v", outcome, err)
	}
	cursor, err = db.CompletedUpdate(ctx)
	if err != nil || cursor != 0 {
		t.Fatalf("%v %v", cursor, err)
	}
}

type timeoutInteractionHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (f timeoutInteractionHandler) Handle(ctx context.Context, s runtimecontract.WorkScope, e interaction.Event) (interaction.HandlerResult, error) {
	return f(ctx, s, e)
}
