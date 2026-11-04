package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type loopAPI struct {
	poll func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error)
}

func (a loopAPI) DeleteWebhook(context.Context, bool) error { return nil }
func (a loopAPI) GetUpdates(ctx context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
	return a.poll(ctx, r)
}

func newLoopPoller(t *testing.T, poll func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error), cursor func(context.Context) (runtimecontract.UpdateID, error), options PollerOptions) *Poller {
	t.Helper()
	p, err := NewPoller(loopAPI{poll}, initializationCursor{load: cursor}, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPollLoopWaitsForDurableCompletionAndOwnsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var cursor atomic.Int64
	cursor.Store(40)
	var polls atomic.Int64
	p := newLoopPoller(t, func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
		n := polls.Add(1)
		if r.Limit != 1 || r.Offset != 40+n || r.Timeout != 20 || !reflect.DeepEqual(r.AllowedUpdates, []string{"message", "callback_query"}) {
			t.Errorf("unexpected request: %+v", r)
		}
		if n == 1 {
			return []json.RawMessage{json.RawMessage(`{"update_id":41}`)}, nil
		}
		cancel()
		return nil, context.Canceled
	}, func(context.Context) (runtimecontract.UpdateID, error) {
		return runtimecontract.UpdateID(cursor.Load()), nil
	}, PollerOptions{TimeoutSeconds: 20, ProcessUpdate: func(_ context.Context, u DecodedUpdate) error {
		if u.Status != DecodeIgnored || u.UpdateID != 41 {
			t.Errorf("decoded=%+v", u)
		}
		close(entered)
		<-release
		cursor.Store(41)
		return nil
	}})
	select {
	case <-p.Running():
		t.Fatal("running before Run")
	default:
	}
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	<-entered
	select {
	case <-p.Running():
	default:
		t.Fatal("owned loop not signaled")
	}
	if err := p.Run(ctx); !errors.Is(err, ErrPollerState) {
		t.Errorf("duplicate Run: %v", err)
	}
	if polls.Load() != 1 {
		t.Fatal("poll acknowledged blocked processing")
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if polls.Load() != 2 {
		t.Fatalf("polls=%d", polls.Load())
	}
	if _, err := p.NextRequest(); !errors.Is(err, ErrPollerState) {
		t.Fatal("terminated session accepts work")
	}
}

func TestPollLoopRejectsUncommittedCompletionAndRedeliversAfterRestart(t *testing.T) {
	crash := errors.New("crash before commit")
	for _, processErr := range []error{nil, crash} {
		t.Run(map[bool]string{true: "crash", false: "false_success"}[processErr != nil], func(t *testing.T) {
			var offsets []int64
			options := PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { return processErr }}
			poll := func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
				offsets = append(offsets, r.Offset)
				return []json.RawMessage{json.RawMessage(`{"update_id":91}`)}, nil
			}
			cursor := func(context.Context) (runtimecontract.UpdateID, error) { return 90, nil }
			p := newLoopPoller(t, poll, cursor, options)
			err := p.Run(context.Background())
			if processErr != nil {
				if !errors.Is(err, crash) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrIncompleteUpdate) {
				t.Fatalf("uncommitted success: %v", err)
			}
			restarted := newLoopPoller(t, poll, cursor, options)
			_ = restarted.Run(context.Background())
			if !reflect.DeepEqual(offsets, []int64{91, 91}) {
				t.Fatalf("restart offsets=%v", offsets)
			}
		})
	}
}

func TestPollLoopRetriesSameOffsetWithBoundedCancellationAwareBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []time.Duration
	var offsets []int64
	p := newLoopPoller(t, func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
		offsets = append(offsets, r.Offset)
		return nil, &APIError{Kind: ErrorTransient}
	}, func(context.Context) (runtimecontract.UpdateID, error) { return 5, nil }, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { t.Fatal("unexpected dispatch"); return nil }, Sleep: func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 9 {
			cancel()
			return ctx.Err()
		}
		return nil
	}})
	if err := p.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(waits) != 9 {
		t.Fatalf("waits=%v", waits)
	}
	for i, d := range waits {
		if d <= 0 || d > 30*time.Second || (i > 0 && d < waits[i-1]) {
			t.Fatalf("unbounded backoff=%v", waits)
		}
	}
	for _, offset := range offsets {
		if offset != 6 {
			t.Fatalf("offsets=%v", offsets)
		}
	}
}

func TestPollLoopMissingIdentityBoundedAndConflictTerminal(t *testing.T) {
	for _, name := range []string{"missing_id", "conflict", "permanent", "oversized_batch"} {
		t.Run(name, func(t *testing.T) {
			calls, waits := 0, 0
			p := newLoopPoller(t, func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
				calls++
				switch name {
				case "conflict":
					return nil, &APIError{Kind: ErrorConflict}
				case "permanent":
					return nil, &APIError{Kind: ErrorAuthorization}
				case "oversized_batch":
					return []json.RawMessage{json.RawMessage(`{"update_id":2}`), json.RawMessage(`{"update_id":3}`)}, nil
				}
				return []json.RawMessage{json.RawMessage(`{"message":{}}`)}, nil
			}, func(context.Context) (runtimecontract.UpdateID, error) { return 1, nil }, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { t.Fatal("unsafe dispatch"); return nil }, Sleep: func(context.Context, time.Duration) error { waits++; return nil }})
			err := p.Run(context.Background())
			if err == nil {
				t.Fatal("unsafe polling succeeded")
			}
			if name == "missing_id" {
				if !errors.Is(err, ErrUpdateID) || calls != 3 || waits != 2 {
					t.Fatalf("err=%v calls=%d waits=%d", err, calls, waits)
				}
			} else if calls != 1 || waits != 0 {
				t.Fatalf("calls=%d waits=%d", calls, waits)
			}
		})
	}
}

func TestPollLoopStopDuringPollPreventsDispatch(t *testing.T) {
	var p *Poller
	p = newLoopPoller(t, func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
		p.StopAccepting()
		return []json.RawMessage{json.RawMessage(`{"update_id":2}`)}, nil
	}, func(context.Context) (runtimecontract.UpdateID, error) { return 1, nil }, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { t.Fatal("dispatch after shutdown"); return nil }})
	if err := p.Run(context.Background()); !errors.Is(err, ErrPollerState) {
		t.Fatal(err)
	}
}

func TestPollLoopRunsKnownMalformedAndUnsupportedThroughTerminalProcessor(t *testing.T) {
	for _, raw := range []string{`{"update_id":2,"message":null}`, `{"update_id":2,"poll":{}}`} {
		t.Run(raw, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cursor := runtimecontract.UpdateID(1)
			calls := 0
			p := newLoopPoller(t, func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
				calls++
				if calls == 1 {
					return []json.RawMessage{json.RawMessage(raw)}, nil
				}
				if r.Offset != 3 {
					t.Errorf("offset=%d", r.Offset)
				}
				cancel()
				return nil, ctx.Err()
			}, func(context.Context) (runtimecontract.UpdateID, error) { return cursor, nil }, PollerOptions{ProcessUpdate: func(_ context.Context, u DecodedUpdate) error {
				if u.Status != DecodeIgnored && u.Status != DecodeFailed {
					t.Fatal(u.Status)
				}
				cursor = u.UpdateID
				return nil
			}})
			if err := p.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestPollLoopInvalidOwnershipNeverSignals(t *testing.T) {
	p := newLoopPoller(t, func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
		t.Fatal("unexpected poll")
		return nil, nil
	}, func(context.Context) (runtimecontract.UpdateID, error) { return 0, nil }, PollerOptions{})
	if err := p.Run(context.Background()); !errors.Is(err, ErrInvalidPoller) {
		t.Fatal(err)
	}
	select {
	case <-p.Running():
		t.Fatal("invalid loop running")
	default:
	}
}

func TestPollLoopDurableFailureContinuesAndDuplicateSkipsProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cursor := runtimecontract.UpdateID(7)
	calls, processed := 0, 0
	p := newLoopPoller(t, func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
		calls++
		switch calls {
		case 1:
			return []json.RawMessage{json.RawMessage(`{"update_id":7}`)}, nil
		case 2:
			if r.Offset != 8 {
				t.Error(r.Offset)
			}
			return []json.RawMessage{json.RawMessage(`{"update_id":8}`)}, nil
		default:
			if r.Offset != 9 {
				t.Error(r.Offset)
			}
			cancel()
			return nil, ctx.Err()
		}
	}, func(context.Context) (runtimecontract.UpdateID, error) { return cursor, nil }, PollerOptions{ProcessUpdate: func(_ context.Context, u DecodedUpdate) error {
		processed++
		cursor = u.UpdateID
		return errors.New("durably isolated failure")
	}})
	if err := p.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if processed != 1 || calls != 3 {
		t.Fatalf("processed=%d polls=%d", processed, calls)
	}
}

func TestPollLoopCursorFailureAndRegressionPreventConfirmation(t *testing.T) {
	failure := errors.New("cursor unavailable")
	for _, name := range []string{"read_failure", "regression", "overflow"} {
		t.Run(name, func(t *testing.T) {
			reads, polls := 0, 0
			p := newLoopPoller(t, func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
				polls++
				return []json.RawMessage{json.RawMessage(`{"update_id":8}`)}, nil
			}, func(context.Context) (runtimecontract.UpdateID, error) {
				reads++
				if reads == 1 {
					return 7, nil
				}
				switch name {
				case "read_failure":
					return 0, failure
				case "regression":
					return 6, nil
				default:
					return runtimecontract.UpdateID(1<<63 - 1), nil
				}
			}, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { return nil }})
			err := p.Run(context.Background())
			want := ErrInvalidCursor
			if name == "read_failure" {
				want = failure
			}
			if !errors.Is(err, want) || polls != 1 {
				t.Fatalf("err=%v polls=%d", err, polls)
			}
		})
	}
}

func TestPollLoopStopInterruptsRetryAndDoesNotCancelAdmittedWork(t *testing.T) {
	for _, name := range []string{"retry", "processing"} {
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			cursor := runtimecontract.UpdateID(1)
			p := newLoopPoller(t, func(context.Context, GetUpdatesRequest) ([]json.RawMessage, error) {
				if name == "retry" {
					return nil, &APIError{Kind: ErrorTransient}
				}
				return []json.RawMessage{json.RawMessage(`{"update_id":2}`)}, nil
			}, func(context.Context) (runtimecontract.UpdateID, error) { return cursor, nil }, PollerOptions{ProcessUpdate: func(ctx context.Context, u DecodedUpdate) error {
				close(entered)
				<-release
				if ctx.Err() != nil {
					t.Error("shutdown canceled admitted work")
				}
				cursor = u.UpdateID
				return nil
			}, Sleep: func(ctx context.Context, _ time.Duration) error { close(entered); <-ctx.Done(); return ctx.Err() }})
			done := make(chan error, 1)
			go func() { done <- p.Run(context.Background()) }()
			<-entered
			p.StopAccepting()
			close(release)
			if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, ErrPollerState) {
				t.Fatal(err)
			}
		})
	}
}

func TestPollLoopCancellationInterruptsNetworkAndPreventsRunning(t *testing.T) {
	for _, name := range []string{"before_run", "during_poll"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			p := newLoopPoller(t, func(ctx context.Context, _ GetUpdatesRequest) ([]json.RawMessage, error) {
				close(entered)
				<-ctx.Done()
				return nil, ctx.Err()
			}, func(context.Context) (runtimecontract.UpdateID, error) { return 0, nil }, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { return nil }})
			if name == "before_run" {
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- p.Run(ctx) }()
			if name == "during_poll" {
				<-entered
				cancel()
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name == "before_run" {
				select {
				case <-p.Running():
					t.Fatal("canceled loop signaled running")
				default:
				}
			}
		})
	}
}

func TestPollLoopRequestDeadlineRetriesAndHonorsRateLimitDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var delays []time.Duration
	p := newLoopPoller(t, func(_ context.Context, r GetUpdatesRequest) ([]json.RawMessage, error) {
		calls++
		if r.Offset != 6 {
			t.Error(r.Offset)
		}
		if calls == 1 {
			return nil, &APIError{Kind: ErrorCancellation, cause: context.DeadlineExceeded}
		}
		if calls == 2 {
			return nil, &APIError{Kind: ErrorRateLimit, RetryAfter: 45 * time.Second}
		}
		cancel()
		return nil, ctx.Err()
	}, func(context.Context) (runtimecontract.UpdateID, error) { return 5, nil }, PollerOptions{ProcessUpdate: func(context.Context, DecodedUpdate) error { return nil }, Sleep: func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }})
	if err := p.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 3 || !reflect.DeepEqual(delays, []time.Duration{250 * time.Millisecond, 45 * time.Second}) {
		t.Fatalf("calls=%d waits=%v", calls, delays)
	}
}
