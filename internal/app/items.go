package app

import (
	"context"
	"strconv"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

func itemRoutes(registry *runtime.Registry, m *modules, routes map[string]boundRoute) error {
	route, err := runtime.Bind(registry, "items", func(
		ctx context.Context,
		_ donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		if !m.groups.IsOwner(scope.Actor) {
			return m.text(ctx, scope, "error.denied")
		}
		if input.Args != "" {
			return m.text(ctx, scope, "command.items.usage")
		}

		session, err := m.sessions.Create("items", scope, scope.Actor)
		if err != nil {
			return core.View{}, err
		}

		view, err := m.itemsView(ctx, scope, session)
		view.Menu = session.Token
		return view, err
	})
	if err != nil {
		return err
	}

	routes["items"] = route
	return nil
}

func (m *modules) itemsView(ctx context.Context, scope core.Scope, session *menus.Session) (core.View, error) {
	if !m.groups.IsOwner(scope.Actor) || session.Opener != scope.Actor {
		return core.View{}, menus.ErrInvalid
	}

	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}

	view, page, pages := views.Items(m.bundle, language, session.Page)
	session.Page = page
	var actions []menus.Action
	var captions []string
	ids := m.bundle.ItemIDs()
	start := page * views.PageSize
	visible := ids[start:min(start+views.PageSize, len(ids))]
	for _, id := range visible {
		actions = append(actions, menus.Action{
			Kind:   "give",
			ItemID: id,
		})
	}

	if page > 0 {
		actions = append(actions, menus.Action{
			Kind: "page",
			Page: page - 1,
		})
		captions = append(captions, "menu.previous")
	}
	if page+1 < pages {
		actions = append(actions, menus.Action{
			Kind: "page",
			Page: page + 1,
		})
		captions = append(captions, "menu.next")
	}

	refs, err := m.sessions.Actions(session, actions)
	if err != nil {
		return core.View{}, err
	}

	var row []core.Button
	for i := range visible {
		row = append(row, core.Button{
			Caption: strconv.Itoa(i + 1),
			Data:    refs[i],
		})
	}
	if len(row) > 0 {
		view.Rows = append(view.Rows, row)
	}

	row = nil
	for i, caption := range captions {
		row = append(row, core.Button{
			Caption: views.Preview(m.bundle.Locales().Text(language, caption), 60),
			Data:    refs[len(visible)+i],
		})
	}
	if len(row) > 0 {
		view.Rows = append(view.Rows, row)
	}
	return view, nil
}

func (m *modules) itemsAction(
	ctx context.Context,
	world donburi.World,
	scope core.Scope,
	input telegram.Input,
	session *menus.Session,
	action menus.Action,
) (core.View, error) {
	if !m.groups.IsOwner(scope.Actor) || session.Opener != scope.Actor {
		return m.feedback(ctx, scope, "error.denied")
	}
	if action.Kind == "page" {
		session.Page = action.Page
		return m.itemsView(ctx, scope, session)
	}
	if action.Kind != "give" || input.User.ID != scope.Actor {
		return m.feedback(ctx, scope, "error.invalid_menu")
	}

	result, err := m.grants.Give(ctx, grants.Request{
		Scope:     scope,
		Recipient: &input.User,
		ItemID:    action.ItemID,
		Quantity:  1,
	}, m.bundle)
	if err != nil {
		return m.feedback(ctx, scope, "command.give.failed")
	}
	if err = m.projectGrant(ctx, world, scope, result); err != nil {
		return core.View{}, err
	}

	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}

	text, err := m.bundle.Locales().Render(language, "command.items.given", map[string]string{
		"item": result.Snapshot.Text(language).Name,
	})
	if err != nil {
		return core.View{}, err
	}

	var feedback string
	for _, span := range text {
		feedback += span.Text
	}
	return core.View{Feedback: views.Preview(feedback, 90)}, nil
}
