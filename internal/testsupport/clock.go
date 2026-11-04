// Package testsupport provides deterministic fakes for runtime tests.
package testsupport

import (
	"context"
	"sync"
	"time"
)

// ManualClock is a clock whose time advances only when Advance is called.
// Its methods are safe for concurrent use.
type ManualClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*ManualTimer]time.Time
	changed chan struct{}
}

// ManualTimer is a timer owned by a ManualClock.
type ManualTimer struct {
	clock  *ManualClock
	ch     chan time.Time
	active bool
}

// NewManualClock returns a clock set to start.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{
		now:     start,
		timers:  make(map[*ManualTimer]time.Time),
		changed: make(chan struct{}),
	}
}

// Now returns the clock's current time.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// NewTimer returns a timer that fires after d has elapsed on this clock.
func (c *ManualClock) NewTimer(d time.Duration) *ManualTimer {
	c.mu.Lock()
	defer c.mu.Unlock()

	timer := &ManualTimer{
		clock:  c,
		ch:     make(chan time.Time, 1),
		active: true,
	}
	if d <= 0 {
		timer.active = false
		timer.ch <- c.now
		return timer
	}

	c.timers[timer] = c.now.Add(d)
	c.signalChangeLocked()
	return timer
}

// After returns a channel that fires after d has elapsed on this clock.
func (c *ManualClock) After(d time.Duration) <-chan time.Time {
	return c.NewTimer(d).C()
}

// Sleep blocks until d has elapsed on this clock or ctx is canceled.
func (c *ManualClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	timer := c.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Advance moves the clock forward and fires every timer whose deadline is due.
// Advance panics for a negative duration because a manual clock is monotonic.
func (c *ManualClock) Advance(d time.Duration) {
	if d < 0 {
		panic("testsupport: cannot advance clock by a negative duration")
	}

	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	var due []*ManualTimer
	for timer, deadline := range c.timers {
		if deadline.After(now) {
			continue
		}
		delete(c.timers, timer)
		timer.active = false
		due = append(due, timer)
	}
	if len(due) > 0 {
		c.signalChangeLocked()
	}
	c.mu.Unlock()

	for _, timer := range due {
		timer.ch <- now
	}
}

// PendingTimers returns the number of active timers.
func (c *ManualClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.timers)
}

// WaitForTimers blocks until at least count timers are active or ctx is done.
// It is a synchronization aid for tests that create timers in goroutines.
func (c *ManualClock) WaitForTimers(ctx context.Context, count int) error {
	for {
		c.mu.Lock()
		if len(c.timers) >= count {
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *ManualClock) signalChangeLocked() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// C returns the channel on which the timer delivers its firing time.
func (t *ManualTimer) C() <-chan time.Time {
	return t.ch
}

// Stop prevents an active timer from firing. It reports whether the timer was
// active when stopped.
func (t *ManualTimer) Stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()

	if !t.active {
		return false
	}
	delete(c.timers, t)
	t.active = false
	c.signalChangeLocked()
	return true
}
