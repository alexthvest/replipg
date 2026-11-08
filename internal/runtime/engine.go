// Package runtime serializes registered operations on one ECS world.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

var ErrStopped = errors.New("runtime stopped")

type Work func(context.Context, donburi.World) (core.View, error)

type Hook struct {
	Name     string
	Requires []string
	Run      func(context.Context, donburi.World) error
	Cleanup  func(donburi.World)
}

type Registry struct {
	names  map[string]bool
	hooks  []Hook
	frozen bool
}

func NewRegistry() *Registry {
	return &Registry{names: map[string]bool{}}
}

func (r *Registry) Route(name string) error {
	if r.frozen {
		return errors.New("registry frozen")
	}
	if name == "" || r.names[name] {
		return fmt.Errorf("duplicate or empty route %q", name)
	}

	r.names[name] = true
	return nil
}

func (r *Registry) Hook(h Hook) error {
	if r.frozen {
		return errors.New("registry frozen")
	}
	if h.Name == "" || h.Run == nil {
		return errors.New("invalid hook")
	}

	for _, old := range r.hooks {
		if old.Name == h.Name {
			return fmt.Errorf("duplicate hook %q", h.Name)
		}
	}

	r.hooks = append(r.hooks, h)
	return nil
}

func (r *Registry) freeze() error {
	seen := map[string]bool{}

	for _, h := range r.hooks {
		for _, dep := range h.Requires {
			if !seen[dep] {
				return fmt.Errorf("hook %s requires earlier hook %s", h.Name, dep)
			}
		}

		seen[h.Name] = true
	}

	r.frozen = true
	return nil
}

type result struct {
	view core.View
	err  error
}

type request struct {
	ctx    context.Context
	work   Work
	result chan result
}

type Engine struct {
	registry  *Registry
	queue     chan request
	stopping  chan struct{}
	done      chan struct{}
	admission sync.RWMutex
	stopOnce  sync.Once
	startOnce sync.Once
	cancel    context.CancelFunc
	lifetime  context.Context
	timeout   time.Duration
}

func New(registry *Registry, capacity int, timeout time.Duration) (*Engine, error) {
	if registry == nil || capacity <= 0 || timeout <= 0 {
		return nil, errors.New("runtime: registry, capacity and timeout required")
	}

	if err := registry.freeze(); err != nil {
		return nil, err
	}

	lifetime, cancel := context.WithCancel(context.Background())
	return &Engine{
		registry: registry,
		queue:    make(chan request, capacity),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
		timeout:  timeout,
		lifetime: lifetime,
		cancel:   cancel,
	}, nil
}

// Start waits for ordered hydration before accepting calls. Call exactly once.
func (e *Engine) Start(ctx context.Context) error {
	started := false
	e.startOnce.Do(func() {
		started = true
	})
	if !started {
		return errors.New("runtime already started or stopped")
	}

	ready := make(chan error, 1)
	go func() {
		defer close(e.done)

		defer e.cancel()

		world := newWorld()

		for _, h := range e.registry.hooks {
			if err := h.Run(ctx, world); err != nil {
				ready <- fmt.Errorf("hydrate %s: %w", h.Name, err)
				return
			}
		}

		ready <- nil
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()

		for {
			var req request
			select {
			case <-timer.C:
				for _, hook := range e.registry.hooks {
					if hook.Cleanup != nil {
						hook.Cleanup(world)
					}
				}
				continue
			case queued, ok := <-e.queue:
				if !ok {
					return
				}
				req = queued
			}
			if err := req.ctx.Err(); err != nil {
				req.result <- result{err: err}
				continue
			}

			if err := e.lifetime.Err(); err != nil {
				req.result <- result{err: ErrStopped}
				continue
			}

			op, cancel := context.WithTimeout(context.WithoutCancel(req.ctx), e.timeout)
			stop := context.AfterFunc(e.lifetime, cancel)
			view, err := req.work(op, world)
			stop()
			cancel()
			req.result <- result{
				view: view,
				err:  err,
			}
			var fatal *ProjectionError
			if errors.As(err, &fatal) {
				return
			}
		}
	}()
	return <-ready
}

func (e *Engine) Submit(ctx context.Context, name string, scope core.Scope, work Work) (core.View, error) {
	if err := scope.Validate(); err != nil {
		return core.View{}, err
	}
	if !e.registry.names[name] || work == nil {
		return core.View{}, errors.New("unregistered operation")
	}

	req := request{
		ctx:    ctx,
		work:   work,
		result: make(chan result, 1),
	}
	e.admission.RLock()

	select {
	case <-e.stopping:
		e.admission.RUnlock()
		return core.View{}, ErrStopped
	default:
	}

	select {
	case e.queue <- req:
	case <-ctx.Done():
		e.admission.RUnlock()
		return core.View{}, ctx.Err()
	case <-e.stopping:
		e.admission.RUnlock()
		return core.View{}, ErrStopped
	case <-e.done:
		e.admission.RUnlock()
		return core.View{}, ErrStopped
	}

	e.admission.RUnlock()

	select {
	case got := <-req.result:
		return got.view, got.err
	case <-ctx.Done():
		return core.View{}, ctx.Err()
	case <-e.done:
		select {
		case got := <-req.result:
			return got.view, got.err
		default:
			return core.View{}, ErrStopped
		}
	}
}

func (e *Engine) Stop(ctx context.Context) error {
	e.startOnce.Do(func() {
		e.cancel()
		close(e.done)
	})
	e.stopOnce.Do(func() {
		close(e.stopping)
		e.admission.Lock()
		close(e.queue)
		e.admission.Unlock()
	})

	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		e.cancel()
		<-e.done
		return ctx.Err()
	}
}

// Bind registers a typed feature closure. Its world argument is available only on the owner.
func Bind[I any](
	registry *Registry,
	name string,
	handler func(context.Context, donburi.World, core.Scope, I) (core.View, error),
) (func(context.Context, *Engine, core.Scope, I) (core.View, error), error) {
	if handler == nil {
		return nil, errors.New("nil handler")
	}

	if err := registry.Route(name); err != nil {
		return nil, err
	}
	return func(ctx context.Context, e *Engine, scope core.Scope, input I) (core.View, error) {
		return e.Submit(ctx, name, scope, func(op context.Context, world donburi.World) (core.View, error) {
			return handler(op, world, scope, input)
		})
	}, nil
}
