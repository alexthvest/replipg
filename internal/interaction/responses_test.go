package interaction

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func publicDispatchResponse() runtimecontract.PublicResponse {
	return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -100, Kind: runtimecontract.ChatKindSupergroup}, Content: runtimecontract.MessageContent{Text: "response", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Length: 8}}, Buttons: [][]runtimecontract.InlineButton{{{Text: "action", CallbackData: "feature:action"}}}}}
}

func ephemeralDispatchResponse(now time.Time) runtimecontract.EphemeralResponse {
	return runtimecontract.EphemeralResponse{Destination: publicDispatchResponse().Destination, TargetUserID: 123, Authorization: &runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: now.Add(time.Second)}, Content: publicDispatchResponse().Content}
}

func TestDispatchReturnsImmediateResponsesOnlyAfterCommit(t *testing.T) {
	dir := t.TempDir()
	f := newDispatchFixture(t, dir)
	observer := openDispatchDB(t, dir)
	public := publicDispatchResponse()
	ephemeral := ephemeralDispatchResponse(f.now)
	returned := []runtimecontract.ResponseRequest{&public, &ephemeral}
	expected := []runtimecontract.ResponseRequest{publicDispatchResponse(), ephemeralDispatchResponse(f.now)}
	calls := 0
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
		calls++
		if _, err := f.effects(ctx, scope, event); err != nil {
			return HandlerResult{}, err
		}

		return HandlerResult{ImmediateResponses: returned}, nil
	})); err != nil {
		t.Fatal(err)
	}

	f.registry.Seal()
	r := f.router()
	inspected := false
	r.cursors = inspectingCursorStore{CursorStore: f.db, inspect: func(tx runtimecontract.DBTX) {
		inspected = true
		assertDispatchCounts(t, tx, 1)
		assertDispatchCounts(t, observer, 0)
		// The handler owns these values. Changing them after validation must not
		// change the requests eventually handed to the transport.
		public.Content.Text = "mutated"
		public.Content.Entities[0].Kind = runtimecontract.TextEntityItalic
		public.Content.Buttons[0][0].Text = "mutated"
		ephemeral.Authorization.(*runtimecontract.CallbackAuthorization).CallbackQueryID = "mutated"
		ephemeral.Content.Buttons[0] = nil
		returned[0] = nil
	}}
	event := Command{Identity: testIdentity(), Name: "feature_action"}
	result, err := r.Dispatch(context.Background(), event)
	if err != nil || result.Status != persistence.UpdateCompleted || !inspected {
		t.Fatalf("dispatch = %+v %v inspected=%v", result, err, inspected)
	}

	if !reflect.DeepEqual(result.ImmediateResponses, expected) {
		t.Fatalf("responses = %#v, want %#v", result.ImmediateResponses, expected)
	}

	assertDispatchCounts(t, observer, 1)
	replay, err := r.Dispatch(context.Background(), event)
	if err != nil || replay.Status != persistence.UpdateCompleted || len(replay.ImmediateResponses) != 0 || calls != 1 {
		t.Fatalf("duplicate = %+v %v calls=%d", replay, err, calls)
	}

	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openDispatchDB(t, dir)
	assertDispatchCounts(t, reopened, 1)
}

func TestDispatchRejectsInvalidImmediateResponses(t *testing.T) {
	cases := []struct {
		name     string
		response func(time.Time) runtimecontract.ResponseRequest
		field    string
		reason   runtimecontract.ValidationReason
	}{
		{"nil", func(time.Time) runtimecontract.ResponseRequest { return nil }, "response", runtimecontract.ReasonRequired},
		{"typed nil", func(time.Time) runtimecontract.ResponseRequest { return (*runtimecontract.PublicResponse)(nil) }, "response", runtimecontract.ReasonRequired},
		{"private destination", func(time.Time) runtimecontract.ResponseRequest {
			r := publicDispatchResponse()
			r.Destination.ChatID = 123
			return r
		}, "destination.chat_id", runtimecontract.ReasonOutOfRange},
		{"empty text", func(time.Time) runtimecontract.ResponseRequest {
			r := publicDispatchResponse()
			r.Content.Text = ""
			return r
		}, "content.text", runtimecontract.ReasonRequired},
		{"entity range", func(time.Time) runtimecontract.ResponseRequest {
			r := publicDispatchResponse()
			r.Content.Entities[0].Length = 999
			return r
		}, "content.entities.range", runtimecontract.ReasonOutOfRange},
		{"button data", func(time.Time) runtimecontract.ResponseRequest {
			r := publicDispatchResponse()
			r.Content.Buttons[0][0].CallbackData = ""
			return r
		}, "content.buttons.callback_data", runtimecontract.ReasonRequired},
		{"target", func(now time.Time) runtimecontract.ResponseRequest {
			r := ephemeralDispatchResponse(now)
			r.TargetUserID = 0
			return r
		}, "target.user_id", runtimecontract.ReasonRequired},
		{"bot target", func(now time.Time) runtimecontract.ResponseRequest {
			r := ephemeralDispatchResponse(now)
			r.TargetIsBot = true
			return r
		}, "target.user_id", runtimecontract.ReasonInvalid},
		{"missing authorization", func(now time.Time) runtimecontract.ResponseRequest {
			r := ephemeralDispatchResponse(now)
			r.Authorization = nil
			return r
		}, "authorization", runtimecontract.ReasonRequired},
		{"expired authorization", func(now time.Time) runtimecontract.ResponseRequest {
			r := ephemeralDispatchResponse(now)
			r.Authorization.(*runtimecontract.CallbackAuthorization).ValidUntil = now
			return r
		}, "authorization.valid_until", runtimecontract.ReasonOutOfRange},
		{"replacement", func(now time.Time) runtimecontract.ResponseRequest {
			r := ephemeralDispatchResponse(now)
			r.ReplaceCallbackMessage = true
			r.Authorization.(*runtimecontract.CallbackAuthorization).OriginatedFromEphemeral = true
			return r
		}, "authorization.replace_callback_message", runtimecontract.ReasonInconsistent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			calls := 0
			if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
				calls++
				if calls > 1 {
					return HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{publicDispatchResponse()}}, nil
				}

				if _, err := f.effects(ctx, scope, event); err != nil {
					return HandlerResult{}, err
				}

				return HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{publicDispatchResponse(), tc.response(f.now)}}, nil
			})); err != nil {
				t.Fatal(err)
			}

			f.registry.Seal()
			r := f.router()
			sink := &diagnosticCapture{}
			r.options.Diagnostics = sink
			event := Command{Identity: testIdentity(), Name: "feature_action"}
			result, err := r.Dispatch(context.Background(), event)
			var validation *runtimecontract.ValidationError

			if !errors.As(err, &validation) || validation.Field != tc.field || validation.Reason != tc.reason {
				t.Fatalf("validation = %v, want %s/%s", err, tc.field, tc.reason)
			}

			if result.Status != persistence.UpdateHandlerFailed || len(result.ImmediateResponses) != 0 {
				t.Fatalf("invalid result = %+v", result)
			}

			for _, table := range []string{"dispatch_feature", "outbound_deliveries", "scheduled_jobs"} {
				var n int

				if err := f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
					t.Fatal(err)
				}

				if n != 0 {
					t.Fatalf("%s residue = %d", table, n)
				}
			}

			terminal, found, err := f.db.TerminalOutcome(context.Background(), 42)
			if err != nil || !found || terminal.Status != persistence.UpdateHandlerFailed || terminal.FailureCode != "invalid_response" {
				t.Fatalf("terminal = %+v %v %v", terminal, found, err)
			}

			if cursor, err := f.db.CompletedUpdate(context.Background()); err != nil || cursor != 42 {
				t.Fatalf("cursor=%d %v", cursor, err)
			}

			if len(sink.events) != 1 || sink.events[0].CorrelationID != result.CorrelationID {
				t.Fatalf("diagnostics = %+v", sink.events)
			}

			replay, err := r.Dispatch(context.Background(), event)
			if err != nil || replay.Status != persistence.UpdateHandlerFailed || len(replay.ImmediateResponses) != 0 || calls != 1 || len(sink.events) != 1 {
				t.Fatalf("duplicate = %+v %v calls=%d", replay, err, calls)
			}

			event.Update = 43
			next, err := r.Dispatch(context.Background(), event)
			if err != nil || next.Status != persistence.UpdateCompleted || len(next.ImmediateResponses) != 1 {
				t.Fatalf("next = %+v %v", next, err)
			}
		})
	}
}

func TestDispatchDiscardsImmediateResponsesOnFailure(t *testing.T) {
	for _, scenario := range []string{"handler", "commit", "cursor", "cancel", "record invalid failure"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if scenario == "cursor" || scenario == "record invalid failure" {
				if _, err := f.db.ExecContext(ctx, `CREATE TRIGGER reject_response_cursor BEFORE INSERT ON runtime_cursors BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
					t.Fatal(err)
				}
			}

			if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
				if _, err := f.effects(ctx, scope, event); err != nil {
					return HandlerResult{}, err
				}

				result := HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{publicDispatchResponse()}}
				switch scenario {
				case "handler":
					return result, errors.New("handler failed")
				case "commit":
					_, err := scope.DBTX().ExecContext(ctx, `INSERT INTO dispatch_child(parent_id) VALUES (404)`)
					return result, err
				case "cancel":
					cancel()
				case "record invalid failure":
					result.ImmediateResponses = append(result.ImmediateResponses, nil)
				}
				return result, nil
			})); err != nil {
				t.Fatal(err)
			}

			f.registry.Seal()
			result, err := f.router().Dispatch(ctx, Command{Identity: testIdentity(), Name: "feature_action"})

			if len(result.ImmediateResponses) != 0 {
				t.Fatalf("responses escaped rollback: %+v", result)
			}

			if scenario == "handler" {
				if err != nil || result.Status != persistence.UpdateHandlerFailed {
					t.Fatalf("handler failure = %+v %v", result, err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(result, DispatchOutcome{}) {
					t.Fatalf("failure = %+v %v", result, err)
				}

				assertDispatchCounts(t, f.db, 0)
			}
		})
	}
}
