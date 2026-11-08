package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

func TestRegistryAndHooks(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()
	var order []string

	for _, name := range []string{"groups", "characters"} {
		h := Hook{
			Name: name,
			Run: func(_ context.Context, _ donburi.World) error {
				order = append(order, name)
				return nil
			},
		}
		if name == "characters" {
			h.Requires = []string{"groups"}
		}

		if err := r.Hook(h); err != nil {
			t.Fatal(err)
		}
	}

	handler, err := Bind(r, "future-feature", func(
		_ context.Context,
		w donburi.World,
		scope core.Scope,
		input int,
	) (core.View, error) {
		if w.Len() != 0 || scope.Group != -1 || input != 7 {
			t.Fatal("wrong typed operation")
		}
		return core.View{Text: core.Literal("ok")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err = r.Route("future-feature"); err == nil {
		t.Fatal("duplicate route")
	}

	e, err := New(r, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if err = r.Route("after-freeze"); err == nil {
		t.Fatal("mutable registry")
	}

	if err = e.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if len(order) != 2 || order[0] != "groups" || order[1] != "characters" {
		t.Fatal(order)
	}

	view, err := handler(ctx, e, core.Scope{
		Group: -1,
		Actor: 1,
	}, 7)
	if err != nil || view.Text[0].Text != "ok" {
		t.Fatalf("result: %v %v", view, err)
	}

	if err = e.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err = handler(ctx, e, core.Scope{
		Group: -1,
		Actor: 1,
	}, 7); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}

	bad := NewRegistry()
	_ = bad.Hook(Hook{
		Name:     "inventory",
		Requires: []string{"missing"},
		Run: func(context.Context, donburi.World) error {
			return nil
		},
	})
	if _, err = New(bad, 1, time.Second); err == nil {
		t.Fatal("missing hook dependency accepted")
	}
}

func TestQueueCancellationAndDrain(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()
	_ = r.Route("test")
	e, err := New(r, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if err = e.Start(ctx); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	go func() {
		_, err := e.Submit(ctx, "test", scope, func(context.Context, donburi.World) (core.View, error) {
			close(entered)
			<-release
			return core.View{}, nil
		})
		first <- err
	}()
	<-entered
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = e.Submit(cancelled, "test", scope, func(context.Context, donburi.World) (core.View, error) {
		t.Error("cancelled work executed")
		return core.View{}, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	wait.Add(8)

	for range 8 {
		go func() {
			defer wait.Done()

			_, err := e.Submit(ctx, "test", scope, func(context.Context, donburi.World) (core.View, error) {
				return core.View{}, nil
			})
			if err != nil && !errors.Is(err, ErrStopped) {
				t.Error(err)
			}
		}()
	}

	stopped := make(chan error, 1)
	go func() {
		stopped <- e.Stop(ctx)
	}()
	close(release)
	if err = <-first; err != nil {
		t.Fatal(err)
	}

	if err = <-stopped; err != nil {
		t.Fatal(err)
	}

	wait.Wait()
}

func TestShutdownCancelsInflightAtDeadline(t *testing.T) {
	r := NewRegistry()
	_ = r.Route("test")
	e, err := New(r, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if err = e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := e.Submit(context.Background(), "test", core.Scope{
			Group: -1,
			Actor: 1,
		}, func(ctx context.Context, _ donburi.World) (core.View, error) {
			close(entered)
			<-ctx.Done()
			return core.View{}, ctx.Err()
		})
		result <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = e.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if err = <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestProjectionFailureStopsOwner(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Route("project")
	engine, err := New(registry, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if err = engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err = engine.Submit(context.Background(), "project", core.Scope{
		Group: -1,
		Actor: 1,
	}, func(context.Context, donburi.World) (core.View, error) {
		return core.View{}, &ProjectionError{Cause: errors.New("injected post-commit projection failure")}
	})
	var projection *ProjectionError
	if !errors.As(err, &projection) {
		t.Fatal(err)
	}

	<-engine.Done()
	if _, err = engine.Submit(context.Background(), "project", core.Scope{
		Group: -1,
		Actor: 1,
	}, func(context.Context, donburi.World) (core.View, error) {
		t.Error("divergent world served")
		return core.View{}, nil
	}); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}

	if err = engine.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
