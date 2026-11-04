package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()

	values := map[string]string{config.EnvBotToken: "123:secret", config.EnvStorageDir: t.TempDir(), config.EnvStorageFilesystem: "local", config.EnvHealthAddress: "127.0.0.1:8080", config.EnvPollTimeout: "1s", config.EnvBusyTimeout: "100ms", config.EnvHandlerTimeout: "1s", config.EnvRetryInitial: "1ms", config.EnvRetryMaximum: "1s", config.EnvRetryAttempts: "3", config.EnvClaimLease: "5s", config.EnvShutdownDeadline: "2s", config.EnvDeploymentMode: "single"}
	cfg, err := config.NewLoader(func(k string) (string, bool) { v, ok := values[k]; return v, ok }).Load()
	if err != nil {
		t.Fatal(err)
	}

	return cfg
}

func TestNewRejectsInvalidConfigurationBeforeSideEffects(t *testing.T) {
	called := false
	_, err := New(config.Config{}, Options{Listen: func(string, string) (net.Listener, error) { called = true; return nil, nil }})
	if err == nil || called {
		t.Fatalf("invalid configuration accepted or side effects occurred: %v %v", err, called)
	}
}

func TestStartupRegistersRecoversAndPreservesBacklogBeforeReady(t *testing.T) {
	fake := testsupport.NewFakeTelegram(testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`)}, testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":true}`)}, testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":[{"update_id":41,"edited_message":{}}]}`)}, testsupport.TelegramResponse{WaitForCancellation: true})
	defer fake.Close()
	registered := false
	app, err := New(testConfig(t), Options{TelegramBaseURL: fake.URL(), HTTPClient: fake.Client(), Register: func(r Registrations) error {
		registered = true

		if r.Registry == nil || r.Outbox == nil || r.Scheduler == nil {
			return errors.New("missing registrations")
		}

		return nil
	}, Listen: func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }})
	if err != nil {
		t.Fatal(err)
	}

	if app.State().Snapshot().Ready {
		t.Fatal("ready before startup")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	if err := fake.WaitForRequests(ctx, 4); err != nil {
		t.Fatal(err)
	}

	select {
	case <-app.Ready():
	case <-ctx.Done():
		t.Fatal("startup did not reach ready")
	}

	if !registered || !app.State().Snapshot().Ready {
		t.Fatal("not ready after startup handshake")
	}

	requests := fake.Requests()
	if !strings.HasSuffix(requests[0].Path, "/getMe") || !strings.HasSuffix(requests[1].Path, "/deleteWebhook") || strings.Contains(string(requests[1].Body), `"drop_pending_updates":true`) {
		t.Fatalf("wrong initialization: %+v", requests)
	}

	if !strings.Contains(string(requests[2].Body), `"offset":1`) || !strings.Contains(string(requests[3].Body), `"offset":42`) {
		t.Fatal("owned polling did not durably process preserved backlog")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owned loops did not stop")
	}

	if s := app.State().Snapshot(); s.Ready || s.Lifecycle != health.Stopped {
		t.Fatalf("shutdown state: %+v", s)
	}
}

func TestHealthRemainsNotReadyWhileIdentityBlockedAfterRecovery(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: cfg.StorageDir, StorageFilesystem: cfg.StorageFilesystem, BusyTimeout: cfg.BusyTimeout})
	if err != nil {
		t.Fatal(err)
	}

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err = db.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}

	if _, err = db.ExecContext(ctx, `INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,created_at,updated_at) VALUES('interrupted','feature',1,X'01',-123,0,'attempting',3,1,10,1,1,1)`); err != nil {
		t.Fatal(err)
	}

	if _, err = db.ExecContext(ctx, `INSERT INTO scheduled_jobs(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,claim_owner,claim_correlation_id,lease_until,created_at,updated_at) VALUES('expired','feature',1,X'01',1,'claimed',3,1,10,1,'previous','previous',1,1,1)`); err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	fake := testsupport.NewFakeTelegram(testsupport.TelegramResponse{WaitForCancellation: true})
	defer fake.Close()

	var listener net.Listener
	var runtimeDB *persistence.DB
	registered := false
	app, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(io.Discard), TelegramBaseURL: fake.URL(), HTTPClient: fake.Client(), Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
		runtimeDB, err = persistence.Open(ctx, opts)
		return runtimeDB, err
	}, Listen: func(string, string) (net.Listener, error) {
		var listenErr error
		listener, listenErr = net.Listen("tcp", "127.0.0.1:0")

		return listener, listenErr
	}, Register: func(Registrations) error {
		var delivery, job string

		if err := runtimeDB.QueryRowContext(ctx, `SELECT state FROM outbound_deliveries WHERE id='interrupted'`).Scan(&delivery); err != nil {
			return err
		}

		if err := runtimeDB.QueryRowContext(ctx, `SELECT state FROM scheduled_jobs WHERE id='expired'`).Scan(&job); err != nil {
			return err
		}

		if delivery != "reconciliation_required" || job != "pending" {
			return errors.New("recovery did not finish before registration")
		}

		registered = true

		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	if err = fake.WaitForRequests(ctx, 1); err != nil {
		t.Fatal(err)
	}

	// Identity request observation synchronizes startup writes before this probe.
	if !registered {
		t.Fatal("identity verification preceded recovery and registration")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusServiceUnavailable || app.State().Snapshot().Ready {
		t.Fatal("ready during blocked identity verification")
	}

	select {
	case <-app.Ready():
		t.Fatal("readiness handshake closed before intake initialized")
	default:
	}

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("failed identity did not abort startup")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not unwind")
	}
}
