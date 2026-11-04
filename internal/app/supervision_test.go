package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
)

func TestRequiredLoopExitRemovesReadiness(t *testing.T) {
	for _, exit := range []error{nil, errors.New("private cause")} {
		t.Run("exit", func(t *testing.T) {
			r, err := New(testConfig(t), Options{Diagnostics: observability.NewJSONSink(io.Discard)})
			if err != nil {
				t.Fatal(err)
			}

			for _, d := range []health.Dependency{health.Storage, health.Telegram, health.Intake, health.Outbox, health.Scheduler} {
				_ = r.state.SetDependency(d, true)
			}

			r.lifecycle(context.Background(), health.Ready)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var owned sync.WaitGroup

			results := make(chan error, 1)
			r.startRequired(ctx, &owned, results, "intake", func() error { return exit })
			owned.Wait()

			if r.state.Snapshot().Ready {
				t.Fatal("required loop exited while readiness remained true")
			}

			select {
			case err := <-results:
				var failure *StartupError

				if !errors.As(err, &failure) || failure.Stage != "intake" || failure.CorrelationID == "" {
					t.Fatalf("unsafe failure: %v", err)
				}
			default:
				t.Fatal("required loop exit was not reported")
			}
		})
	}
}

type waitingHandler struct{}

type failedHandler struct{}

func (failedHandler) Handle(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
	return interaction.HandlerResult{}, errors.New("feature failure")
}

func (waitingHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
	if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_writes(value) VALUES(1)`); err != nil {
		return interaction.HandlerResult{}, err
	}

	<-ctx.Done()
	return interaction.HandlerResult{}, ctx.Err()
}

func TestHandlerTimeoutRollsBackWithoutCompletingCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg := testConfig(t)
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

	if _, err = db.ExecContext(ctx, `CREATE TABLE feature_writes(value INTEGER)`); err != nil {
		t.Fatal(err)
	}

	registry := interaction.NewRegistry()
	if err = registry.RegisterCommand("wait", waitingHandler{}); err != nil {
		t.Fatal(err)
	}

	if err = registry.RegisterCommand("fail", failedHandler{}); err != nil {
		t.Fatal(err)
	}

	registry.Seal()
	router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Diagnostics: observability.NewJSONSink(io.Discard)})
	bounded := boundedRouter{UpdateRouter: router, timeout: 20 * time.Millisecond}
	event := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, Name: "wait"}
	outcome, err := bounded.Dispatch(ctx, event)
	if ctx.Err() != nil {
		t.Fatal("configured handler timeout did not interrupt dispatch")
	}

	if err == nil || outcome.Status != "" {
		t.Fatalf("interruption became terminal: %+v %v", outcome, err)
	}

	cursor, err := db.CompletedUpdate(ctx)
	if err != nil || cursor != 0 {
		t.Fatalf("cursor advanced: %v %v", cursor, err)
	}

	var count int

	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM feature_writes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("feature writes survived interruption: %d %v", count, err)
	}

	event.Update = 2
	event.Name = "fail"
	outcome, err = bounded.Dispatch(ctx, event)
	if err != nil || outcome.Status != persistence.UpdateHandlerFailed {
		t.Fatalf("ordinary feature failure lost its terminal outcome: %+v %v", outcome, err)
	}

	cursor, err = db.CompletedUpdate(ctx)
	if err != nil || cursor != 2 {
		t.Fatalf("terminal feature failure did not complete cursor: %v %v", cursor, err)
	}
}

type recordFailureRunner struct {
	failure   error
	continued chan struct{}
	calls     int
}

func (w *recordFailureRunner) RunOnce(ctx context.Context) (bool, error) {
	w.calls++
	if w.calls == 1 {
		return true, w.failure
	}

	close(w.continued)
	<-ctx.Done()
	return false, ctx.Err()
}

func (*recordFailureRunner) WaitNext(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func TestRecordFailuresKeepRequiredWorkersReady(t *testing.T) {
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprint(scheduled), func(t *testing.T) {
			r, err := New(testConfig(t), Options{Diagnostics: observability.NewJSONSink(io.Discard)})
			if err != nil {
				t.Fatal(err)
			}

			for _, d := range []health.Dependency{health.Storage, health.Telegram, health.Intake, health.Outbox, health.Scheduler} {
				_ = r.state.SetDependency(d, true)
			}

			r.lifecycle(context.Background(), health.Ready)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w := &recordFailureRunner{failure: outbox.ErrDeliveryNotCompleted, continued: make(chan struct{})}
			run := func() error { return r.runOutbox(ctx, w) }
			if scheduled {
				w.failure = scheduler.ErrJobNotCompleted
				run = func() error { return r.runScheduler(ctx, w) }
			}
			results := make(chan error, 1)
			var owned sync.WaitGroup

			r.startRequired(ctx, &owned, results, "worker", run)
			select {
			case <-w.continued:
			case <-time.After(time.Second):
				t.Fatal("record failure stopped required worker")
			}

			if !r.state.Snapshot().Ready {
				t.Fatal("record failure removed readiness")
			}

			cancel()
			owned.Wait()
			select {
			case err := <-results:
				t.Fatalf("cancellation reported as critical failure: %v", err)
			default:
			}
		})
	}
}

func TestCriticalIntakeAndHealthExitStopRuntime(t *testing.T) {
	for _, stage := range []string{"intake", "health_server", "storage"} {
		t.Run(stage, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case strings.HasSuffix(req.URL.Path, "/getMe"):
					_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`)
				case strings.HasSuffix(req.URL.Path, "/deleteWebhook"):
					_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				default:
					select {
					case <-req.Context().Done():
						return
					case <-release:
					}

					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"ok":false,"error_code":409,"description":"private conflict"}`)
				}
			}))
			defer server.Close()

			var releaseOnce sync.Once

			defer releaseOnce.Do(func() { close(release) })

			var listener net.Listener
			var database *persistence.DB

			r, err := New(testConfig(t), Options{Diagnostics: observability.NewJSONSink(io.Discard), TelegramBaseURL: server.URL, HTTPClient: server.Client(), Listen: func(string, string) (net.Listener, error) {
				var listenErr error

				listener, listenErr = net.Listen("tcp", "127.0.0.1:0")
				return listener, listenErr
			}, Open: func(ctx context.Context, options persistence.Options) (*persistence.DB, error) {
				var openErr error

				database, openErr = persistence.Open(ctx, options)
				return database, openErr
			}})
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- r.Run(ctx) }()
			select {
			case <-r.Ready():
			case <-ctx.Done():
				t.Fatal("runtime did not become ready")
			}

			switch stage {
			case "intake":
				releaseOnce.Do(func() { close(release) })
			case "storage":
				if err = database.Close(); err != nil {
					t.Fatal(err)
				}
			default:
				if err = listener.Close(); err != nil {
					t.Fatal(err)
				}
			}

			select {
			case err = <-done:
				var failure *StartupError

				if !errors.As(err, &failure) {
					t.Fatalf("critical exit did not return safe failure: %v", err)
				}

				if stage == "storage" && (failure.Stage == "outbox_worker" || failure.Stage == "scheduler_worker") {
					break
				}

				if failure.Stage != stage {
					t.Fatalf("critical exit did not retain safe stage: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("critical worker exit did not stop owned runtime")
			}

			if snapshot := r.state.Snapshot(); snapshot.Ready || snapshot.Lifecycle != health.Stopped {
				t.Fatalf("unsafe state: %+v", snapshot)
			}
		})
	}
}
