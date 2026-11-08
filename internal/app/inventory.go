package app

import (
	"context"
	"errors"
	"strconv"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

func configureInventory(
	registry *runtime.Registry,
	m *modules,
	db *sqlite.Database,
	routes map[string]boundRoute,
) error {
	service, err := inventory.New(db)
	if err != nil {
		return err
	}

	m.inventory = service
	m.inventoryProjection = inventory.NewProjection()
	coordinator, err := grants.New(db, m.groups, core.NewID)
	if err != nil {
		return err
	}
	m.grants = coordinator

	if err = registry.Hook(runtime.Hook{
		Name:     "inventory",
		Requires: []string{"characters"},
		Run: func(ctx context.Context, world donburi.World) error {
			entries, err := db.LoadInventory(ctx)
			if err != nil {
				return err
			}

			for _, entry := range entries {
				if err = m.inventoryProjection.Apply(world, entry); err != nil {
					return err
				}
			}
			return nil
		},
	}); err != nil {
		return err
	}

	if err = registry.Hook(runtime.Hook{
		Name:     "menus",
		Requires: []string{"groups"},
		Run: func(context.Context, donburi.World) error {
			return nil
		},
		Cleanup: func(donburi.World) {
			m.sweepMenus()
		},
	}); err != nil {
		return err
	}

	give, err := runtime.Bind(registry, "give", func(
		ctx context.Context,
		world donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		id, quantity, err := telegram.ParseGive(input.Args)
		if err != nil {
			return m.text(ctx, scope, "command.give.usage")
		}

		result, err := coordinator.Give(ctx, grants.Request{
			Scope:     scope,
			Recipient: input.Recipient,
			ItemID:    id,
			Quantity:  quantity,
		}, m.bundle)
		if errors.Is(err, grants.ErrDenied) {
			return m.text(ctx, scope, "error.denied")
		}
		if err != nil {
			return m.text(ctx, scope, "command.give.failed")
		}
		if err = m.projectGrant(ctx, world, scope, result); err != nil {
			return core.View{}, err
		}

		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}

		text, err := m.bundle.Locales().Render(language, "command.give.success", map[string]string{
			"recipient": result.Character.Name,
			"item":      result.Snapshot.Text(language).Name,
			"quantity":  strconv.FormatInt(result.Quantity, 10),
		})
		return core.View{Text: text}, err
	})
	if err != nil {
		return err
	}

	routes["give"] = give
	browse, err := runtime.Bind(registry, "inventory", func(
		ctx context.Context,
		world donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		if input.Args != "" {
			return m.text(ctx, scope, "command.inventory.usage")
		}

		character, err := m.characters.Profile(ctx, scope, input.User)
		if err != nil {
			return m.text(ctx, scope, "error.persistence")
		}

		if err = m.projectCharacter(ctx, world, character); err != nil {
			return core.View{}, err
		}

		session, err := m.sessions.Create("inventory", scope, scope.Actor)
		if err != nil {
			return core.View{}, err
		}

		session.Character = character.ID
		m.sessions.Depend(session, menus.Dependency{
			Kind:      "inventory",
			Group:     scope.Group,
			Character: character.ID,
		})
		view, err := m.inventoryView(ctx, scope, session)
		view.Menu = session.Token
		return view, err
	})
	if err != nil {
		return err
	}

	routes["inventory"] = browse
	return nil
}

func (m *modules) projectCharacter(ctx context.Context, world donburi.World, c characters.Character) error {
	group, err := m.groups.State(ctx, c.Group)
	if err != nil {
		return &runtime.ProjectionError{Cause: err}
	}

	if err = m.groupProjection.Apply(world, group); err != nil {
		return &runtime.ProjectionError{Cause: err}
	}

	if err = m.characterProjection.Apply(world, c); err != nil {
		return &runtime.ProjectionError{Cause: err}
	}
	return nil
}

func (m *modules) projectGrant(ctx context.Context, world donburi.World, scope core.Scope, result grants.Result) error {
	// Replays project current state so later renames and grants cannot be reverted.
	current, err := m.characters.Current(ctx, scope.Group, result.Character.User)
	if err != nil {
		return &runtime.ProjectionError{Cause: err}
	}

	if err = m.projectCharacter(ctx, world, current); err != nil {
		return err
	}

	entries, err := m.inventory.List(ctx, scope.Group, current.ID)
	if err != nil {
		return &runtime.ProjectionError{Cause: err}
	}

	for _, entry := range entries {
		if err = m.inventoryProjection.Apply(world, entry); err != nil {
			return &runtime.ProjectionError{Cause: err}
		}
	}
	m.invalidateInventory(ctx, scope.Group, current.ID)
	return nil
}

func (m *modules) inventoryView(ctx context.Context, scope core.Scope, session *menus.Session) (result core.View, resultErr error) {
	defer func() {
		if resultErr == nil {
			m.sessions.Stage(session, result)
		}
	}()
	c, err := m.characters.Current(ctx, scope.Group, scope.Actor)
	if err != nil {
		return core.View{}, err
	}
	if c.ID != session.Character {
		return core.View{}, menus.ErrInvalid
	}

	entries, err := m.inventory.List(ctx, scope.Group, c.ID)
	if err != nil {
		return core.View{}, err
	}

	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}
	if session.Selection != "" {
		for _, entry := range entries {
			if entry.ID == session.Selection {
				view := views.Inspection(m.bundle.Locales(), language, entry)
				refs, err := m.sessions.Actions(session, []menus.Action{{Kind: "back"}})
				if err != nil {
					return core.View{}, err
				}
				view.Rows = [][]core.Button{{{
					Caption: views.Preview(m.bundle.Locales().Text(language, "menu.back"), 60),
					Data:    refs[0],
				}}}
				return view, nil
			}
		}
		session.Selection = ""
	}

	view, visible, page, pages := views.Inventory(m.bundle.Locales(), language, c, entries, session.Page)
	session.Page = page
	var actions []menus.Action
	for _, entry := range visible {
		actions = append(actions, menus.Action{
			Kind:  "inspect",
			Entry: entry.ID,
		})
	}

	if page > 0 {
		actions = append(actions, menus.Action{
			Kind: "page",
			Page: page - 1,
		})
	}

	if page+1 < pages {
		actions = append(actions, menus.Action{
			Kind: "page",
			Page: page + 1,
		})
	}

	refs, err := m.sessions.Actions(session, actions)
	if err != nil {
		return core.View{}, err
	}

	if len(visible) > 0 {
		var row []core.Button
		for i := range visible {
			row = append(row, core.Button{
				Caption: strconv.Itoa(i + 1),
				Data:    refs[i],
			})
		}

		view.Rows = append(view.Rows, row)
	}

	var navigation []core.Button
	for i := len(visible); i < len(actions); i++ {
		key := "menu.next"
		if actions[i].Page < page {
			key = "menu.previous"
		}

		navigation = append(navigation, core.Button{
			Caption: views.Preview(m.bundle.Locales().Text(language, key), 60),
			Data:    refs[i],
		})
	}

	if len(navigation) > 0 {
		view.Rows = append(view.Rows, navigation)
	}
	return view, nil
}

func (m *modules) inventoryAction(
	ctx context.Context,
	scope core.Scope,
	session *menus.Session,
	action menus.Action,
) (core.View, error) {
	if session.Opener != scope.Actor {
		return m.feedback(ctx, scope, "error.invalid_menu")
	}

	switch action.Kind {
	case "page":
		session.Selection = ""
		session.Page = action.Page
		return m.inventoryView(ctx, scope, session)
	case "back":
		session.Selection = ""
		return m.inventoryView(ctx, scope, session)
	case "inspect":
		c, err := m.characters.Current(ctx, scope.Group, scope.Actor)
		if err != nil || c.ID != session.Character {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		_, err = m.inventory.Inspect(ctx, scope.Group, c.ID, action.Entry)
		if err != nil {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		session.Selection = action.Entry
		return m.inventoryView(ctx, scope, session)
	default:
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
}
