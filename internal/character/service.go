package character

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Service applies character operations using the caller's transaction. Results
// are tentative until its owner commits; callers must never supply a bare DB.
type Service struct {
	config Config
	clock  func() time.Time
}

// NewService validates immutable tuning and the character clock without I/O.
func NewService(cfg Config, clock func() time.Time) (*Service, error) {
	if cfg.maxHealth <= 0 || cfg.attack < 0 || cfg.defense < 0 || cfg.incapacitationDuration <= 0 || !decimalPercent(cfg.recoveryPercent) {
		return nil, &contract.DomainError{Kind: contract.ErrorInvalidConfig}
	}

	if clock == nil {
		return nil, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "clock"}
	}

	return &Service{config: cfg, clock: clock}, nil
}

// Ensure creates a missing character or returns its existing stored attributes.
// The bool reports insertion by this operation, not transaction commitment.
func (s *Service) Ensure(ctx context.Context, tx runtimecontract.DBTX, userID runtimecontract.UserID) (contract.Snapshot, bool, error) {
	if err := validateServiceInput(tx, userID); err != nil {
		return contract.Snapshot{}, false, err
	}

	now := s.clock().UTC()
	existing, found, err := readCharacterByUser(ctx, tx, userID)
	if err != nil {
		return contract.Snapshot{}, false, err
	}

	if found {
		existing, err = normalizeRecord(ctx, tx, existing, now)
		if err != nil {
			return contract.Snapshot{}, false, err
		}

		return existing.snapshot, false, nil
	}

	var randomID [16]byte

	if _, err := rand.Read(randomID[:]); err != nil {
		return contract.Snapshot{}, false, contract.NewStorageError(err)
	}

	id := contract.ID(hex.EncodeToString(randomID[:]))
	inserted, err := insertCharacter(ctx, tx, id, userID, s.config)
	if err != nil {
		return contract.Snapshot{}, false, err
	}

	stored, found, err := readCharacterByUser(ctx, tx, userID)
	if err != nil {
		return contract.Snapshot{}, false, err
	}

	if !found {
		return contract.Snapshot{}, false, contract.NewStorageError(errors.New("inserted character missing"))
	}

	return stored.snapshot, inserted, nil
}

// Lookup normalizes an existing character's due recovery without creating a
// missing one. Even this read must use the caller's transaction.
func (s *Service) Lookup(ctx context.Context, tx runtimecontract.DBTX, userID runtimecontract.UserID) (contract.Snapshot, bool, error) {
	if err := validateServiceInput(tx, userID); err != nil {
		return contract.Snapshot{}, false, err
	}

	return lookupCharacter(ctx, tx, userID, s.clock().UTC())
}

// Eligibility reports missing, active, or incapacitated after normalizing any
// expired recovery. Additional battle and encounter restrictions belong to callers.
func (s *Service) Eligibility(ctx context.Context, tx runtimecontract.DBTX, userID runtimecontract.UserID) (contract.Eligibility, error) {
	if err := validateServiceInput(tx, userID); err != nil {
		return contract.Eligibility{}, err
	}

	snapshot, found, err := lookupCharacter(ctx, tx, userID, s.clock().UTC())
	if err != nil {
		return contract.Eligibility{}, err
	}

	if !found {
		return contract.Eligibility{Kind: contract.EligibilityMissing}, nil
	}

	kind := contract.EligibilityActive
	if snapshot.State == contract.StateIncapacitated {
		kind = contract.EligibilityIncapacitated
	}
	return contract.Eligibility{Kind: kind, Character: snapshot}, nil
}

// ApplyHealth applies an authorized whole-HP mutation in the caller's transaction.
// The transaction owner must propagate storage failures and roll back all related
// gameplay effects. Matching replay returns current state and the original applied
// revision; rejected input never consumes its operation identity.
func (s *Service) ApplyHealth(ctx context.Context, tx runtimecontract.DBTX, change contract.HealthChange) (contract.MutationResult, error) {
	if err := change.Validate(); err != nil {
		return contract.MutationResult{}, err
	}

	if tx == nil {
		return contract.MutationResult{}, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction"}
	}

	now := s.clock().UTC()
	appliedRevision, replay, err := checkOperation(ctx, tx, change)
	if err != nil {
		return contract.MutationResult{}, err
	}

	record, found, err := readCharacterByID(ctx, tx, change.CharacterID)
	if err != nil {
		return contract.MutationResult{}, err
	}

	if !found {
		return contract.MutationResult{}, &contract.DomainError{Kind: contract.ErrorNotFound}
	}

	if replay {
		record, err = normalizeRecord(ctx, tx, record, now)
		if err != nil {
			return contract.MutationResult{}, err
		}

		return contract.MutationResult{Applied: false, AppliedRevision: appliedRevision, Current: record.snapshot}, nil
	}

	// Calculate both transitions before writing: a domain rejection must not
	// partially normalize state even when a caller handles it and commits.
	normalized, recoveryHealth, recovered, err := normalizeRecovery(record.snapshot, record.recoveryHealth, now)
	if err != nil {
		return contract.MutationResult{}, err
	}

	snapshot, recoveryHealth, changed, err := applyHealthRule(normalized, recoveryHealth, change.Kind, change.Amount, now, s.config)
	if err != nil {
		return contract.MutationResult{}, err
	}

	if recovered || changed {
		if err := updateCharacter(ctx, tx, snapshot, recoveryHealth, record.snapshot.Revision); err != nil {
			return contract.MutationResult{}, err
		}
	}
	if err := recordOperation(ctx, tx, change, snapshot.Revision); err != nil {
		return contract.MutationResult{}, err
	}

	return contract.MutationResult{Applied: true, AppliedRevision: snapshot.Revision, Current: snapshot}, nil
}

func lookupCharacter(ctx context.Context, tx runtimecontract.DBTX, userID runtimecontract.UserID, now time.Time) (contract.Snapshot, bool, error) {
	record, found, err := readCharacterByUser(ctx, tx, userID)
	if err != nil || !found {
		return contract.Snapshot{}, false, err
	}

	record, err = normalizeRecord(ctx, tx, record, now)
	if err != nil {
		return contract.Snapshot{}, false, err
	}

	return record.snapshot, true, nil
}

func normalizeRecord(ctx context.Context, tx runtimecontract.DBTX, record characterRecord, now time.Time) (characterRecord, error) {
	snapshot, recoveryHealth, changed, err := normalizeRecovery(record.snapshot, record.recoveryHealth, now)
	if err != nil {
		return characterRecord{}, err
	}

	if changed {
		if err := updateCharacter(ctx, tx, snapshot, recoveryHealth, record.snapshot.Revision); err != nil {
			return characterRecord{}, err
		}
	}
	return characterRecord{snapshot: snapshot, recoveryHealth: recoveryHealth}, nil
}

func validateServiceInput(tx runtimecontract.DBTX, userID runtimecontract.UserID) error {
	if tx == nil {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction"}
	}

	if userID <= 0 {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "user_id"}
	}

	return nil
}
