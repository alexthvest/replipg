package interaction

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestDispatchDurableStoreStorageFailureRemainsRetryable(t *testing.T) {
	for _, table := range []string{"outbound_deliveries", "scheduled_jobs"} {
		for _, handling := range []string{"returned", "swallowed", "panic"} {
			t.Run(table+"/"+handling, func(t *testing.T) {
				ctx := context.Background()
				f := newDispatchFixture(t, t.TempDir())
				if _, err := f.db.ExecContext(ctx, `CREATE TRIGGER reject_effect BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT,'injected durable storage failure'); END`); err != nil {
					t.Fatal(err)
				}

				calls := 0
				if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
					calls++
					result, err := f.effects(ctx, scope, event)
					if err != nil {
						switch handling {
						case "swallowed":
							return HandlerResult{}, nil
						case "panic":
							panic("handler panic after failed durable effect")
						}
					}
					return result, err
				})); err != nil {
					t.Fatal(err)
				}

				f.registry.Seal()
				router := f.router()
				event := Command{Identity: testIdentity(), Name: "feature_action"}
				result, err := router.Dispatch(ctx, event)
				var txErr *persistence.TransactionError

				if !errors.As(err, &txErr) || txErr.Stage != persistence.TransactionStageCallback || !reflect.DeepEqual(result, DispatchOutcome{}) {
					t.Errorf("storage failure must return empty outcome and callback transaction error: outcome=%+v err=%v", result, err)
				}

				assertDispatchCounts(t, f.db, 0)
				if cursor, err := f.db.CompletedUpdate(ctx); err != nil || cursor != 0 {
					t.Errorf("storage failure advanced cursor: cursor=%d err=%v", cursor, err)
				}

				if _, err := f.db.ExecContext(ctx, `DROP TRIGGER reject_effect`); err != nil {
					t.Fatal(err)
				}

				result, err = router.Dispatch(ctx, event)
				if err != nil || result.Status != persistence.UpdateCompleted || calls != 2 {
					t.Fatalf("same update must retry after storage recovers: outcome=%+v calls=%d err=%v", result, calls, err)
				}

				assertDispatchCounts(t, f.db, 1)
				if cursor, err := f.db.CompletedUpdate(ctx); err != nil || cursor != event.UpdateID() {
					t.Fatalf("retry cursor=%d err=%v", cursor, err)
				}
			})
		}
	}
}
