package interaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type dispatchDiagnosticFunc func(context.Context, observability.Diagnostic)

func (f dispatchDiagnosticFunc) Emit(ctx context.Context, event observability.Diagnostic) {
	f(ctx, event)
}

func TestDispatchTerminalDiagnosticsFollowCommit(t *testing.T) {
	for _, scenario := range []string{"completed", "ignored", "decode failed", "handler failed", "commit failed", "unsafe rollback"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			f := newDispatchFixture(t, dir)
			observer := openDispatchDB(t, dir)
			calls := 0
			if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
				calls++
				result, err := f.effects(ctx, scope, event)
				if err != nil {
					return result, err
				}

				switch scenario {
				case "handler failed", "unsafe rollback":
					return HandlerResult{}, errors.New("private feature error")
				case "commit failed":
					_, err = scope.DBTX().ExecContext(ctx, `INSERT INTO dispatch_child(parent_id) VALUES (404)`)
				}
				return result, err
			})); err != nil {
				t.Fatal(err)
			}

			f.registry.Seal()
			router := f.router()
			const correlation runtimecontract.CorrelationID = "0123456789abcdef0123456789abcdef"

			router.options.NewCorrelationID = func() (runtimecontract.CorrelationID, error) { return correlation, nil }
			if scenario == "unsafe rollback" {
				router.transactions = rollbackFailureTransactor{delegate: f.db}
			}
			var output bytes.Buffer

			jsonSink := observability.NewJSONSink(&output)
			emissions := 0
			router.options.Diagnostics = dispatchDiagnosticFunc(func(ctx context.Context, diagnostic observability.Diagnostic) {
				emissions++
				// A second connection must see the terminal record and cursor
				// at emission time, not just after Dispatch returns.
				stored, exists, err := observer.TerminalOutcome(ctx, 42)
				if err != nil || !exists || stored.CorrelationID != diagnostic.CorrelationID {
					t.Fatalf("diagnostic before terminal commit: %+v, %v, %v", stored, exists, err)
				}

				cursor, err := observer.CompletedUpdate(ctx)
				if err != nil || cursor != 42 {
					t.Fatalf("diagnostic before cursor commit: %d, %v", cursor, err)
				}

				wantEffects := 0
				if scenario == "completed" {
					wantEffects = 1
				}
				var effects int

				if err := observer.QueryRowContext(ctx, `SELECT count(*) FROM dispatch_feature`).Scan(&effects); err != nil || effects != wantEffects {
					t.Fatalf("diagnostic before feature commit/rollback: %d, %v", effects, err)
				}

				jsonSink.Emit(ctx, diagnostic)
			})
			event := Command{Identity: testIdentity(), Name: "feature_action", Arguments: "private personal message and bot credential"}
			if scenario == "ignored" {
				event.Name = "private_unowned_route"
			}
			dispatch := func() (DispatchOutcome, error) {
				if scenario == "decode failed" {
					return router.RecordDecodeFailure(context.Background(), 42)
				}

				return router.Dispatch(context.Background(), event)
			}
			result, err := dispatch()

			if scenario == "commit failed" || scenario == "unsafe rollback" {
				if err == nil || !reflect.DeepEqual(result, DispatchOutcome{}) || emissions != 0 || output.Len() != 0 {
					t.Fatalf("uncommitted outcome=%+v err=%v emissions=%d output=%s", result, err, emissions, &output)
				}

				return
			}

			if err != nil || len(result.ImmediateResponses) != 0 {
				t.Fatalf("dispatch=%+v, %v", result, err)
			}

			wantOutcome := observability.OutcomeCompleted
			if scenario == "ignored" {
				wantOutcome = observability.OutcomeIgnored
			} else if scenario != "completed" {
				wantOutcome = observability.OutcomeFailed
			}
			var record map[string]any

			if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record); err != nil {
				t.Fatalf("missing single operator diagnostic: %q, %v", output.String(), err)
			}

			want := map[string]any{"category": "dispatch", "operation": "dispatch", "outcome": wantOutcome, "correlation_id": string(result.CorrelationID), "update_id": float64(42)}

			if emissions != 1 || !reflect.DeepEqual(record, want) {
				t.Fatalf("diagnostics=%v emissions=%d, want %v", record, emissions, want)
			}

			before := output.String()
			callsBefore := calls
			// Replays retain the original correlation and emit no fresh
			// completed/ignored/failed processing diagnostic.
			router.options.NewCorrelationID = func() (runtimecontract.CorrelationID, error) { return "abcdef0123456789abcdef0123456789", nil }
			if replay, err := dispatch(); err != nil || replay.CorrelationID != result.CorrelationID || output.String() != before || emissions != 1 || calls != callsBefore {
				t.Fatalf("replay=%+v err=%v emissions=%d calls=%d", replay, err, emissions, calls)
			}

			if _, err := router.RecordIgnored(context.Background(), 41); err != nil || output.String() != before || emissions != 1 {
				t.Fatalf("stale update emitted fresh diagnostic: err=%v emissions=%d", err, emissions)
			}
		})
	}
}
