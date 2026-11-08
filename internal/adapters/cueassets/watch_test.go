package cueassets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/content"
	"github.com/fsnotify/fsnotify"
)

type fakeSource struct {
	events chan fsnotify.Event
	errors chan error
	mu     sync.Mutex
	closed bool
}

func newSource() *fakeSource {
	return &fakeSource{
		events: make(chan fsnotify.Event, 8),
		errors: make(chan error, 1),
	}
}

func (s *fakeSource) Add(string) error {
	return nil
}

func (s *fakeSource) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *fakeSource) EventsChannel() <-chan fsnotify.Event {
	return s.events
}

func (s *fakeSource) ErrorsChannel() <-chan error {
	return s.errors
}

func receive[T any](t *testing.T, c <-chan T) T {
	t.Helper()

	select {
	case v := <-c:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("event did not arrive")
		var zero T
		return zero
	}
}

func TestWatchEventsInvalidCandidatesAndRecovery(t *testing.T) {
	sources := []*fakeSource{
		newSource(),
		newSource(),
	}
	factoryCalls := 0
	loaded := make(chan struct{}, 16)
	published := make(chan struct{}, 16)
	reports := make(chan error, 16)
	var mu sync.Mutex
	invalid := false
	load := func(string) (*content.Bundle, error) {
		mu.Lock()
		bad := invalid
		mu.Unlock()
		loaded <- struct{}{}
		if bad {
			return nil, errors.New("invalid assets")
		}
		return nil, nil
	}
	watcher, err := watch(context.Background(), t.TempDir(), time.Millisecond, load, func(
		context.Context,
		*content.Bundle,
	) error {
		published <- struct{}{}
		return nil
	}, func(err error) {
		reports <- err
	}, func() (eventSource, error) {
		s := sources[factoryCalls]
		factoryCalls++
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	defer watcher.Stop()

	receive(t, published)
	receive(t, loaded)

	for _, op := range []fsnotify.Op{fsnotify.Create, fsnotify.Write, fsnotify.Remove, fsnotify.Rename} {
		sources[0].events <- fsnotify.Event{
			Name: "items/test.cue",
			Op:   op,
		}
		receive(t, published)
		receive(t, loaded)
	}

	mu.Lock()
	invalid = true
	mu.Unlock()
	sources[0].events <- fsnotify.Event{
		Name: "locales/en.cue",
		Op:   fsnotify.Write,
	}
	_ = receive(t, reports)
	receive(t, loaded)

	select {
	case <-published:
		t.Fatal("invalid candidate published")
	default:
	}

	mu.Lock()
	invalid = false
	mu.Unlock()
	sources[0].errors <- errors.New("injected watcher failure")
	_ = receive(t, reports)
	receive(t, published)
	receive(t, loaded)
	watcher.Stop()
	if watcher.Err() != nil {
		t.Fatal(watcher.Err())
	}

	if factoryCalls != 2 {
		t.Fatal("watcher not re-registered")
	}

	for _, source := range sources {
		source.mu.Lock()
		closed := source.closed
		source.mu.Unlock()
		if !closed {
			t.Fatal("source left open")
		}
	}
}

func TestWatchAtomicReplacement(t *testing.T) {
	root := copyAssets(t)
	published := make(chan *content.Bundle, 16)
	reports := make(chan error, 16)
	watcher, err := Watch(context.Background(), root, time.Millisecond, func(_ context.Context, b *content.Bundle) error {
		published <- b
		return nil
	}, func(err error) {
		reports <- err
	})
	if err != nil {
		t.Fatal(err)
	}

	defer watcher.Stop()

	receive(t, published)
	target := filepath.Join(root, "locales", "en.cue")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	temp := filepath.Join(root, "locales", "replacement.tmp")
	if err = os.WriteFile(temp, data, 0600); err != nil {
		t.Fatal(err)
	}

	if err = os.Rename(temp, target); err != nil {
		t.Fatal(err)
	}

	select {
	case <-published:
	case err := <-reports:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("atomic save not observed")
	}
}

func TestCaptureRetriesInterruptedReads(t *testing.T) {
	calls := 0
	read := func(string) (map[string][]byte, error) {
		calls++
		if calls == 1 {
			return nil, os.ErrNotExist
		}
		return map[string][]byte{"a": []byte("same")}, nil
	}
	if _, err := stableCapture("unused", read); err != nil || calls != 3 {
		t.Fatalf("retry: %d %v", calls, err)
	}

	if _, err := stableCapture("unused", func(string) (map[string][]byte, error) {
		return nil, os.ErrPermission
	}); err == nil {
		t.Fatal("persistent capture error ignored")
	}
}

func TestWatchPersistentFailureStops(t *testing.T) {
	source := newSource()
	calls := 0
	reports := make(chan error, 8)
	watcher, err := watch(context.Background(), t.TempDir(), time.Hour, func(string) (*content.Bundle, error) {
		return nil, nil
	}, func(context.Context, *content.Bundle) error {
		return nil
	}, func(err error) {
		reports <- err
	}, func() (eventSource, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("watch unavailable")
		}
		return source, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	source.errors <- errors.New("watch lost")
	select {
	case <-watcher.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("persistent watcher failure hidden")
	}
	if watcher.Err() == nil || calls != 4 {
		t.Fatalf("recovery result: %d %v", calls, watcher.Err())
	}

	watcher.Stop()
}
