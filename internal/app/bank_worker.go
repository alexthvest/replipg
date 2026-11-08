package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/yohamta/donburi"
)

type bankOperation struct {
	RequestID string
	Reply     string
	Fulfill   core.ID
	Observe   bool
	SendError error
	Claim     bool
	Response  chan []appshop.Work
}

type bankOwner struct {
	engine *runtime.Engine
	scope  core.Scope
	call   bankCall
}

type bankCall func(context.Context, *runtime.Engine, core.Scope, bankOperation) (core.View, error)

func bankOperations(registry *runtime.Registry, m *modules) (bankCall, error) {
	return runtime.Bind(registry, "bank-work", func(
		ctx context.Context,
		world donburi.World,
		_ core.Scope,
		input bankOperation,
	) (core.View, error) {
		if input.Claim {
			work, err := m.shop.DueWork(ctx, time.Now(), 1)
			for _, item := range work {
				m.invalidateOrders(item.Group, item.Character)
			}
			input.Response <- work
			return core.View{}, err
		}
		if input.Observe {
			return core.View{}, m.shop.ObserveSend(ctx, input.RequestID, input.SendError)
		}
		var result appshop.Result
		var err error
		if input.Fulfill != "" {
			result, err = m.shop.Reconcile(ctx, input.Fulfill)
		} else {
			result, err = m.shop.HandleReply(ctx, input.RequestID, input.Reply)
		}
		if result.Order.ID != "" {
			if projectionErr := m.projectShopResult(ctx, world, result); projectionErr != nil {
				return core.View{}, projectionErr
			}
		}
		return core.View{}, err
	})
}

func (m *modules) projectShopResult(ctx context.Context, world donburi.World, result appshop.Result) error {
	quote := result.Order.Quote
	entries, err := m.inventory.List(ctx, quote.Group, quote.Character)
	if err != nil {
		return &runtime.ProjectionError{Cause: err}
	}
	if err = m.inventoryProjection.Reconcile(world, quote.Group, quote.Character, entries); err != nil {
		return &runtime.ProjectionError{Cause: err}
	}
	m.invalidateInventory(ctx, quote.Group, quote.Character)
	m.invalidateOrders(quote.Group, quote.Character)
	return nil
}

func (o bankOwner) reply(ctx context.Context, text string) error {
	id, err := bank.ResponseID(text)
	if err != nil {
		return err
	}
	_, err = o.call(ctx, o.engine, o.scope, bankOperation{
		RequestID: id,
		Reply:     text,
	})
	return err
}

// runBankWorker claims durable intent through the owner and sends outside it.
// One send per second bounds peer traffic without waiting for any bank response.
func runBankWorker(ctx context.Context, owner bankOwner, sender appshop.Sender, log *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			response := make(chan []appshop.Work, 1)
			if _, err := owner.call(ctx, owner.engine, owner.scope, bankOperation{
				Claim:    true,
				Response: response,
			}); err != nil {
				log.Error("bank recovery claim failed", "error", err)
				continue
			}
			for _, work := range <-response {
				if work.Fulfill {
					if _, err := owner.call(ctx, owner.engine, owner.scope, bankOperation{Fulfill: work.OrderID}); err != nil {
						log.Error("shop fulfillment remains pending", "order", work.OrderID, "error", err)
					}
					continue
				}
				err := sender.SendBank(ctx, work.BankID, work.Body)
				if _, observeErr := owner.call(ctx, owner.engine, owner.scope, bankOperation{
					RequestID: work.RequestID,
					Observe:   true,
					SendError: err,
				}); observeErr != nil {
					log.Error("bank send observation remains unknown", "order", work.OrderID, "request", work.RequestID, "error", observeErr)
				}
			}
		}
	}
}
