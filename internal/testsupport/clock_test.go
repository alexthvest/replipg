package testsupport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManualClockAdvancesTimersDeterministically(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
	clock := NewManualClock(start)
	timer := clock.NewTimer(5 * time.Second)

	clock.Advance(4 * time.Second)
	select {
	case <-timer.C():
		t.Fatal("timer fired before its deadline")
	default:
	}

	clock.Advance(time.Second)
	select {
	case got := <-timer.C():
		if want := start.Add(5 * time.Second); !got.Equal(want) {
			t.Fatalf("timer fired at %v, want %v", got, want)
		}
	default:
		t.Fatal("timer did not fire at its deadline")
	}
}

func TestManualClockSleepStopsPromptlyOnCancellation(t *testing.T) {
	t.Parallel()

	clock := NewManualClock(time.Unix(0, 0))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- clock.Sleep(ctx, time.Hour)
	}()

	waitCtx, stopWaiting := context.WithTimeout(context.Background(), time.Second)
	defer stopWaiting()
	if err := clock.WaitForTimers(waitCtx, 1); err != nil {
		t.Fatalf("wait for timer registration: %v", err)
	}

	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep returned %v, want context.Canceled", err)
	}
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("pending timers = %d, want 0", got)
	}
}

func TestManualClockStopPreventsDelivery(t *testing.T) {
	t.Parallel()

	clock := NewManualClock(time.Unix(0, 0))
	timer := clock.NewTimer(time.Minute)
	if !timer.Stop() {
		t.Fatal("first Stop returned false")
	}
	if timer.Stop() {
		t.Fatal("second Stop returned true")
	}

	clock.Advance(time.Minute)
	select {
	case <-timer.C():
		t.Fatal("stopped timer fired")
	default:
	}
}
