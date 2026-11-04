package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const sensitiveFixture = "SENSITIVE_arbitrary-context-attribute_7vK!"

func TestJSONSinkEmitsOnlyAllowlistedOperationalFields(t *testing.T) {
	var output bytes.Buffer

	sink := NewJSONSink(&output)
	retryAt := time.Date(2026, time.October, 26, 8, 9, 10, 123000000, time.FixedZone("private-zone", 3*60*60))
	event, err := NewDiagnostic(
		CategoryDelivery,
		runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef"),
		"send_public",
		"retryable",
		SafeFields{
			Duration: 1500 * time.Millisecond,
			RetryAt:  retryAt,
			State:    "retrying",
			ChatID:   runtimecontract.ChatID(-100202),
		},
	)
	if err != nil {
		t.Fatalf("NewDiagnostic() error = %v", err)
	}

	ctx := context.WithValue(context.Background(), sensitiveContextKey{}, sensitiveFixture)
	sink.Emit(ctx, event)

	var record map[string]any

	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode diagnostic %q: %v", output.String(), err)
	}

	wantKeys := []string{"category", "chat_id", "correlation_id", "duration_ms", "operation", "outcome", "retry_at", "state"}
	gotKeys := make([]string, 0, len(record))
	for key := range record {
		gotKeys = append(gotKeys, key)
	}

	sort.Strings(gotKeys)

	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("record keys = %v, want %v; record=%s", gotKeys, wantKeys, output.String())
	}

	if record["category"] != "delivery" || record["correlation_id"] != "0123456789abcdef0123456789abcdef" || record["operation"] != "send_public" || record["outcome"] != "retryable" {
		t.Fatalf("core fields = %#v", record)
	}

	if record["duration_ms"] != float64(1500) || record["retry_at"] != "2026-10-26T05:09:10.123Z" || record["state"] != "retrying" {
		t.Fatalf("operational fields = %#v", record)
	}

	if record["chat_id"] != float64(-100202) {
		t.Fatalf("identifier fields = %#v", record)
	}

	if strings.Contains(output.String(), sensitiveFixture) || strings.Contains(output.String(), "private-zone") {
		t.Fatalf("diagnostic exposed sensitive context or time-zone data: %s", output.String())
	}
}

func TestMachineCodeShapedSensitiveValuesAreRejected(t *testing.T) {
	validCorrelation := runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef")
	fixtures := []string{"swordfish", "private_response", "raw_api_body", "opaque_payload"}
	for _, fixture := range fixtures {
		for _, field := range []string{"operation", "outcome", "state"} {
			t.Run(field+"/"+fixture, func(t *testing.T) {
				event := Diagnostic{
					Category:      CategoryDispatch,
					CorrelationID: validCorrelation,
					Operation:     "dispatch",
					Outcome:       "failed",
				}
				switch field {
				case "operation":
					event.Operation = fixture
				case "outcome":
					event.Outcome = fixture
				case "state":
					event.Fields.State = fixture
				}

				var output bytes.Buffer

				sink := NewJSONSink(&output)
				sink.Emit(context.Background(), event)

				if output.Len() != 0 {
					t.Fatalf("sensitive machine code emitted: %s", output.String())
				}

				_, err := NewDiagnostic(event.Category, event.CorrelationID, event.Operation, event.Outcome, event.Fields)
				var validation *ValidationError

				if !errors.As(err, &validation) {
					t.Fatalf("error = %T %v, want *ValidationError", err, err)
				}

				if validation.Field != field {
					t.Fatalf("field = %q, want %q", validation.Field, field)
				}

				if strings.Contains(err.Error(), fixture) {
					t.Fatalf("validation error exposed rejected value: %v", err)
				}
			})
		}
	}
}

func TestIdentifierPolicyAllowsOneCategoryAppropriateIdentifier(t *testing.T) {
	correlationID := runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef")
	tests := []struct {
		name     string
		category EventCategory
		fields   SafeFields
		key      string
	}{
		{name: "intake update", category: CategoryIntake, fields: SafeFields{UpdateID: 101}, key: "update_id"},
		{name: "dispatch update", category: CategoryDispatch, fields: SafeFields{UpdateID: 102}, key: "update_id"},
		{name: "callback acknowledgement update", category: CategoryCallbackAck, fields: SafeFields{UpdateID: 103}, key: "update_id"},
		{name: "delivery chat", category: CategoryDelivery, fields: SafeFields{ChatID: -104}, key: "chat_id"},
		{name: "delivery user", category: CategoryDelivery, fields: SafeFields{UserID: 105}, key: "user_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := NewDiagnostic(tt.category, correlationID, "state_change", "ok", tt.fields)
			if err != nil {
				t.Fatalf("NewDiagnostic() error = %v", err)
			}

			var output bytes.Buffer

			NewJSONSink(&output).Emit(context.Background(), event)
			var record map[string]any

			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatalf("decode diagnostic: %v", err)
			}

			if _, exists := record[tt.key]; !exists {
				t.Fatalf("expected %q in %s", tt.key, output.String())
			}
		})
	}
}

func TestIdentifierPolicyRejectsExcessiveAndWrongCategoryIdentifiers(t *testing.T) {
	correlationID := runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef")
	tests := []struct {
		name     string
		category EventCategory
		fields   SafeFields
		field    string
	}{
		{name: "intake multiple", category: CategoryIntake, fields: SafeFields{UpdateID: 101, ChatID: -102}, field: "identifiers"},
		{name: "delivery multiple", category: CategoryDelivery, fields: SafeFields{ChatID: -101, UserID: 102}, field: "identifiers"},
		{name: "delivery update", category: CategoryDelivery, fields: SafeFields{UpdateID: 101}, field: "update_id"},
	}
	for _, category := range []EventCategory{CategoryIntake, CategoryDispatch, CategoryCallbackAck} {
		tests = append(tests,
			struct {
				name     string
				category EventCategory
				fields   SafeFields
				field    string
			}{name: string(category) + " chat", category: category, fields: SafeFields{ChatID: -101}, field: "chat_id"},
			struct {
				name     string
				category EventCategory
				fields   SafeFields
				field    string
			}{name: string(category) + " user", category: category, fields: SafeFields{UserID: 101}, field: "user_id"},
		)
	}

	for _, category := range []EventCategory{CategoryStartup, CategoryReconciliation, CategoryScheduler, CategoryStorage, CategoryHealth, CategoryShutdown} {
		for _, identifier := range []struct {
			name   string
			fields SafeFields
			field  string
		}{
			{name: "update", fields: SafeFields{UpdateID: 101}, field: "update_id"},
			{name: "chat", fields: SafeFields{ChatID: -101}, field: "chat_id"},
			{name: "user", fields: SafeFields{UserID: 101}, field: "user_id"},
		} {
			tests = append(tests, struct {
				name     string
				category EventCategory
				fields   SafeFields
				field    string
			}{name: string(category) + " " + identifier.name, category: category, fields: identifier.fields, field: identifier.field})
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := Diagnostic{Category: tt.category, CorrelationID: correlationID, Operation: "state_change", Outcome: "ok", Fields: tt.fields}
			var output bytes.Buffer

			NewJSONSink(&output).Emit(context.Background(), direct)

			if output.Len() != 0 {
				t.Fatalf("invalid directly-built event emitted: %s", output.String())
			}

			_, err := NewDiagnostic(direct.Category, direct.CorrelationID, direct.Operation, direct.Outcome, direct.Fields)
			var validation *ValidationError

			if !errors.As(err, &validation) {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}

			if validation.Field != tt.field {
				t.Fatalf("field = %q, want %q", validation.Field, tt.field)
			}
		})
	}
}

func TestDeclaredOperationalVocabularyIsAccepted(t *testing.T) {
	correlationID := runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef")
	operations := []string{
		OperationValidate, OperationPrepare, OperationMigrate, OperationRecover,
		OperationPoll, OperationReceive, OperationDispatch, OperationAcknowledge,
		OperationSend, OperationSendPublic, OperationSendEphemeral, OperationAttempt,
		OperationReconcile, OperationSchedule, OperationExecute, OperationTransaction,
		OperationProbe, OperationStateChange, OperationDrain, OperationClose,
	}
	for _, operation := range operations {
		if _, err := NewDiagnostic(CategoryStartup, correlationID, operation, OutcomeOK, SafeFields{}); err != nil {
			t.Errorf("operation %q rejected: %v", operation, err)
		}
	}

	outcomes := []string{
		OutcomeOK, OutcomeAccepted, OutcomeCompleted, OutcomeIgnored, OutcomeSkipped,
		OutcomeFailed, OutcomeRetryable, OutcomeIndeterminate, OutcomePermanentFailure,
		OutcomeCancelled,
	}
	for _, outcome := range outcomes {
		if _, err := NewDiagnostic(CategoryStartup, correlationID, OperationStateChange, outcome, SafeFields{}); err != nil {
			t.Errorf("outcome %q rejected: %v", outcome, err)
		}
	}

	states := []string{
		StateStarting, StateRecovering, StateReady, StateDraining, StateStopped,
		StatePending, StateAttempting, StateClaimed, StateRetrying,
		StateReconciliationRequired, StateCompleted, StateCancelled,
		StatePermanentFailure,
	}
	for _, state := range states {
		if _, err := NewDiagnostic(CategoryStartup, correlationID, OperationStateChange, OutcomeOK, SafeFields{State: state}); err != nil {
			t.Errorf("state %q rejected: %v", state, err)
		}
	}
}

func TestJSONSinkSupportsEveryCategoryAndOmitsUnsetFields(t *testing.T) {
	var output bytes.Buffer

	sink := NewJSONSink(&output)
	categories := []EventCategory{
		CategoryStartup,
		CategoryIntake,
		CategoryDispatch,
		CategoryCallbackAck,
		CategoryDelivery,
		CategoryReconciliation,
		CategoryScheduler,
		CategoryStorage,
		CategoryHealth,
		CategoryShutdown,
	}
	for _, category := range categories {
		event, err := NewDiagnostic(category, runtimecontract.CorrelationID("abcdef0123456789abcdef0123456789"), "state_change", "ok", SafeFields{})
		if err != nil {
			t.Fatalf("NewDiagnostic(%q) error = %v", category, err)
		}

		sink.Emit(context.Background(), event)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")

	if len(lines) != len(categories) {
		t.Fatalf("emitted %d records, want %d: %s", len(lines), len(categories), output.String())
	}

	for i, line := range lines {
		var record map[string]any

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode line %d: %v", i, err)
		}

		if record["category"] != string(categories[i]) {
			t.Fatalf("line %d category = %v, want %q", i, record["category"], categories[i])
		}

		for _, omitted := range []string{"duration_ms", "retry_at", "state", "update_id", "chat_id", "user_id"} {
			if _, exists := record[omitted]; exists {
				t.Fatalf("unset field %q emitted in %s", omitted, line)
			}
		}
	}
}

func TestSensitiveOrInvalidValuesAreRejectedWithoutDisclosure(t *testing.T) {
	validCorrelation := runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef")
	tests := []struct {
		name      string
		sensitive string
		event     func(string) Diagnostic
		field     string
	}{
		{name: "bot credential", sensitive: "123456:BOT_TOKEN_DO_NOT_LOG!", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryDispatch, CorrelationID: validCorrelation, Operation: value, Outcome: "failed"}
		}, field: "operation"},
		{name: "secret config value", sensitive: "CONFIG_PASSWORD=do-not-log!", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryStartup, CorrelationID: validCorrelation, Operation: "validate", Outcome: value}
		}, field: "outcome"},
		{name: "message text", sensitive: "message text: swordfish!", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryDispatch, CorrelationID: validCorrelation, Operation: "dispatch", Outcome: value}
		}, field: "outcome"},
		{name: "private response", sensitive: "private response: hidden reward!", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryDelivery, CorrelationID: validCorrelation, Operation: "send", Outcome: "failed", Fields: SafeFields{State: value}}
		}, field: "state"},
		{name: "opaque payload", sensitive: `{"opaque_payload":"buried-secret!"}`, event: func(value string) Diagnostic {
			return Diagnostic{Category: EventCategory(value), CorrelationID: validCorrelation, Operation: "execute", Outcome: "failed"}
		}, field: "category"},
		{name: "raw api body", sensitive: `{"ok":false,"description":"raw-secret!"}`, event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryDelivery, CorrelationID: runtimecontract.CorrelationID(value), Operation: "send", Outcome: "failed"}
		}, field: "correlation_id"},
		{name: "raw error", sensitive: "dial tcp: raw-secret-error!", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryIntake, CorrelationID: validCorrelation, Operation: "poll", Outcome: value}
		}, field: "outcome"},
		{name: "filesystem path", sensitive: "/private/storage/tenant-secret.sqlite", event: func(value string) Diagnostic {
			return Diagnostic{Category: CategoryStorage, CorrelationID: validCorrelation, Operation: value, Outcome: "failed"}
		}, field: "operation"},
		{name: "negative duration", event: func(string) Diagnostic {
			return Diagnostic{Category: CategoryHealth, CorrelationID: validCorrelation, Operation: "probe", Outcome: "failed", Fields: SafeFields{Duration: -time.Second}}
		}, field: "duration"},
		{name: "invalid retry time", event: func(string) Diagnostic {
			return Diagnostic{Category: CategoryDelivery, CorrelationID: validCorrelation, Operation: "send", Outcome: "retryable", Fields: SafeFields{RetryAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}
		}, field: "retry_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer

			sink := NewJSONSink(&output)
			event := tt.event(tt.sensitive)
			sink.Emit(context.Background(), event)

			if output.Len() != 0 {
				t.Fatalf("invalid event emitted: %s", output.String())
			}

			_, err := NewDiagnostic(event.Category, event.CorrelationID, event.Operation, event.Outcome, event.Fields)
			var validation *ValidationError

			if !errors.As(err, &validation) {
				t.Fatalf("error = %T %v, want *ValidationError", err, err)
			}

			if validation.Field != tt.field {
				t.Fatalf("field = %q, want %q", validation.Field, tt.field)
			}

			if tt.sensitive != "" && strings.Contains(err.Error(), tt.sensitive) {
				t.Fatalf("validation error exposed rejected value: %v", err)
			}
		})
	}
}

func TestGenerateCorrelationIDIsOpaqueNonemptyAndUnique(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := make(map[runtimecontract.CorrelationID]struct{}, 512)
	for range 512 {
		id, err := GenerateCorrelationID()
		if err != nil {
			t.Fatalf("GenerateCorrelationID() error = %v", err)
		}

		if !pattern.MatchString(string(id)) {
			t.Fatalf("correlation ID %q has unexpected shape", id)
		}

		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate correlation ID %q", id)
		}

		seen[id] = struct{}{}
	}
}

func TestJSONSinkIsSafeForConcurrentUse(t *testing.T) {
	var output lockedBuffer

	sink := NewJSONSink(&output)
	event, err := NewDiagnostic(CategoryStorage, runtimecontract.CorrelationID("0123456789abcdef0123456789abcdef"), "transaction", "ok", SafeFields{Duration: time.Millisecond})
	if err != nil {
		t.Fatalf("NewDiagnostic() error = %v", err)
	}

	var workers sync.WaitGroup

	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()

			for range 20 {
				sink.Emit(context.Background(), event)
			}
		}()
	}

	workers.Wait()
	if lines := strings.Count(strings.TrimSpace(output.String()), "\n") + 1; lines != 640 {
		t.Fatalf("emitted %d records, want 640", lines)
	}
}

type sensitiveContextKey struct{}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.b.String()
}
