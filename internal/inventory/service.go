package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var errCharacterIdentity = errors.New("character identity invariant failed")

var errStarterInvariant = errors.New("starter entitlement invariant failed")

type characterPort interface {
	Ensure(context.Context, runtimecontract.DBTX, runtimecontract.UserID) (charactercontract.Snapshot, bool, error)
	Lookup(context.Context, runtimecontract.DBTX, runtimecontract.UserID) (charactercontract.Snapshot, bool, error)
	ApplyHealth(context.Context, runtimecontract.DBTX, charactercontract.HealthChange) (charactercontract.MutationResult, error)
}

// Service coordinates inventory changes in the caller's transaction.
// Returned results remain tentative until that transaction commits.
type Service struct {
	catalog    Catalog
	characters characterPort
	clock      func() time.Time
}

// NewService validates and copies immutable content without accessing storage.
func NewService(catalog Catalog, characters characterPort, clock func() time.Time) (*Service, error) {
	if characters == nil || (reflect.ValueOf(characters).Kind() == reflect.Pointer && reflect.ValueOf(characters).IsNil()) || clock == nil {
		return nil, contract.ErrInvalidInput
	}
	content := catalogContent{}
	for _, item := range catalog.Items() {
		effect := contract.EffectKind("")
		if item.Category == contract.CategoryConsumable {
			effect = contract.EffectHealSelfFixedHP
		}
		content.Items = append(content.Items, struct {
			ItemDefinition
			Effect contract.EffectKind `json:"effect"`
		}{item, effect})
	}
	for _, line := range catalog.Starter() {
		content.Starter = append(content.Starter, struct {
			ItemID   contract.ItemID `json:"item_id"`
			Quantity int64           `json:"quantity"`
		}{line.ItemID, line.Quantity})
	}
	data, err := json.Marshal(content)
	if err != nil {
		return nil, invalidCatalog("content")
	}
	validated, err := ParseCatalog(data)
	if err != nil {
		return nil, err
	}
	return &Service{catalog: validated, characters: characters, clock: clock}, nil
}

// Prepare rejects incompatible saved definitions and registers new identities.
func (s *Service) Prepare(ctx context.Context, tx runtimecontract.DBTX) error {
	return withInventoryOperation(ctx, tx, func() error { return prepareCatalog(ctx, tx, s.catalog) })
}

// Open creates a missing character and grants its starter supplies exactly once.
func (s *Service) Open(ctx context.Context, tx runtimecontract.DBTX, user runtimecontract.UserID) (contract.OpenResult, error) {
	if tx == nil || user <= 0 {
		return contract.OpenResult{}, contract.ErrInvalidInput
	}

	var result contract.OpenResult

	err := withInventoryOperation(ctx, tx, func() error {
		ch, created, err := s.characters.Ensure(ctx, tx, user)
		if err != nil {
			return err
		}

		if ch.ID.Validate() != nil || ch.UserID != user {
			return contract.NewStorageError(errCharacterIdentity)
		}

		if err := ensureInventoryRoot(ctx, tx, ch.ID); err != nil {
			return err
		}

		current, starter, err := readInventory(ctx, tx, s.catalog, ch.ID)
		if err != nil {
			return err
		}

		result.CharacterCreated = created
		if !starter {
			grant, err := s.grant(ctx, tx, contract.GrantRequest{OperationID: starterOperationID(ch.ID), CharacterID: ch.ID, Items: s.catalog.Starter()}, "starter")
			if err != nil {
				return err
			}

			if !grant.Applied {
				return contract.NewStorageError(errStarterInvariant)
			}

			if err := markStarterGranted(ctx, tx, ch.ID); err != nil {
				return err
			}

			current = grant.Current
			result.StarterGranted = true
		}
		result.Current = current
		return nil
	})
	if err != nil {
		return contract.OpenResult{}, err
	}

	return result, nil
}

// Inspect reads ownership without creating a character, inventory or starter kit.
func (s *Service) Inspect(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID) (contract.Snapshot, error) {
	if tx == nil || id.Validate() != nil {
		return contract.Snapshot{}, contract.ErrInvalidInput
	}

	current, _, err := readInventory(ctx, tx, s.catalog, id)
	return current, err
}

// Modifiers reads equipment bonuses without changing character recovery state.
func (s *Service) Modifiers(ctx context.Context, tx runtimecontract.DBTX, id charactercontract.ID) (contract.Modifiers, error) {
	if tx == nil || id.Validate() != nil {
		return contract.Modifiers{}, contract.ErrInvalidInput
	}

	current, _, err := readInventoryMetadata(ctx, tx, s.catalog, id)
	return current.Modifiers, err
}
