package cueassets

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/content"
	"github.com/fsnotify/fsnotify"
)

type eventSource interface {
	Add(string) error
	Close() error
	EventsChannel() <-chan fsnotify.Event
	ErrorsChannel() <-chan error
}

type nativeSource struct {
	*fsnotify.Watcher
}

func (s nativeSource) EventsChannel() <-chan fsnotify.Event {
	return s.Events
}

func (s nativeSource) ErrorsChannel() <-chan error {
	return s.Errors
}

func nativeWatcher() (eventSource, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return nativeSource{w}, nil
}

type Watcher struct {
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

func (w *Watcher) Done() <-chan struct{} {
	return w.done
}

func (w *Watcher) Err() error {
	<-w.done
	return w.err
}

func (w *Watcher) Stop() {
	w.cancel()
	<-w.done
}

func Watch(
	ctx context.Context,
	root string,
	debounce time.Duration,
	publish func(context.Context, *content.Bundle) error,
	report func(error),
) (*Watcher, error) {
	return watch(ctx, root, debounce, Load, publish, report, nativeWatcher)
}

func watch(
	ctx context.Context,
	root string,
	debounce time.Duration,
	load func(string) (*content.Bundle, error),
	publish func(context.Context, *content.Bundle) error,
	report func(error),
	factory func() (eventSource, error),
) (*Watcher, error) {
	if debounce <= 0 || load == nil || publish == nil || report == nil || factory == nil {
		return nil, errors.New("watcher: dependencies and positive debounce required")
	}

	open := func() (eventSource, error) {
		source, err := factory()
		if err != nil {
			return nil, err
		}

		for _, dir := range []string{"items", "locales"} {
			if err = source.Add(filepath.Join(root, dir)); err != nil {
				_ = source.Close()
				return nil, err
			}
		}
		return source, nil
	}
	source, err := open()
	if err != nil {
		return nil, err
	}

	run, cancel := context.WithCancel(ctx)
	w := &Watcher{
		done:   make(chan struct{}),
		cancel: cancel,
	}
	go func() {
		defer close(w.done)

		defer func() {
			_ = source.Close()
		}()

		timer := time.NewTimer(debounce)
		defer timer.Stop() // Initial rescan closes the load/watch startup gap.
		pending := true
		failures := 0
		reload := func() {
			candidate, err := load(root)
			if err == nil {
				err = publish(run, candidate)
			}

			if err != nil && run.Err() == nil {
				report(err)
			}
		}

		for {
			select {
			case <-run.Done():
				return
			case event, ok := <-source.EventsChannel():
				if !ok {
					w.err = errors.New("asset event channel closed")
					return
				}

				if strings.HasSuffix(event.Name, ".cue") || event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					timer.Reset(debounce)
					pending = true
				}
			case err, ok := <-source.ErrorsChannel():
				if !ok {
					w.err = errors.New("asset error channel closed")
					return
				}

				report(err)
				_ = source.Close()
				recovered := false

				for failures < 3 {
					failures++
					replacement, err := open()
					if err != nil {
						report(err)
						continue
					}

					source = replacement
					recovered = true
					break
				}

				if !recovered {
					w.err = errors.New("asset watcher recovery exhausted")
					return
				}

				reload()
				timer.Reset(debounce)
				pending = true
			case <-timer.C:
				if pending {
					reload()
					pending = false
					failures = 0
				}
			}
		}
	}()
	return w, nil
}
