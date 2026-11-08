package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/localization"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

type boundRoute func(context.Context, *runtime.Engine, core.Scope, telegram.Input) (core.View, error)

type modules struct {
	bundle              *content.Bundle
	groups              *groups.Service
	characters          *characters.Service
	characterProjection *characters.Projection
	localization        *localization.Service
	groupProjection     *groups.Projection
	sessions            *menus.Sessions
	inventory           *inventory.Service
	inventoryProjection *inventory.Projection
	grants              *grants.Service
}

func newModules(
	service *groups.Service,
	characterService *characters.Service,
	bundle *content.Bundle,
	ttl time.Duration,
) (*modules, error) {
	locales, err := localization.NewService(service)
	if err != nil {
		return nil, err
	}

	sessions, err := menus.New(ttl, 1000, time.Now, menus.RandomToken)
	if err != nil {
		return nil, err
	}
	return &modules{
		bundle:              bundle,
		groups:              service,
		characters:          characterService,
		characterProjection: characters.NewProjection(),
		localization:        locales,
		groupProjection:     groups.NewProjection(),
		sessions:            sessions,
	}, nil
}

func (m *modules) text(ctx context.Context, scope core.Scope, key string) (core.View, error) {
	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}
	return core.View{Text: core.Literal(m.bundle.Locales().Text(language, key))}, nil
}

func (m *modules) feedback(ctx context.Context, scope core.Scope, key string) (core.View, error) {
	view, err := m.text(ctx, scope, key)
	if err != nil {
		return view, err
	}
	return core.View{Feedback: views.Preview(view.Text[0].Text, 90)}, nil
}

func (m *modules) settingsView(session *menus.Session, language string) (core.View, error) {
	view := views.Settings(m.bundle.Locales(), language)
	refs := session.Refs
	if len(refs) == 0 {
		var err error
		refs, err = m.sessions.Actions(session, []menus.Action{
			{
				Kind:     "language",
				Language: "en",
			},
			{
				Kind:     "language",
				Language: "ru",
			},
		})
		if err != nil {
			return core.View{}, err
		}

		session.Refs = refs
	}

	for i, choice := range []string{"en", "ru"} {
		caption := views.Preview(m.bundle.Locales().Text(language, "language."+choice), 60)
		if choice == language {
			caption = "✓ " + caption
		}

		view.Rows = append(view.Rows, []core.Button{
			{
				Caption: caption,
				Data:    refs[i],
			},
		})
	}
	return view, nil
}

func groupRoutes(registry *runtime.Registry, m *modules) (map[string]boundRoute, error) {
	routes := map[string]boundRoute{}
	register := func(
		name string,
		handler func(
			context.Context,
			donburi.World,
			core.Scope,
			telegram.Input,
		) (core.View, error),
	) error {
		route, err := runtime.Bind(registry, name, handler)
		if err == nil {
			routes[name] = route
		}
		return err
	}

	if err := register("ping", func(
		ctx context.Context,
		_ donburi.World,
		scope core.Scope,
		_ telegram.Input,
	) (core.View, error) {
		return m.text(ctx, scope, "command.ping.message")
	}); err != nil {
		return nil, err
	}

	if err := register("admin", func(
		ctx context.Context,
		world donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		fields := strings.Fields(input.Args)
		if len(fields) != 1 || (fields[0] != "add" && fields[0] != "remove") || input.Recipient == nil {
			return m.text(ctx, scope, "command.admin.usage")
		}

		state, err := m.groups.Admin(ctx, scope, input.Recipient.ID, fields[0] == "add")
		if errors.Is(err, groups.ErrDenied) {
			return m.text(ctx, scope, "error.denied")
		}
		if err != nil {
			return m.text(ctx, scope, "error.persistence")
		}

		if err = m.groupProjection.Apply(world, state); err != nil {
			return core.View{}, &runtime.ProjectionError{Cause: err}
		}

		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}

		text, err := m.bundle.Locales().Render(language, "command.admin.updated", map[string]string{
			"user": strconv.FormatInt(int64(input.Recipient.ID), 10),
		})
		return core.View{Text: text}, err
	}); err != nil {
		return nil, err
	}

	if err := register("settings", func(
		ctx context.Context,
		_ donburi.World,
		scope core.Scope,
		_ telegram.Input,
	) (core.View, error) {
		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}

		session, err := m.sessions.Create("settings", scope, 0)
		if err != nil {
			return core.View{}, err
		}

		view := views.Settings(m.bundle.Locales(), language)
		view.Menu = session.Token
		return view, nil
	}); err != nil {
		return nil, err
	}

	if err := register("bind-menu", func(
		ctx context.Context,
		_ donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		session, err := m.sessions.Bind(input.Data, scope, input.MessageID)
		if err != nil {
			return core.View{}, err
		}
		if session.Kind == "inventory" {
			return m.inventoryView(ctx, scope, session)
		}
		if session.Kind == "items" {
			return m.itemsView(ctx, scope, session)
		}

		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}
		return m.settingsView(session, language)
	}); err != nil {
		return nil, err
	}

	if err := register("menu", func(
		ctx context.Context,
		world donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		session, action, err := m.sessions.Resolve(input.Data, scope, input.MessageID)
		if err != nil {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		if session.Kind == "inventory" {
			return m.inventoryAction(ctx, scope, session, action)
		}
		if session.Kind == "items" {
			return m.itemsAction(ctx, world, scope, input, session, action)
		}
		if session.Kind != "settings" || action.Kind != "language" || (action.Language != "en" && action.Language != "ru") {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}

		allowed, err := m.groups.Allowed(ctx, scope)
		if err != nil {
			return m.feedback(ctx, scope, "error.persistence")
		}
		if !allowed {
			return m.feedback(ctx, scope, "error.denied")
		}

		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}
		if action.Language == language {
			return core.View{}, nil
		}

		state, err := m.groups.ChangeLanguage(ctx, scope, action.Language)
		if err != nil {
			return m.feedback(ctx, scope, "error.persistence")
		}

		if err = m.groupProjection.Apply(world, state); err != nil {
			return core.View{}, &runtime.ProjectionError{Cause: err}
		}
		return m.settingsView(session, state.Language)
	}); err != nil {
		return nil, err
	}
	return routes, nil
}
