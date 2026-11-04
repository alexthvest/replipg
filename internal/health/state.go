// Package health owns privacy-safe runtime lifecycle and probe state.
package health

import (
	"errors"
	"sync"
)

var ErrInvalidDependency = errors.New("invalid dependency")
var ErrInvalidLifecycle = errors.New("invalid lifecycle")

// Lifecycle is one of the runtime's operational phases.
type Lifecycle string

// SafeReason identifies a closed, content-free readiness reason.
type SafeReason string

// Dependency identifies a runtime component required for safe admission.
type Dependency string

const (
	Starting                    Lifecycle  = "starting"
	Recovering                  Lifecycle  = "recovering"
	Ready                       Lifecycle  = "ready"
	Draining                    Lifecycle  = "draining"
	Stopped                     Lifecycle  = "stopped"
	ReasonNone                  SafeReason = ""
	ReasonStarting              SafeReason = "starting"
	ReasonRecovering            SafeReason = "recovering"
	ReasonDraining              SafeReason = "draining"
	ReasonStopped               SafeReason = "stopped"
	ReasonDependencyUnavailable SafeReason = "dependency_unavailable"
	Storage                     Dependency = "storage"
	Telegram                    Dependency = "telegram"
	Intake                      Dependency = "intake"
	Outbox                      Dependency = "outbox"
	Scheduler                   Dependency = "scheduler"
)

// Snapshot contains operational metadata without configuration or Telegram IDs.
type Snapshot struct {
	Lifecycle              Lifecycle  `json:"lifecycle"`
	Live                   bool       `json:"live"`
	Ready                  bool       `json:"ready"`
	Reason                 SafeReason `json:"reason,omitempty"`
	DeploymentMode         string     `json:"deployment_mode"`
	MultiInstanceSupported bool       `json:"multi_instance_supported"`
}

// State publishes coherent lifecycle and required dependency health snapshots.
// Its zero value starts with no dependency confirmed healthy.
type State struct {
	mu           sync.RWMutex
	lifecycle    Lifecycle
	dependencies [5]bool
}

// NewState begins startup with every required dependency unverified.
func NewState() *State { return &State{lifecycle: Starting} }

// SetLifecycle publishes a known phase, rejecting arbitrary diagnostic text.
func (s *State) SetLifecycle(state Lifecycle) error {
	switch state {
	case Starting, Recovering, Ready, Draining, Stopped:
	default:
		return ErrInvalidLifecycle
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycle = state
	return nil
}

// SetDependency records whether a required component permits safe admission.
func (s *State) SetDependency(dependency Dependency, healthy bool) error {
	var index int
	switch dependency {
	case Storage:
		index = 0
	case Telegram:
		index = 1
	case Intake:
		index = 2
	case Outbox:
		index = 3
	case Scheduler:
		index = 4
	default:
		return ErrInvalidDependency
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dependencies[index] = healthy
	return nil
}

// Snapshot reads lifecycle and dependency state under a single lock.
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.lifecycle
	if state == "" {
		state = Starting
	}
	result := Snapshot{Lifecycle: state, Live: state != Stopped, DeploymentMode: "single_instance"}
	switch state {
	case Starting:
		result.Reason = ReasonStarting
	case Recovering:
		result.Reason = ReasonRecovering
	case Draining:
		result.Reason = ReasonDraining
	case Stopped:
		result.Reason = ReasonStopped
	case Ready:
		for _, healthy := range s.dependencies {
			if !healthy {
				result.Reason = ReasonDependencyUnavailable
				return result
			}
		}
		result.Ready = true
	}
	return result
}
