package inventory

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
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
	starter    []contract.GrantLine
	characters characterPort
	clock      func() time.Time
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

type catalogSnapshot struct {
	items []itemcontract.Definition
	byID  map[itemcontract.ItemID]itemcontract.Definition
}

func (c catalogSnapshot) Lookup(id itemcontract.ItemID) (itemcontract.Definition, bool) {
	d, ok := c.byID[id]
	return d, ok
}

func (c catalogSnapshot) Items() []itemcontract.Definition { return slices.Clone(c.items) }

func (c catalogSnapshot) Effect(id itemcontract.ItemID) (itemcontract.EffectDefinition, error) {
	if err := id.Validate(); err != nil {
		return itemcontract.EffectDefinition{}, err
	}

	d, ok := c.Lookup(id)
	if !ok {
		return itemcontract.EffectDefinition{}, &itemcontract.DomainError{Kind: itemcontract.ErrorInvalidInput, Field: "item_id"}
	}

	return d.EffectDefinition()
}

// NewService validates and copies all catalog definitions and starter lines before admission.
func NewService(catalog Catalog, starter []contract.GrantLine, characters characterPort, clock func() time.Time) (*Service, error) {
	if nilPort(catalog) || nilPort(characters) || clock == nil {
		return nil, contract.ErrInvalidInput
	}

	snapshot := catalogSnapshot{byID: make(map[itemcontract.ItemID]itemcontract.Definition)}
	for _, d := range catalog.Items() {
		if d.Validate() != nil {
			return nil, invalidCatalog("mechanics")
		}

		if _, ok := snapshot.byID[d.ID]; ok {
			return nil, invalidCatalog("item_id")
		}

		lookup, ok := catalog.Lookup(d.ID)
		if !ok || lookup != d {
			return nil, invalidCatalog("catalog")
		}

		expected, effectErr := d.EffectDefinition()
		actual, err := catalog.Effect(d.ID)
		if effectErr == nil {
			if err != nil || actual != expected {
				return nil, invalidCatalog("mechanics")
			}
		} else if !errors.Is(err, itemcontract.ErrNotUsable) {
			return nil, invalidCatalog("mechanics")
		}

		snapshot.byID[d.ID] = d
		snapshot.items = append(snapshot.items, d)
	}

	if len(snapshot.items) == 0 {
		return nil, invalidCatalog("items")
	}

	copied, err := validateStarter(snapshot, starter)
	if err != nil {
		return nil, err
	}

	return &Service{catalog: snapshot, starter: copied, characters: characters, clock: clock}, nil
}

func inventoryItemError(err error) error {
	if err == nil {
		return nil
	}

	var domain *itemcontract.DomainError

	if errors.As(err, &domain) {
		kind := contract.ErrorInvalidInput
		if domain.Kind == itemcontract.ErrorNotUsable {
			kind = contract.ErrorNotUsable
		}
		if domain.Kind == itemcontract.ErrorInvalidCatalog {
			kind = contract.ErrorInvalidCatalog
		}
		return &contract.DomainError{Kind: kind, Field: domain.Field}
	}

	return contract.ErrInvalidInput
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
			grant, err := s.grant(ctx, tx, contract.GrantRequest{OperationID: starterOperationID(ch.ID), CharacterID: ch.ID, Items: s.starter}, "starter")
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
