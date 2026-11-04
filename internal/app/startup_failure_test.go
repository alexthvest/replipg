package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const startupPrivateFixture = "private-startup-fixture"

func TestStartupInvalidConfigurationPreventsAcquisition(t *testing.T) {
	cfg := testConfig(t)
	cfg.StorageDir = startupPrivateFixture
	called := false
	_, err := New(cfg, Options{
		Open: func(context.Context, persistence.Options) (*persistence.DB, error) {
			called = true
			return nil, errors.New("unexpected acquisition")
		},
		Listen: func(string, string) (net.Listener, error) {
			called = true
			return nil, errors.New("unexpected acquisition")
		},
		Register: func(Registrations) error {
			called = true
			return nil
		},
	})
	var invalid *config.ConfigError
	if !errors.As(err, &invalid) || invalid.Setting != config.EnvStorageDir {
		t.Fatalf("expected typed storage setting failure, got %v", err)
	}
	if called || strings.Contains(err.Error(), startupPrivateFixture) || strings.Contains(err.Error(), cfg.BotToken()) {
		t.Fatal("invalid configuration acquired resources or exposed a sensitive value")
	}
}

// All failure injection uses existing composition boundaries or real SQLite
// fixtures. Constructors whose inputs are guaranteed by composition need no hooks.
func TestStartupFailuresUnwindAcquiredResources(t *testing.T) {
	cases := []struct {
		name, stage string
		fixture     []string
		migrate     bool
		requests    int
	}{
		{name: "storage", stage: "storage"},
		{name: "future_schema", stage: "migrations", fixture: []string{
			`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`,
			`INSERT INTO schema_migrations VALUES(5,1)`,
		}},
		{name: "failing_migration", stage: "migrations", fixture: []string{`CREATE TABLE processed_updates(poison TEXT)`}},
		{name: "listener", stage: "health_listener"},
		{name: "outbox_recovery", stage: "outbox_recovery", migrate: true, fixture: []string{
			`INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,created_at,updated_at) VALUES('interrupted','feature',1,X'01',-123,0,'attempting',3,1,10,1,1,1)`,
			`CREATE TRIGGER fail_recovery BEFORE UPDATE ON outbound_deliveries BEGIN SELECT RAISE(ABORT,'private-startup-fixture'); END`,
		}},
		{name: "scheduler_recovery", stage: "scheduler_recovery", migrate: true, fixture: []string{
			`INSERT INTO scheduled_jobs(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,claim_owner,claim_correlation_id,lease_until,created_at,updated_at) VALUES('expired','feature',1,X'01',1,'claimed',3,1,10,1,'previous','previous',1,1,1)`,
			`CREATE TRIGGER fail_recovery BEFORE UPDATE ON scheduled_jobs BEGIN SELECT RAISE(ABORT,'private-startup-fixture'); END`,
		}},
		{name: "registration", stage: "registrations", requests: 1},
		{name: "reconciliation", stage: "outbox_reconciliation", requests: 1, migrate: true, fixture: []string{
			`INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,created_at,updated_at) VALUES('interrupted','feature',1,X'01',-123,0,'attempting',3,1,10,1,1,1)`,
			`CREATE TRIGGER fail_resolution BEFORE UPDATE ON outbound_deliveries WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'private-startup-fixture'); END`,
		}},
		{name: "identity", stage: "telegram_identity", requests: 1},
		{name: "webhook", stage: "intake_initialization", requests: 2},
		{name: "cursor", stage: "intake_initialization", requests: 2, migrate: true, fixture: []string{
			`INSERT INTO runtime_cursors(source,last_completed_id,updated_at) VALUES('telegram',9223372036854775807,1)`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var db *persistence.DB
			var listener *startupListener
			var diagnostics bytes.Buffer
			requestCount, registrations, listens := 0, 0, 0
			options := Options{
				Diagnostics:     observability.NewJSONSink(&diagnostics),
				TelegramBaseURL: "http://telegram.invalid",
				HTTPClient: &http.Client{Transport: startupTransport(func(req *http.Request) (*http.Response, error) {
					requestCount++
					if tc.name == "identity" || tc.name == "webhook" && strings.HasSuffix(req.URL.Path, "/deleteWebhook") {
						return nil, errors.New(startupPrivateFixture)
					}
					return startupResponse(req), nil
				})},
				Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
					if tc.name == "storage" {
						return nil, errors.New(startupPrivateFixture)
					}
					var err error
					db, err = persistence.Open(ctx, opts)
					if err != nil {
						return nil, err
					}
					if tc.migrate {
						migrateStartupFixture(t, ctx, db)
					}
					for _, statement := range tc.fixture {
						if _, err := db.ExecContext(ctx, statement); err != nil {
							t.Fatal(err)
						}
					}
					return db, nil
				},
				Listen: func(string, string) (net.Listener, error) {
					listens++
					if tc.name == "listener" {
						return nil, errors.New(startupPrivateFixture)
					}
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						return nil, err
					}
					listener = &startupListener{Listener: ln, db: db}
					return listener, nil
				},
				Register: func(registrationsForStartup Registrations) error {
					registrations++
					if tc.name == "registration" {
						return errors.New(startupPrivateFixture)
					}
					if tc.name == "reconciliation" {
						return registrationsForStartup.Outbox.RegisterReconciler("feature", 1, startupReconciler{})
					}
					return nil
				},
			}
			app, err := New(cfg, options)
			if err != nil {
				t.Fatal(err)
			}
			err = app.Run(ctx)
			assertStartupFailure(t, app, err, tc.stage, diagnostics.String(), cfg)
			if requestCount != tc.requests {
				t.Fatalf("Telegram requests = %d, want %d", requestCount, tc.requests)
			}
			if tc.stage == "storage" || tc.stage == "migrations" {
				if listens != 0 {
					t.Fatal("listener acquired after storage failure")
				}
			}
			if tc.stage == "storage" || tc.stage == "migrations" || tc.stage == "health_listener" || strings.HasSuffix(tc.stage, "_recovery") {
				if registrations != 0 {
					t.Fatal("registration ran after earlier failure")
				}
			}
			assertStartupResourcesClosed(t, cfg, db, listener)
			if tc.name == "failing_migration" {
				reopened, err := persistence.Open(ctx, persistence.Options{StorageDir: cfg.StorageDir, StorageFilesystem: cfg.StorageFilesystem, BusyTimeout: cfg.BusyTimeout})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = reopened.Close() }()
				var count int
				if err := reopened.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name IN ('runtime_cursors','schema_migrations')`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatal("failed migration left partial schema")
				}
			}
		})
	}
}

type startupTransport func(*http.Request) (*http.Response, error)

type startupReconciler struct{}

func (startupReconciler) Reconcile(context.Context, outbox.Delivery) (outbox.ReconciliationResolution, error) {
	return outbox.ReconciliationResolution{Decision: outbox.ReconciliationConfirm, CorrelationID: "0123456789abcdef0123456789abcdef"}, nil
}

func (f startupTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func startupResponse(req *http.Request) *http.Response {
	body := `{"ok":true,"result":true}`
	if strings.HasSuffix(req.URL.Path, "/getMe") {
		body = `{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

type startupListener struct {
	net.Listener
	db       *persistence.DB
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func (l *startupListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	var value int
	l.closeErr = l.db.QueryRowContext(context.Background(), "SELECT 1").Scan(&value)
	return l.Listener.Close()
}

func migrateStartupFixture(t *testing.T, ctx context.Context, db *persistence.DB) {
	t.Helper()
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}
}

func assertStartupFailure(t *testing.T, app *Runtime, err error, stage, diagnostics string, cfg config.Config) {
	t.Helper()
	var failure *StartupError
	if !errors.As(err, &failure) || failure.Stage != stage || failure.CorrelationID == "" {
		t.Fatalf("failure = %v, want stage %s with correlation", err, stage)
	}
	if !strings.Contains(diagnostics, string(failure.CorrelationID)) || !strings.Contains(diagnostics, `"category":"startup"`) || !strings.Contains(diagnostics, `"outcome":"failed"`) {
		t.Fatalf("missing categorized correlated startup failure: %s", diagnostics)
	}
	for _, private := range []string{startupPrivateFixture, cfg.BotToken(), cfg.StorageDir} {
		if strings.Contains(err.Error()+diagnostics, private) {
			t.Fatal("startup error or diagnostic exposed sensitive fixture")
		}
	}
	if strings.Contains(diagnostics, `"state":"ready"`) {
		t.Fatal("startup failure transiently reported ready")
	}
	select {
	case <-app.Ready():
		t.Fatal("failed startup closed Ready")
	default:
	}
	if state := app.State().Snapshot(); state.Ready || state.Lifecycle != health.Stopped {
		t.Fatalf("failed startup state = %+v", state)
	}
}

func assertStartupResourcesClosed(t *testing.T, cfg config.Config, db *persistence.DB, listener *startupListener) {
	t.Helper()
	if listener != nil {
		listener.mu.Lock()
		closed, closeErr := listener.closed, listener.closeErr
		listener.mu.Unlock()
		if !closed || closeErr != nil {
			t.Fatalf("listener must close before database: closed=%v db query=%v", closed, closeErr)
		}
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
		if err == nil {
			_ = conn.Close()
			t.Fatal("health listener still accepts connections")
		}
	}
	if db == nil {
		return
	}
	var value int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&value); err == nil {
		t.Fatal("runtime database still open")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reopened, err := persistence.Open(ctx, persistence.Options{StorageDir: cfg.StorageDir, StorageFilesystem: cfg.StorageFilesystem, BusyTimeout: cfg.BusyTimeout})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE startup_unwind_probe(value INTEGER)`)
		return err
	}); err != nil {
		t.Fatalf("reopened database cannot acquire writer: %v", err)
	}
}

func TestStartupCancellationJoinsBlockedTelegramRequest(t *testing.T) {
	for _, stage := range []string{"identity", "webhook"} {
		t.Run(stage, func(t *testing.T) {
			cfg := testConfig(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, exited := make(chan struct{}), make(chan struct{})
			var db *persistence.DB
			var listener *startupListener
			var diagnostics bytes.Buffer
			app, err := New(cfg, Options{
				Diagnostics: observability.NewJSONSink(&diagnostics), TelegramBaseURL: "http://telegram.invalid",
				HTTPClient: &http.Client{Transport: startupTransport(func(req *http.Request) (*http.Response, error) {
					if stage == "webhook" && strings.HasSuffix(req.URL.Path, "/getMe") {
						return startupResponse(req), nil
					}
					close(entered)
					<-req.Context().Done()
					close(exited)
					return nil, req.Context().Err()
				})},
				Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
					var err error
					db, err = persistence.Open(ctx, opts)
					return db, err
				},
				Listen: func(string, string) (net.Listener, error) {
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						return nil, err
					}
					listener = &startupListener{Listener: ln, db: db}
					return listener, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- app.Run(ctx) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled startup did not join owned resources")
			}
			select {
			case <-exited:
			default:
				t.Fatal("HTTP request still running after Run returned")
			}
			wantStage := "telegram_identity"
			if stage == "webhook" {
				wantStage = "intake_initialization"
			}
			assertStartupFailure(t, app, err, wantStage, diagnostics.String(), cfg)
			assertStartupResourcesClosed(t, cfg, db, listener)
		})
	}
}
