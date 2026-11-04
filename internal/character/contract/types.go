// Package contract defines character values shared with gameplay consumers.
// Consumers declare interfaces for the service methods they need. All service
// operations use the caller's transaction, including reads that normalize recovery;
// returned results remain tentative until that transaction commits.
package contract

import (
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// ID is an opaque internal identity, distinct from the owning Telegram user ID.
type ID string

// Validate rejects an absent character identity without exposing its contents.
func (id ID) Validate() error {
	if id == "" {
		return &DomainError{Kind: ErrorInvalidInput, Field: "character_id"}
	}
	return nil
}

// OperationID globally identifies one mutation. Callers namespace identities by
// action and target; accepted identities must never be reused for different input.
type OperationID string

// Validate requires a nonempty identity of at most 128 bytes.
func (id OperationID) Validate() error {
	if len(id) == 0 || len(id) > 128 {
		return &DomainError{Kind: ErrorInvalidInput, Field: "operation_id"}
	}
	return nil
}

// HealthKind selects a trusted gameplay health operation.
type HealthKind string

const (
	HealthDamage HealthKind = "damage"
	HealthHeal   HealthKind = "heal"
)

// Valid reports whether the health operation is supported.
func (k HealthKind) Valid() bool { return k == HealthDamage || k == HealthHeal }

// State describes effective character activity after accounting for recovery.
type State string

const (
	StateActive        State = "active"
	StateIncapacitated State = "incapacitated"
)

// Valid reports whether the state is supported.
func (s State) Valid() bool { return s == StateActive || s == StateIncapacitated }

// Snapshot contains stored base attributes, excluding equipment modifiers.
// RecoverAt is a UTC deadline and is zero while active. Revision tracks state
// changes; observing a matching mutation replay may return a later revision.
type Snapshot struct {
	ID                                 ID
	UserID                             runtimecontract.UserID
	MaxHealth, Health, Attack, Defense int64
	State                              State
	RecoverAt                          time.Time
	Revision                           int64
}

// HealthChange requests whole HP from an authorized gameplay caller. The
// character service does not decide battle permissions or calculate damage.
type HealthChange struct {
	OperationID OperationID
	CharacterID ID
	Kind        HealthKind
	Amount      int64
}

// Validate checks mutation input without loading or creating a character.
func (c HealthChange) Validate() error {
	if err := c.OperationID.Validate(); err != nil {
		return err
	}
	if err := c.CharacterID.Validate(); err != nil {
		return err
	}
	if !c.Kind.Valid() {
		return &DomainError{Kind: ErrorInvalidInput, Field: "kind"}
	}
	if c.Amount < 0 {
		return &DomainError{Kind: ErrorInvalidInput, Field: "amount"}
	}
	return nil
}

// MutationResult describes a tentative mutation outcome. Applied is false for
// matching replay. AppliedRevision is the original operation's revision; Current
// is the latest normalized state, which may include subsequent mutations.
type MutationResult struct {
	Applied         bool
	AppliedRevision int64
	Current         Snapshot
}

// EligibilityKind distinguishes absence from active and incapacitated state.
type EligibilityKind string

const (
	EligibilityMissing       EligibilityKind = "missing"
	EligibilityActive        EligibilityKind = "active"
	EligibilityIncapacitated EligibilityKind = "incapacitated"
)

// Valid reports whether the eligibility value is supported.
func (k EligibilityKind) Valid() bool {
	return k == EligibilityMissing || k == EligibilityActive || k == EligibilityIncapacitated
}

// Eligibility reports character-level eligibility only; consumers enforce their
// own battle and encounter restrictions. Character is zero when Kind is missing.
type Eligibility struct {
	Kind      EligibilityKind
	Character Snapshot
}
