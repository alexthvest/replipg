package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
	"github.com/alexthvest/replipg/internal/testsupport"
)

const privacyBody = "private-message-body-fixture"

const privacyPayload = "private-feature-payload-fixture"

const privacyReply = "private-response-content-fixture"

const privacyError = "private-raw-error-and-panic-fixture"

// Writers and readers synchronize explicitly: all composed services share this
// actual JSON sink, and tests wait on emitted records rather than worker sleeps.
type privacyLog struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func newPrivacyLog() *privacyLog { return &privacyLog{changed: make(chan struct{})} }

func (l *privacyLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	n, err := l.buffer.Write(p)
	close(l.changed)
	l.changed = make(chan struct{})
	return n, err
}

func (l *privacyLog) snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buffer.String()
}

func (l *privacyLog) wait(t *testing.T, ctx context.Context, category, outcome string, operation ...string) {
	t.Helper()
	for {
		l.mu.Lock()
		data, changed := l.buffer.String(), l.changed
		l.mu.Unlock()
		for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
			var event map[string]any

			if json.Unmarshal([]byte(line), &event) == nil && event["category"] == category && event["outcome"] == outcome && (len(operation) == 0 || event["operation"] == operation[0]) {
				return
			}
		}

		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatalf("missing %s/%s diagnostic; observed %s", category, outcome, data)
		}
	}
}

func assertPrivacyDiagnostics(t *testing.T, data string, private ...string) []map[string]any {
	t.Helper()
	for _, value := range private {
		if value != "" && strings.Contains(data, value) {
			t.Fatal("sensitive fixture appeared in operational output")
		}
	}

	var events []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var event map[string]any

		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid structured diagnostic: %v", err)
		}

		category, _ := event["category"].(string)
		correlation, _ := event["correlation_id"].(string)
		operation, _ := event["operation"].(string)
		outcome, _ := event["outcome"].(string)
		fields := observability.SafeFields{}
		identifiers := 0
		for key, value := range event {
			switch key {
			case "category", "correlation_id", "operation", "outcome", "state", "retry_at":
				if _, ok := value.(string); !ok {
					t.Fatalf("%s must be a string", key)
				}
			case "duration_ms", "update_id", "chat_id", "user_id":
				number, ok := value.(float64)

				if !ok {
					t.Fatalf("%s must be numeric", key)
				}

				switch key {
				case "duration_ms":
					fields.Duration = time.Duration(number * float64(time.Millisecond))
				case "update_id":
					fields.UpdateID = runtimecontract.UpdateID(number)
					identifiers++
				case "chat_id":
					fields.ChatID = runtimecontract.ChatID(number)
					identifiers++
				case "user_id":
					fields.UserID = runtimecontract.UserID(number)
					identifiers++
				}
			default:
				t.Fatalf("unexpected diagnostic field %q", key)
			}
		}

		if state, ok := event["state"].(string); ok {
			fields.State = state
		}
		if retry, ok := event["retry_at"].(string); ok {
			var err error

			fields.RetryAt, err = time.Parse(time.RFC3339Nano, retry)
			if err != nil {
				t.Fatal(err)
			}
		}

		if identifiers > 1 {
			t.Fatal("diagnostic retained multiple Telegram identifiers")
		}

		if _, err := observability.NewDiagnostic(observability.EventCategory(category), runtimecontract.CorrelationID(correlation), operation, outcome, fields); err != nil {
			t.Fatalf("invalid diagnostic envelope: %v", err)
		}

		events = append(events, event)
	}
	return events
}

func TestRuntimeStatusPrivacyWithEveryDurableState(t *testing.T) {
	cfg := testConfig(t)
	fake := testsupport.NewFakeTelegram(
		testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"test"}}`)},
		testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":true}`)},
		testsupport.TelegramResponse{WaitForCancellation: true},
	)
	defer fake.Close()

	logs := newPrivacyLog()
	var db *persistence.DB
	var listener net.Listener

	runtime, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(logs), TelegramBaseURL: fake.URL(), HTTPClient: fake.Client(),
		Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
			var err error

			db, err = persistence.Open(ctx, opts)
			return db, err
		},
		Listen: func(string, string) (net.Listener, error) {
			var err error

			listener, err = net.Listen("tcp", "127.0.0.1:0")
			return listener, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	}()

	select {
	case <-runtime.Ready():
	case <-ctx.Done():
		t.Fatal("runtime did not become ready")
	}

	// Install fixtures after startup recovery/reconciliation. Future due/lease
	// times keep actual running workers from changing this coherent snapshot.
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	deliveryStates := []string{"pending", "attempting", "retrying", "reconciliation_required", "completed", "permanent_failure"}
	jobStates := []string{"pending", "claimed", "retrying", "completed", "cancelled", "permanent_failure"}
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for index, state := range deliveryStates {
			for n := 0; n <= index; n++ {
				var next, terminal, reason any

				if state == "pending" || state == "retrying" {
					next = future
				}
				if state == "completed" || state == "permanent_failure" {
					terminal = int64(1)
				}
				if state == "permanent_failure" || state == "reconciliation_required" {
					reason = "permanent_failure"
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,target_user_id,authorization_kind,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,reason_code,created_at,updated_at,terminal_at) VALUES(?,?,1,?,-987654321,876543210,'administrator',0,?,3,1,10,1,?,?,1,1,?)`, fmt.Sprintf("private-delivery-%d-%d", index, n), privacyReply, []byte(privacyPayload), state, next, reason, terminal)
				if err != nil {
					return err
				}
			}
		}

		for index, state := range jobStates {
			for n := 0; n <= index; n++ {
				var next, owner, correlation, lease, terminal, reason any

				if state == "retrying" {
					next = future
				}
				if state == "claimed" {
					owner = privacyError
					correlation = privacyBody
					lease = future
				}
				if state == "completed" || state == "cancelled" || state == "permanent_failure" {
					terminal = int64(1)
				}
				if state == "permanent_failure" {
					reason = "permanent_failure"
				}
				_, err := tx.ExecContext(ctx, `INSERT INTO scheduled_jobs(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,claim_owner,claim_correlation_id,lease_until,reason_code,created_at,updated_at,terminal_at) VALUES(?,?,1,?,?,?,3,1,10,1,?,?,?,?,?,1,1,?)`, fmt.Sprintf("private-job-%d-%d", index, n), privacyReply, []byte(privacyPayload), future, state, next, owner, correlation, lease, reason, terminal)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()

	for _, path := range []string{"/livez", "/readyz", "/statusz"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+path, nil)
		if err != nil {
			t.Fatal(err)
		}

		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}

		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status %d", path, response.StatusCode)
		}

		for _, private := range []string{cfg.BotToken(), cfg.StorageDir, privacyBody, privacyPayload, privacyReply, privacyError, "private-delivery-", "private-job-", "987654321", "876543210"} {
			if strings.Contains(string(body), private) {
				t.Fatal("probe exposed private durable data")
			}
		}

		if path != "/statusz" {
			continue
		}

		// Comparing the complete JSON value rejects extra metadata/content fields,
		// missing zero-value fields, and state counts accidentally conflated together.
		var actual any

		if err := json.Unmarshal(body, &actual); err != nil {
			t.Fatal(err)
		}

		expectedJSON := `{"lifecycle":"ready","live":true,"ready":true,"deployment_mode":"single_instance","multi_instance_supported":false,"work":{"deliveries":{"pending":1,"attempting":2,"retrying":3,"reconciliation_required":4,"completed":5,"permanent_failure":6},"jobs":{"pending":1,"claimed":2,"retrying":3,"completed":4,"cancelled":5,"permanent_failure":6}}}`
		var expected any

		if err := json.Unmarshal([]byte(expectedJSON), &expected); err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("status aggregate differs: %s", body)
		}
	}

	assertPrivacyDiagnostics(t, logs.snapshot(), cfg.BotToken(), cfg.StorageDir, privacyBody, privacyPayload, privacyReply, privacyError)
}

func TestRuntimeDiagnosticPrivacyAcrossComposedFaults(t *testing.T) {
	cfg := testConfig(t)
	logs := newPrivacyLog()
	var db *persistence.DB

	// Poll requests are serial and the transport remains cancellation owned.
	poll := 0
	var sends, restricted atomic.Int32

	transport := startupTransport(func(req *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/getUpdates"):
			poll++
			if poll <= 5 {
				body := `{"ok":true,"result":[{"update_id":41,"message":{"message_id":7,"from":{"id":876543210,"is_bot":false},"chat":{"id":-987654321,"type":"group"},"text":"panic private-message-body-fixture"}},{"update_id":42,"message":{"message_id":8,"from":{"id":876543210,"is_bot":false},"chat":{"id":-987654321,"type":"group"},"text":"error private-message-body-fixture"}},{"update_id":43,"message":{"message_id":9,"from":{"id":876543210,"is_bot":false},"chat":{"id":-987654321,"type":"group"},"text":"reply private-message-body-fixture"}},{"update_id":44,"message":{"message_id":10,"from":{"id":876543210,"is_bot":false},"chat":{"id":-987654321,"type":"group"},"text":"ok private-message-body-fixture"}},{"update_id":45,"edited_message":{"text":"private-message-body-fixture"}}]}`
				var batch struct {
					Result []json.RawMessage `json:"result"`
				}

				if err := json.Unmarshal([]byte(body), &batch); err != nil {
					return nil, err
				}

				body = `{"ok":true,"result":[` + string(batch.Result[poll-1]) + `]}`
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}

			<-req.Context().Done()
			return nil, req.Context().Err()
		case strings.HasSuffix(req.URL.Path, "/sendMessage"):
			body, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}

			_ = req.Body.Close()
			sends.Add(1)
			if strings.Contains(string(body), "ephemeral_message_parameters") {
				restricted.Add(1)
			}
			// The real Telegram adapter must classify this without retaining the raw
			// upstream body or the response content in its safe diagnostic.
			return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400,"description":"private-raw-error-and-panic-fixture"}`)), Request: req}, nil
		default:
			return startupResponse(req), nil
		}
	})
	payload, err := runtimecontract.NewVersionedPayload(1, []byte(privacyPayload))
	if err != nil {
		t.Fatal(err)
	}

	retry := runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Second}
	response := runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -987654321, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: privacyReply}}
	runtime, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(logs), TelegramBaseURL: "http://telegram.test", HTTPClient: &http.Client{Transport: transport},
		Listen: func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
		Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
			var err error

			db, err = persistence.Open(ctx, opts)
			return db, err
		},
		Register: func(r Registrations) error {
			if err := r.Outbox.Register("privacy", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) { return response, nil }); err != nil {
				return err
			}

			if err := r.Registry.RegisterGroupMessages(deadlineHandler(func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
				message := event.(interaction.GroupMessage)
				switch {
				case strings.HasPrefix(message.Text, "panic"):
					panic(privacyError)
				case strings.HasPrefix(message.Text, "error"):
					return interaction.HandlerResult{}, errors.New(privacyError)
				case strings.HasPrefix(message.Text, "reply"):
					if _, err := scope.EnqueueDelivery(ctx, runtimecontract.DeliveryRequest{Kind: "privacy", Payload: payload, Retry: retry}); err != nil {
						return interaction.HandlerResult{}, err
					}

					privateResponse := runtimecontract.EphemeralResponse{Destination: response.Destination, TargetUserID: 876543210, Authorization: runtimecontract.AdministratorAuthorization{}, Content: runtimecontract.MessageContent{Text: privacyReply}}
					return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response, privateResponse}}, nil
				default:
					return interaction.HandlerResult{}, nil
				}
			})); err != nil {
				return err
			}

			if err := r.Scheduler.Register("privacy", 1, lifecycleJobHandler(func(context.Context, runtimecontract.WorkScope, scheduler.Job) scheduler.JobOutcome {
				panic(privacyError)
			})); err != nil {
				return err
			}

			r.Scheduler.SealRegistrations()
			return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				_, err := r.Scheduler.Schedule(ctx, tx, runtimecontract.JobRequest{ID: "private-job-fault", Type: "privacy", Payload: payload, DueAt: time.Now().Add(-time.Minute), Retry: retry})
				return err
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("runtime did not stop")
			}
		}
	}()

	select {
	case <-runtime.Ready():
	case <-ctx.Done():
		t.Fatal("runtime did not become ready")
	}

	logs.wait(t, ctx, "dispatch", "ignored")
	logs.wait(t, ctx, "delivery", "failed", "send_public")
	logs.wait(t, ctx, "delivery", "failed", "send_ephemeral")
	logs.wait(t, ctx, "delivery", "failed", "attempt")
	logs.wait(t, ctx, "scheduler", "failed") // panic was isolated and durably classified
	var jobState, reason string

	if err := db.QueryRowContext(ctx, `SELECT state,reason_code FROM scheduled_jobs WHERE id='private-job-fault'`).Scan(&jobState, &reason); err != nil {
		t.Fatal(err)
	}

	if jobState != "permanent_failure" || reason != "handler_panic" {
		t.Fatalf("scheduler panic not classified: %s/%s", jobState, reason)
	}

	var deliveryState string

	if err := db.QueryRowContext(ctx, `SELECT state FROM outbound_deliveries`).Scan(&deliveryState); err != nil {
		t.Fatal(err)
	}

	if deliveryState != "permanent_failure" {
		t.Fatalf("delivery state = %s", deliveryState)
	}

	rows, err := db.QueryContext(ctx, `SELECT update_id,status,correlation_id FROM processed_updates ORDER BY update_id`)
	if err != nil {
		t.Fatal(err)
	}

	correlations := make(map[int64]string)
	statuses := []string{"handler_failed", "handler_failed", "completed", "completed", "ignored"}
	index := 0
	for rows.Next() {
		var id int64
		var status, correlation string

		if err := rows.Scan(&id, &status, &correlation); err != nil {
			t.Fatal(err)
		}

		if index >= len(statuses) || id != int64(41+index) || status != statuses[index] {
			t.Fatal("dispatch fault isolation lost durable completion")
		}

		correlations[id] = correlation
		index++
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	_ = rows.Close()

	if index != len(statuses) {
		t.Fatal("missing completed update fixtures")
	}

	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not join")
	}

	if sends.Load() != 3 || restricted.Load() != 1 {
		t.Fatalf("unexpected composed deliveries: total=%d restricted=%d", sends.Load(), restricted.Load())
	}

	events := assertPrivacyDiagnostics(t, logs.snapshot(), cfg.BotToken(), cfg.StorageDir, privacyBody, privacyPayload, privacyReply, privacyError, "private-job-fault")
	dispatches := make(map[int64]int)
	for _, event := range events {
		if event["category"] != "dispatch" {
			continue
		}

		id, ok := event["update_id"].(float64)

		if !ok {
			t.Fatal("dispatch diagnostic lost update incident identifier")
		}

		if event["correlation_id"] != correlations[int64(id)] {
			t.Fatal("dispatch diagnostic lost durable correlation")
		}

		dispatches[int64(id)]++
	}

	for id := range correlations {
		if dispatches[id] != 1 {
			t.Fatalf("update %d emitted %d terminal dispatch diagnostics", id, dispatches[id])
		}
	}
}

func TestRuntimeStartupFaultDiagnosticPrivacy(t *testing.T) {
	cfg := testConfig(t)
	logs := newPrivacyLog()
	runtime, err := New(cfg, Options{Diagnostics: observability.NewJSONSink(logs),
		TelegramBaseURL: "http://telegram.invalid", HTTPClient: &http.Client{Transport: startupTransport(func(req *http.Request) (*http.Response, error) { return startupResponse(req), nil })},
		Listen:   func(string, string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
		Register: func(Registrations) error { return errors.New(privacyError + cfg.StorageDir + cfg.BotToken()) },
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = runtime.Run(ctx)
	var failure *StartupError

	if !errors.As(err, &failure) || failure.Stage != "registrations" {
		t.Fatal("startup fault did not retain safe stage")
	}

	events := assertPrivacyDiagnostics(t, logs.snapshot(), cfg.BotToken(), cfg.StorageDir, privacyError)
	assertPrivacyDiagnosticsError := err.Error()
	for _, private := range []string{cfg.BotToken(), cfg.StorageDir, privacyError} {
		if strings.Contains(assertPrivacyDiagnosticsError, private) {
			t.Fatal("startup error exposed raw cause")
		}
	}

	found := false
	for _, event := range events {
		if event["category"] == "startup" && event["outcome"] == "failed" {
			found = true

			if event["correlation_id"] != string(failure.CorrelationID) {
				t.Fatal("startup diagnostic correlation differs from returned error")
			}
		}
	}

	if !found {
		t.Fatal("missing categorized startup failure diagnostic")
	}

	if state := runtime.State().Snapshot(); state.Lifecycle != health.Stopped || state.Ready {
		t.Fatal("startup failure did not stop runtime")
	}
}
