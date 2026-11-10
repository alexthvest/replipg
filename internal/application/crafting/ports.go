// Package crafting coordinates durable attempts without network calls in transactions.
package crafting

import (
	"context"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
)

type Generator interface {
	Generate(context.Context, []domain.Ingredient) (domain.Outcome, error)
}

type Recipe struct {
	ID       core.ID
	Identity domain.Identity
	Outcome  domain.Outcome
}

type Claim struct {
	Recipe      core.ID
	Token       core.ID
	Ingredients []domain.Ingredient
	Expires     time.Time
}

type Confirmation struct {
	Token       core.ID
	Scope       core.Scope
	Character   core.ID
	MessageID   int
	Expires     time.Time
	Allocations []domain.Allocation
}

type Repository interface {
	Recipe(context.Context, domain.Identity) (Recipe, bool, error)
	EnsureRecipe(context.Context, Recipe) (Recipe, error)
	Attempt(context.Context, core.ID) (domain.Attempt, bool, error)
	SaveAttempt(context.Context, domain.Attempt) error
	Pending(context.Context) ([]domain.Attempt, error)
	Affected(context.Context, core.ID) ([]domain.Attempt, error)
	Claim(context.Context, time.Time, time.Time, core.ID) (Claim, bool, error)
	Publish(context.Context, Claim, domain.Outcome) (bool, error)
	FailClaim(context.Context, Claim) (bool, error)
	RecipeByID(context.Context, core.ID) (Recipe, error)
	Validate(context.Context, domain.Attempt) error
	Consume(context.Context, domain.Attempt) error
}

type Transaction struct {
	Repository Repository
	Inventory  inventory.Writer
}

type UnitOfWork interface {
	WithinCrafting(context.Context, func(Transaction) error) error
}
