package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
)

// TestStartupProcessHelper runs the actual command composition in a separate
// process. Only this test binary recognizes the local API override; production
// configuration deliberately has no way to redirect Telegram credentials.
func TestStartupProcessHelper(t *testing.T) {
	if os.Getenv("REPLIPG_TEST_STARTUP_CHILD") != "1" {
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := execute(ctx, os.LookupEnv, func(cfg config.Config) (runtimeRunner, error) {
		runtime, err := app.New(cfg, app.Options{
			TelegramBaseURL: os.Getenv("REPLIPG_TEST_TELEGRAM_URL"),
			HTTPClient:      &http.Client{Timeout: 10 * time.Second},
		})
		if err != nil {
			return nil, err
		}

		return fakeRuntime{run: func(ctx context.Context) error {
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()
			select {
			case <-runtime.Ready():
				fmt.Println("ready")
				return <-done
			case err := <-done:
				return err
			}
		}}, nil
	}, os.Stderr)
	os.Exit(code)
}

func TestExecutableStartupReadinessAndWebhook(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		recover, webhookFailure bool
	}{
		{name: "fresh migrations"},
		{name: "recover before identity", recover: true},
		{name: "webhook failure stays not ready", webhookFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			dir := t.TempDir()
			if tc.recover {
				seedStartupProcess(t, ctx, dir)
			}
			identity, webhook := make(chan struct{}), make(chan struct{})
			requests := make(chan string, 8)
			offsets := make(chan int64, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				requests <- method
				switch method {
				case "getMe":
					select {
					case <-identity:
						_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"fixture"}}`)
					case <-r.Context().Done():
					}
				case "deleteWebhook":
					var payload struct {
						Drop bool `json:"drop_pending_updates"`
					}

					if err := json.NewDecoder(r.Body).Decode(&payload); (err != nil && !errors.Is(err, io.EOF)) || payload.Drop {
						t.Error("webhook cleanup must preserve queued updates")
					}

					select {
					case <-webhook:
						if tc.webhookFailure {
							w.WriteHeader(http.StatusUnauthorized)
							_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"fixture rejection"}`)
						} else {
							_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
						}
					case <-r.Context().Done():
					}
				case "getUpdates":
					var payload struct {
						Offset int64 `json:"offset"`
						Limit  int   `json:"limit"`
					}

					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Limit != 1 {
						t.Error("invalid completion-controlled polling request")
					}

					offsets <- payload.Offset
					if payload.Offset < 42 {
						_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":41,"edited_message":{}}]}`)
					} else {
						<-r.Context().Done()
					}
				default:
					t.Errorf("unexpected API operation %q", method)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)

			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStartupProcessHelper$")
			env := commandEnvironment()
			env[config.EnvStorageDir] = dir
			env[config.EnvPollTimeout] = "1s"
			// Supply only the test's configuration, excluding real operator secrets
			// and dotenv values inherited by the parent development environment.
			for key, value := range env {
				cmd.Env = append(cmd.Env, key+"="+value)
			}

			cmd.Env = append(cmd.Env, "REPLIPG_TEST_STARTUP_CHILD=1", "REPLIPG_TEST_TELEGRAM_URL="+server.URL)
			var diagnostic bytes.Buffer

			cmd.Stderr = &diagnostic
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}

			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}

			lines := make(chan string, 4)
			scanned := make(chan struct{})
			go func() {
				defer close(scanned)
				defer close(lines)

				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
				<-scanned
			})

			if got := startupReceive(t, ctx, requests); got != "getMe" {
				t.Fatalf("first request = %s", got)
			}

			db := openStartupDB(t, ctx, dir)
			var migrations int

			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&migrations); err != nil || migrations == 0 {
				t.Fatalf("startup migrations not applied: count=%d err=%v", migrations, err)
			}

			if tc.recover {
				for query, want := range map[string]string{
					"SELECT state FROM outbound_deliveries WHERE id='interrupted'": "reconciliation_required",
					"SELECT state FROM scheduled_jobs WHERE id='expired'":          "pending",
				} {
					var got string

					if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
						t.Fatalf("startup recovery: got=%s want=%s err=%v", got, want, err)
					}
				}
			}

			close(identity)
			if got := startupReceive(t, ctx, requests); got != "deleteWebhook" {
				t.Fatalf("second request = %s", got)
			}

			select {
			case line := <-lines:
				t.Fatalf("readiness signaled before webhook initialization: %q", line)
			default:
			}

			close(webhook)

			if !tc.webhookFailure {
				if line := startupReceive(t, ctx, lines); line != "ready" {
					t.Fatalf("missing owned polling handshake: %q", line)
				}

				wantOffset := int64(1)
				if tc.recover {
					wantOffset = 40
				}
				if got := startupReceive(t, ctx, offsets); got != wantOffset {
					t.Fatalf("initial offset = %d, want %d", got, wantOffset)
				}

				if got := startupReceive(t, ctx, offsets); got != 42 {
					t.Fatalf("backlog completion offset = %d, want 42", got)
				}

				if cursor, err := db.CompletedUpdate(ctx); err != nil || cursor != 41 {
					t.Fatalf("backlog not durably completed: cursor=%d err=%v", cursor, err)
				}

				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}

			err = cmd.Wait()
			waited = true
			<-scanned

			if tc.webhookFailure {
				if err == nil || !strings.Contains(diagnostic.String(), "intake_initialization") {
					t.Fatalf("webhook failure did not terminate with safe stage: %v %s", err, diagnostic.String())
				}

				select {
				case <-offsets:
					t.Fatal("polling began after webhook failure")
				default:
				}
			} else if err != nil {
				t.Fatalf("runtime exit: %v %s", err, diagnostic.String())
			}
			wantReady := 1
			if tc.webhookFailure {
				wantReady = 0
			}
			if got := strings.Count(diagnostic.String(), `"state":"ready"`); got != wantReady {
				t.Fatalf("ready transitions=%d, want %d: %s", got, wantReady, diagnostic.String())
			}

			if strings.Contains(diagnostic.String(), env[config.EnvBotToken]) || strings.Contains(diagnostic.String(), dir) {
				t.Fatal("process diagnostics exposed private configuration")
			}
		})
	}
}

func startupReceive[T any](t *testing.T, ctx context.Context, values <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-values:
		if !ok {
			t.Fatal("startup process output closed unexpectedly")
		}

		return value
	case <-ctx.Done():
		t.Fatal("startup integration synchronization timed out")
		var zero T

		return zero
	}
}

func openStartupDB(t *testing.T, ctx context.Context, dir string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db
}

func seedStartupProcess(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	db := openStartupDB(t, ctx, dir)
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}

	for _, statement := range []string{
		`INSERT INTO runtime_cursors(source,last_completed_id,updated_at) VALUES('telegram',39,1)`,
		`INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,created_at,updated_at) VALUES('interrupted','feature',1,X'01',-123,0,'attempting',3,1,10,1,1,1)`,
		`INSERT INTO scheduled_jobs(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,claim_owner,claim_correlation_id,lease_until,created_at,updated_at) VALUES('expired','feature',1,X'01',9223372036854775807,'claimed',3,1,10,1,'previous','previous',1,1,1)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
