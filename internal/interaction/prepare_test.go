package interaction

import (
	"context"
	"errors"
	"testing"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type preparingTestHandler struct {
	prepare func(context.Context, Event) (Handler, error)
}

func (h preparingTestHandler) Prepare(ctx context.Context, e Event) (Handler, error) {
	return h.prepare(ctx, e)
}
func (preparingTestHandler) Handle(context.Context, runtimecontract.WorkScope, Event) (HandlerResult, error) {
	panic("unprepared handler")
}
func TestPreparationBeforeTransactionAndRecoverableFailure(t *testing.T) {
	for _, mode := range []string{"success", "error", "panic", "nil"} {
		t.Run(mode, func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			called := false
			h := preparingTestHandler{prepare: func(ctx context.Context, e Event) (Handler, error) {
				// A write here would block if dispatch already held the single writer.
				if _, err := f.db.ExecContext(ctx, "INSERT INTO dispatch_feature(value) VALUES('prepared')"); err != nil {
					return nil, err
				}
				switch mode {
				case "error":
					return nil, errors.New("secret")
				case "panic":
					panic("secret")
				case "nil":
					return nil, nil
				}
				return dispatchHandlerFunc(func(context.Context, runtimecontract.WorkScope, Event) (HandlerResult, error) {
					called = true
					return HandlerResult{}, nil
				}), nil
			}}
			if err := f.registry.RegisterCommand("prepare", h); err != nil {
				t.Fatal(err)
			}
			f.registry.Seal()
			_, err := f.router().Dispatch(context.Background(), Command{Identity: Identity{Update: 1, Chat: -100, User: 2, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "prepare"})
			if (err == nil) != (mode == "success") || called != (mode == "success") {
				t.Fatalf("called=%v error=%v", called, err)
			}
			var count int
			if err := f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM processed_updates").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if mode != "success" && count != 0 {
				t.Fatal("preparation failure completed update")
			}
		})
	}
}
