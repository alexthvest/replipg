package health

import (
	"sync"
	"testing"
)

func TestLifecycleReadiness(t *testing.T) {
	s := NewState()
	for _, dep := range []Dependency{Storage, Telegram, Intake, Outbox, Scheduler} {
		_ = s.SetDependency(dep, true)
	}

	for _, tc := range []struct {
		state       Lifecycle
		live, ready bool
		reason      SafeReason
	}{
		{Starting, true, false, ReasonStarting}, {Recovering, true, false, ReasonRecovering},
		{Ready, true, true, ReasonNone}, {Draining, true, false, ReasonDraining}, {Stopped, false, false, ReasonStopped},
	} {
		if err := s.SetLifecycle(tc.state); err != nil {
			t.Fatal(err)
		}

		got := s.Snapshot()

		if got.Lifecycle != tc.state || got.Live != tc.live || got.Ready != tc.ready || got.Reason != tc.reason {
			t.Fatalf("%s: %+v", tc.state, got)
		}

		if got.DeploymentMode != "single_instance" || got.MultiInstanceSupported {
			t.Fatalf("deployment: %+v", got)
		}
	}
}

func TestDependencyHealthIsSafeAndRequired(t *testing.T) {
	s := NewState()
	if err := s.SetLifecycle(Ready); err != nil {
		t.Fatal(err)
	}

	if s.Snapshot().Ready {
		t.Fatal("unverified dependencies allowed readiness")
	}

	for _, dep := range []Dependency{Storage, Telegram, Intake, Outbox, Scheduler} {
		_ = s.SetDependency(dep, true)
	}

	for _, dep := range []Dependency{Storage, Telegram, Intake, Outbox, Scheduler} {
		if err := s.SetDependency(dep, false); err != nil {
			t.Fatal(err)
		}

		got := s.Snapshot()

		if got.Ready || !got.Live || got.Reason != ReasonDependencyUnavailable {
			t.Fatalf("unhealthy %s: %+v", dep, got)
		}

		if err := s.SetDependency(dep, true); err != nil {
			t.Fatal(err)
		}

		if !s.Snapshot().Ready {
			t.Fatal("recovery did not restore readiness")
		}
	}

	before := s.Snapshot()
	if err := s.SetDependency(Dependency("secret"), false); err != ErrInvalidDependency {
		t.Fatalf("invalid dependency: %v", err)
	}

	if err := s.SetLifecycle(Lifecycle("secret")); err != ErrInvalidLifecycle {
		t.Fatalf("invalid lifecycle: %v", err)
	}

	if s.Snapshot() != before {
		t.Fatal("invalid input changed state")
	}
}

func TestStateConcurrentSnapshots(t *testing.T) {
	s := NewState()
	for _, dep := range []Dependency{Storage, Telegram, Intake, Outbox, Scheduler} {
		_ = s.SetDependency(dep, true)
	}

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 100 {
				_ = s.SetLifecycle(Ready)
				_ = s.SetDependency(Storage, false)
				got := s.Snapshot()

				if got.Ready && (got.Lifecycle != Ready || got.Reason != ReasonNone) {
					t.Errorf("inconsistent snapshot: %+v", got)
				}

				_ = s.SetDependency(Storage, true)
			}
		})
	}

	wg.Wait()
}
