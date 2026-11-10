package crafting

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

var ErrDisabled = errors.New("crafting discovery disabled")
var ErrConfirmation = errors.New("invalid crafting confirmation")

type Service struct {
	repo    UnitOfWork
	ids     func() (core.ID, error)
	now     func() time.Time
	enabled bool
}

func New(repo UnitOfWork, ids func() (core.ID, error), now func() time.Time, enabled bool) (*Service, error) {
	if repo == nil || ids == nil || now == nil {
		return nil, errors.New("crafting dependencies required")
	}
	return &Service{
		repo:    repo,
		ids:     ids,
		now:     now,
		enabled: enabled,
	}, nil
}

func (s *Service) Confirm(ctx context.Context, c Confirmation) (domain.Attempt, error) {
	var result domain.Attempt
	err := s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		saved, found, err := tx.Repository.Attempt(ctx, c.Token)
		if err != nil {
			return err
		}
		if found {
			if saved.Scope.Group != c.Scope.Group || saved.Scope.Actor != c.Scope.Actor || saved.Scope.BotID != c.Scope.BotID || saved.Character != c.Character || saved.MessageID != c.MessageID || !reflect.DeepEqual(saved.Allocations, c.Allocations) {
				return ErrConfirmation
			}
			result = saved
			return nil
		}

		if c.Scope.Validate() != nil || c.Token == "" || c.Character == "" || c.MessageID <= 0 || !s.now().Before(c.Expires) {
			return ErrConfirmation
		}

		identity, err := domain.Identify(c.Allocations)
		if err != nil {
			return err
		}
		result = domain.Attempt{
			Token:       c.Token,
			Scope:       c.Scope,
			Character:   c.Character,
			MessageID:   c.MessageID,
			Allocations: c.Allocations,
			State:       domain.Pending,
		}
		if err = tx.Repository.Validate(ctx, result); err != nil {
			return err
		}

		recipe, found, err := tx.Repository.Recipe(ctx, identity)
		if err != nil {
			return err
		}
		if (!found || recipe.Outcome.Kind == "") && !s.enabled {
			return ErrDisabled
		}
		if !found {
			id, err := s.ids()
			if err != nil {
				return err
			}
			recipe, err = tx.Repository.EnsureRecipe(ctx, Recipe{
				ID:       id,
				Identity: identity,
			})
			if err != nil {
				return err
			}
		}
		if recipe.Outcome.Kind == "" {
			recipe, err = tx.Repository.EnsureRecipe(ctx, recipe)
			if err != nil {
				return err
			}
		}

		result.ID, err = s.ids()
		if err != nil {
			return err
		}
		result.Recipe = recipe.ID
		if err = tx.Repository.SaveAttempt(ctx, result); err != nil {
			return err
		}
		if recipe.Outcome.Kind != "" {
			return s.fulfill(ctx, tx, &result, recipe.Outcome)
		}
		return nil
	})
	return result, err
}

func (s *Service) fulfill(ctx context.Context, tx Transaction, a *domain.Attempt, o domain.Outcome) error {
	if a.State != domain.Pending {
		return nil
	}
	if err := o.Validate(); err != nil {
		return err
	}

	if err := tx.Repository.Validate(ctx, *a); err != nil {
		if !errors.Is(err, domain.ErrUnavailable) && !errors.Is(err, domain.ErrIngredients) {
			return err
		}
		a.State = domain.Unavailable
		return tx.Repository.SaveAttempt(ctx, *a)
	}

	if err := tx.Repository.Consume(ctx, *a); err != nil {
		return err
	}

	if _, err := inventory.Grant(ctx, tx.Inventory, s.ids, a.Scope.Group, a.Character, *o.Item, 1); err != nil {
		return err
	}

	a.Outcome = o
	a.State = o.Kind
	return tx.Repository.SaveAttempt(ctx, *a)
}

func (s *Service) Lookup(ctx context.Context, allocations []domain.Allocation) (domain.Outcome, bool, error) {
	var outcome domain.Outcome
	identity, err := domain.Identify(allocations)
	if err != nil {
		return outcome, false, err
	}
	err = s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		r, _, err := tx.Repository.Recipe(ctx, identity)
		outcome = r.Outcome
		return err
	})
	return outcome, outcome.Kind != "", err
}

func (s *Service) Attempt(ctx context.Context, token core.ID) (domain.Attempt, bool, error) {
	var a domain.Attempt
	var found bool
	err := s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		var err error
		a, found, err = tx.Repository.Attempt(ctx, token)
		return err
	})
	return a, found, err
}

func (s *Service) Claim(ctx context.Context, lease time.Duration) (Claim, bool, error) {
	if !s.enabled {
		return Claim{}, false, nil
	}

	var claim Claim
	var found bool
	id, err := s.ids()
	if err != nil {
		return claim, false, err
	}
	err = s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		var err error
		claim, found, err = tx.Repository.Claim(ctx, s.now(), s.now().Add(lease), id)
		return err
	})
	return claim, found, err
}

func (s *Service) Complete(ctx context.Context, claim Claim, outcome domain.Outcome) error {
	if !s.now().Before(claim.Expires) {
		return nil
	}
	if err := outcome.Validate(); err != nil {
		return errors.Join(err, s.Fail(ctx, claim))
	}
	if outcome.Kind == domain.Success {
		id, err := s.ids()
		if err != nil {
			return err
		}
		copy := *outcome.Item
		copy.CatalogID = "generated:" + string(id)
		outcome.Item = &copy
	}
	if err := s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		_, err := tx.Repository.Publish(ctx, claim, outcome)
		return err
	}); err != nil {
		return err
	}
	return s.Recover(ctx)
}

func (s *Service) Fail(ctx context.Context, claim Claim) error {
	if !s.now().Before(claim.Expires) {
		return nil
	}
	return s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		_, err := tx.Repository.FailClaim(ctx, claim)
		return err
	})
}

func (s *Service) Recover(ctx context.Context) error {
	return s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		attempts, err := tx.Repository.Pending(ctx)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			r, err := tx.Repository.RecipeByID(ctx, a.Recipe)
			if err != nil {
				return err
			}
			if r.Outcome.Kind != "" {
				if err = s.fulfill(ctx, tx, &a, r.Outcome); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Service) Affected(ctx context.Context, recipe core.ID) ([]domain.Attempt, error) {
	var result []domain.Attempt
	err := s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		var err error
		result, err = tx.Repository.Affected(ctx, recipe)
		return err
	})
	return result, err
}

func (s *Service) Pending(ctx context.Context) ([]domain.Attempt, error) {
	var result []domain.Attempt
	err := s.repo.WithinCrafting(ctx, func(tx Transaction) error {
		var err error
		result, err = tx.Repository.Pending(ctx)
		return err
	})
	return result, err
}
