package menus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

type lockedOwner struct {
	mu   sync.Mutex
	pool *Sessions
}

func (o *lockedOwner) Claim(_ context.Context, limit int) ([]Job, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pool.Claim(limit), nil
}

func (o *lockedOwner) Active(_ context.Context, job Job) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.pool.Active(job), nil
}

func (o *lockedOwner) Complete(_ context.Context, outcome Outcome) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pool.Complete(outcome)
	return nil
}

type delayedEditor struct {
	started    chan string
	release    chan struct{}
	mu         sync.Mutex
	inFlight   map[int]int
	overlapped bool
}

func (e *delayedEditor) Edit(ctx context.Context, _ core.GroupID, message int, view core.View) error {
	e.mu.Lock()
	e.inFlight[message]++
	if e.inFlight[message] > 1 {
		e.overlapped = true
	}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.inFlight[message]--
		e.mu.Unlock()
	}()
	text := view.Text[0].Text
	e.started <- text
	if text == "old" {
		select {
		case <-e.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestCoordinatorDelayedNavigationAndUnrelatedMessage(t *testing.T) {
	i := 0
	pool, _ := New(time.Minute, 4, time.Now, func() (string, error) {
		i++
		return string(rune('a' + i)), nil
	})
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	session, _ := pool.Create("inventory", scope, 1)
	_, _ = pool.Bind(session.Token, scope, 10)
	pool.Stage(session, core.View{Text: core.Literal("old")})
	owner := &lockedOwner{pool: pool}
	editor := &delayedEditor{
		started:  make(chan string, 8),
		release:  make(chan struct{}),
		inFlight: map[int]int{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Deliver(ctx, owner, editor)
	}()
	defer func() {
		cancel()
		<-done
	}()
	receive := func(want string) {
		t.Helper()
		select {
		case actual := <-editor.started:
			if actual != want {
				t.Fatalf("delivered %q, want %q", actual, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("delivery of %q stalled", want)
		}
	}
	receive("old")
	owner.mu.Lock()
	pool.Stage(session, core.View{Text: core.Literal("intermediate")})
	pool.Stage(session, core.View{Text: core.Literal("navigation")})
	other, _ := pool.Create("inventory", scope, 1)
	_, _ = pool.Bind(other.Token, scope, 11)
	pool.Stage(other, core.View{Text: core.Literal("unrelated")})
	owner.mu.Unlock()
	receive("unrelated")
	close(editor.release)
	receive("navigation")
	editor.mu.Lock()
	overlapped := editor.overlapped
	editor.mu.Unlock()
	if overlapped {
		t.Fatal("same message edited concurrently")
	}
}
