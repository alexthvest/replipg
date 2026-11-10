package app

import (
	"context"
	"log/slog"
	"time"

	appcraft "github.com/alexthvest/replipg/internal/application/crafting"
	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/yohamta/donburi"
)

type craftingOperation struct {
	Claim    bool
	Work     appcraft.Claim
	Outcome  domain.Outcome
	Failed   bool
	Response chan craftingResult
}

type craftingResult struct {
	Work  appcraft.Claim
	Found bool
}

type craftingCall func(context.Context, *runtime.Engine, core.Scope, craftingOperation) (core.View, error)

type craftingOwner struct {
	engine *runtime.Engine
	scope  core.Scope
	call   craftingCall
}

func craftingOperations(registry *runtime.Registry, m *modules, lease time.Duration) (craftingCall, error) {
	return runtime.Bind(registry, "crafting-work", func(ctx context.Context, world donburi.World, _ core.Scope, input craftingOperation) (core.View, error) {
		var result craftingResult
		if input.Claim {
			pending, err := m.crafting.Pending(ctx)
			if err != nil {
				return core.View{}, err
			}
			if err := m.crafting.Recover(ctx); err != nil {
				return core.View{}, err
			}
			for _, attempt := range pending {
				if err := m.projectCrafting(ctx, world, attempt.Recipe); err != nil {
					return core.View{}, err
				}
			}
			work, found, err := m.crafting.Claim(ctx, lease)
			if err != nil {
				return core.View{}, err
			}
			result.Work, result.Found = work, found
		} else {
			var err error
			if input.Failed {
				err = m.crafting.Fail(ctx, input.Work)
			} else {
				err = m.crafting.Complete(ctx, input.Work, input.Outcome)
			}
			if err != nil {
				return core.View{}, err
			}
			if err = m.projectCrafting(ctx, world, input.Work.Recipe); err != nil {
				return core.View{}, err
			}
		}
		input.Response <- result
		return core.View{}, nil
	})
}

func (o craftingOwner) operation(ctx context.Context, input craftingOperation) (craftingResult, error) {
	input.Response = make(chan craftingResult, 1)
	if _, err := o.call(ctx, o.engine, o.scope, input); err != nil {
		return craftingResult{}, err
	}
	return <-input.Response, nil
}

type craftingWorkerOwner interface {
	operation(context.Context, craftingOperation) (craftingResult, error)
}

// Generation is owned by this goroutine; only typed operations cross into the world.
func runCraftingWorker(
	ctx context.Context,
	owner craftingWorkerOwner,
	generator appcraft.Generator,
	interval time.Duration,
	log *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		result, err := owner.operation(ctx, craftingOperation{Claim: true})
		if err == nil && result.Found {
			outcome, generationErr := generator.Generate(ctx, result.Work.Ingredients)
			if generationErr == nil {
				generationErr = outcome.Validate()
			}
			if ctx.Err() != nil {
				return
			}

			_, err = owner.operation(ctx, craftingOperation{
				Work:    result.Work,
				Outcome: outcome,
				Failed:  generationErr != nil,
			})
		}

		if err != nil && ctx.Err() == nil {
			log.Error("crafting worker operation failed")
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
