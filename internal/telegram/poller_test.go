package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type initializationAPI struct {
	delete func(context.Context, bool) error
}

func (a initializationAPI) DeleteWebhook(ctx context.Context, drop bool) error {
	return a.delete(ctx, drop)
}
func (a initializationAPI) GetUpdates(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
	panic("initialization must not poll")
}

type initializationCursor struct {
	load func(context.Context) (runtimecontract.UpdateID, error)
}

func (c initializationCursor) CompletedUpdate(ctx context.Context) (runtimecontract.UpdateID, error) {
	return c.load(ctx)
}

func TestPollerInitializationPreservesBacklogAndExactOffset(t *testing.T) {
	for _, cursor := range []runtimecontract.UpdateID{0, 41, math.MaxInt64 - 1} {
		t.Run(fmt.Sprint(cursor), func(t *testing.T) {
			var order []string
			p, err := NewPoller(initializationAPI{delete: func(_ context.Context, drop bool) error {
				if drop {
					t.Fatal("queued updates dropped")
				}
				order = append(order, "delete")
				return nil
			}}, initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) {
				order = append(order, "cursor")
				return cursor, nil
			}}, PollerOptions{TimeoutSeconds: 20})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.Started():
				t.Fatal("started before initialization")
			default:
			}
			if _, err := p.NextRequest(); !errors.Is(err, ErrPollerState) {
				t.Fatalf("uninitialized request: %v", err)
			}
			if err := p.Initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.Started():
			default:
				t.Fatal("initialization did not signal")
			}
			request, err := p.NextRequest()
			if err != nil {
				t.Fatal(err)
			}
			want := GetUpdatesRequest{Offset: int64(cursor) + 1, Limit: 1, Timeout: 20, AllowedUpdates: []string{"message", "callback_query"}}
			if !reflect.DeepEqual(request, want) {
				t.Fatalf("request=%+v want=%+v", request, want)
			}
			request.AllowedUpdates[0] = "changed"
			again, _ := p.NextRequest()
			if again.AllowedUpdates[0] != "message" {
				t.Fatal("request leaked mutable state")
			}
			if !reflect.DeepEqual(order, []string{"delete", "cursor"}) {
				t.Fatalf("order=%v", order)
			}
			if err := p.Initialize(context.Background()); !errors.Is(err, ErrPollerState) {
				t.Fatalf("second initialization: %v", err)
			}
		})
	}
}

func TestPollerInitializationFailuresNeverSignal(t *testing.T) {
	failure := errors.New("dependency failed")
	for _, name := range []string{"webhook", "cursor", "negative_cursor", "overflow_cursor", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cursorCalled := false
			p, err := NewPoller(initializationAPI{delete: func(context.Context, bool) error {
				if name == "webhook" {
					return failure
				}
				return nil
			}}, initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) {
				cursorCalled = true
				switch name {
				case "cursor":
					return 0, failure
				case "negative_cursor":
					return -1, nil
				case "overflow_cursor":
					return math.MaxInt64, nil
				}
				return 9, nil
			}}, PollerOptions{TimeoutSeconds: 20})
			if err != nil {
				t.Fatal(err)
			}
			if name == "cancelled" {
				cancel()
			}
			err = p.Initialize(ctx)
			if err == nil {
				t.Fatal("initialization succeeded")
			}
			if name == "webhook" || name == "cursor" {
				if !errors.Is(err, failure) {
					t.Fatalf("cause lost: %v", err)
				}
			}
			if name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if (name == "webhook" || name == "cancelled") && cursorCalled {
				t.Fatal("cursor loaded after failed prerequisite")
			}
			select {
			case <-p.Started():
				t.Fatal("failed initialization signaled")
			default:
			}
			if _, err := p.NextRequest(); !errors.Is(err, ErrPollerState) {
				t.Fatalf("failed request: %v", err)
			}
		})
	}
}

func TestPollerOwnsInitializationAndStopPreventsStartup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p, err := NewPoller(initializationAPI{delete: func(context.Context, bool) error { close(entered); <-release; return nil }}, initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) { return 4, nil }}, PollerOptions{TimeoutSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.Initialize(context.Background()) }()
	<-entered
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := p.Initialize(context.Background()); !errors.Is(err, ErrPollerState) {
				t.Errorf("concurrent ownership: %v", err)
			}
		})
	}
	wg.Wait()
	select {
	case <-p.Started():
		t.Fatal("started before prerequisites")
	default:
	}
	p.StopAccepting()
	p.StopAccepting()
	close(release)
	if err := <-done; !errors.Is(err, ErrPollerState) {
		t.Fatalf("stopped startup: %v", err)
	}
	select {
	case <-p.Started():
		t.Fatal("stopped initialization signaled")
	default:
	}
}

func TestPollerStopClosesAdmission(t *testing.T) {
	p, err := NewPoller(initializationAPI{delete: func(context.Context, bool) error { return nil }}, initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) { return 0, nil }}, PollerOptions{TimeoutSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.StopAccepting()
	if _, err := p.NextRequest(); !errors.Is(err, ErrPollerState) {
		t.Fatalf("stopped admission: %v", err)
	}
}

func TestPollerRejectsInvalidDependencies(t *testing.T) {
	api := initializationAPI{delete: func(context.Context, bool) error { return nil }}
	cursor := initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) { return 0, nil }}
	var typedAPI *initializationAPI
	var typedCursor *initializationCursor
	for _, tt := range []struct {
		name    string
		api     PollingAPI
		cursor  CompletedCursor
		options PollerOptions
	}{
		{name: "missing_api", cursor: cursor},
		{name: "typed_nil_api", api: typedAPI, cursor: cursor},
		{name: "missing_cursor", api: api},
		{name: "typed_nil_cursor", api: api, cursor: typedCursor},
		{name: "negative_timeout", api: api, cursor: cursor, options: PollerOptions{TimeoutSeconds: -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewPoller(tt.api, tt.cursor, tt.options); !errors.Is(err, ErrInvalidPoller) {
				t.Fatalf("invalid configuration: %v", err)
			}
		})
	}
}

func TestPollerCancelledCursorCannotPublishStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := NewPoller(initializationAPI{delete: func(context.Context, bool) error { return nil }}, initializationCursor{load: func(context.Context) (runtimecontract.UpdateID, error) { cancel(); return 9, nil }}, PollerOptions{TimeoutSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Initialize(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cursor: %v", err)
	}
	select {
	case <-p.Started():
		t.Fatal("cancelled startup signaled")
	default:
	}
	if _, err := p.NextRequest(); !errors.Is(err, ErrPollerState) {
		t.Fatalf("cancelled request: %v", err)
	}
}
