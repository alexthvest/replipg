package app

import (
	"context"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/yohamta/donburi"
)

func (m *modules) invalidateInventory(_ context.Context, group core.GroupID, character core.ID) {
	m.sessions.Invalidate(menus.Dependency{
		Kind:      "inventory",
		Group:     group,
		Character: character,
	})
}

func (m *modules) invalidateOrders(group core.GroupID, character core.ID) {
	m.sessions.Invalidate(menus.Dependency{
		Kind:      "orders",
		Group:     group,
		Character: character,
	})
}

func (m *modules) sweepMenus() {
	m.sessions.Sweep()
	for token := range m.craftReviews {
		if !m.sessions.Contains(token) {
			delete(m.craftReviews, token)
			delete(m.craftSnapshots, token)
		}
	}
	for token := range m.craftSnapshots {
		if !m.sessions.Contains(token) {
			delete(m.craftSnapshots, token)
		}
	}
	for token := range m.quotes {
		if !m.sessions.Contains(token) {
			delete(m.quotes, token)
		}
	}
}

func (m *modules) renderSession(ctx context.Context, session *menus.Session) (core.View, error) {
	scope := core.Scope{
		Group: session.Group,
		Actor: session.Actor,
	}
	switch session.Kind {
	case "inventory":
		return m.inventoryView(ctx, scope, session)
	case "items":
		return m.itemsView(ctx, scope, session)
	case "shop":
		return m.shopView(ctx, scope, session)
	case "craft":
		return m.craftingView(ctx, scope, session)
	default:
		language, err := m.localization.Language(ctx, session.Group)
		if err != nil {
			return core.View{}, err
		}
		return m.settingsView(session, language)
	}
}

type menuOperation struct {
	Limit    int
	Job      menus.Job
	Outcome  *menus.Outcome
	Response chan menuResult
}

type menuResult struct {
	Jobs   []menus.Job
	Active bool
}

type menuOwner struct {
	engine *runtime.Engine
	scope  core.Scope
	call   menuCall
}

type menuCall func(context.Context, *runtime.Engine, core.Scope, menuOperation) (core.View, error)

func menuOperations(registry *runtime.Registry, m *modules) (menuCall, error) {
	return runtime.Bind(registry, "menu-delivery", func(
		ctx context.Context,
		_ donburi.World,
		_ core.Scope,
		input menuOperation,
	) (core.View, error) {
		var result menuResult
		if input.Outcome != nil {
			m.sessions.Complete(*input.Outcome)
		} else if input.Job.Token != "" {
			result.Active = m.sessions.Active(input.Job)
		} else if input.Limit > 0 {
			m.sweepMenus()
			for _, session := range m.sessions.Dirty() {
				if _, err := m.renderSession(ctx, session); err != nil {
					m.sessions.Remove(session.Token)
				}
			}
			result.Jobs = m.sessions.Claim(input.Limit)
		}
		input.Response <- result
		return core.View{}, nil
	})
}

func (o menuOwner) operation(ctx context.Context, input menuOperation) (menuResult, error) {
	input.Response = make(chan menuResult, 1)
	if _, err := o.call(ctx, o.engine, o.scope, input); err != nil {
		return menuResult{}, err
	}
	return <-input.Response, nil
}

func (o menuOwner) Claim(ctx context.Context, limit int) ([]menus.Job, error) {
	result, err := o.operation(ctx, menuOperation{Limit: limit})
	return result.Jobs, err
}

func (o menuOwner) Active(ctx context.Context, job menus.Job) (bool, error) {
	result, err := o.operation(ctx, menuOperation{Job: job})
	return result.Active, err
}

func (o menuOwner) Complete(ctx context.Context, outcome menus.Outcome) error {
	_, err := o.operation(ctx, menuOperation{Outcome: &outcome})
	return err
}
