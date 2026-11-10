package app

import (
	"context"
	"strconv"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
)

type craftControl struct {
	caption string
	action  menus.Action
}

type craftKeyboard struct {
	rows [][]craftControl
}

func (k *craftKeyboard) row(controls ...craftControl) {
	if len(controls) > 0 {
		k.rows = append(k.rows, controls)
	}
}

func (k *craftKeyboard) bind(sessions *menus.Sessions, session *menus.Session, view *core.View) error {
	var actions []menus.Action
	for _, row := range k.rows {
		for _, control := range row {
			actions = append(actions, control.action)
		}
	}
	refs, err := sessions.Actions(session, actions)
	if err != nil {
		return err
	}
	i := 0
	for _, row := range k.rows {
		var buttons []core.Button
		for _, control := range row {
			buttons = append(buttons, core.Button{
				Caption: views.Preview(control.caption, 60),
				Data:    refs[i],
			})
			i++
		}
		view.Rows = append(view.Rows, buttons)
	}
	return nil
}

func (m *modules) craftingView(ctx context.Context, scope core.Scope, session *menus.Session) (result core.View, resultErr error) {
	defer func() {
		if resultErr == nil {
			m.sessions.Stage(session, result)
		}
	}()
	if session.Closing {
		return session.Desired, nil
	}
	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}
	entries, err := m.inventory.List(ctx, scope.Group, session.Character)
	if err != nil {
		return core.View{}, err
	}

	catalog := m.bundle.Locales()
	control := func(key, kind string) craftControl {
		return craftControl{
			caption: catalog.Text(language, "menu.craft."+key),
			action:  menus.Action{Kind: kind},
		}
	}
	back := craftControl{
		caption: catalog.Text(language, "menu.back"),
		action:  menus.Action{Kind: "back"},
	}
	closeControl := control("close", "close")
	keyboard := craftKeyboard{}
	selection := session.Craft
	page, pages := 0, 1
	var view core.View
	switch session.Screen {
	case "result":
		attempt, found, err := m.crafting.Attempt(ctx, session.Order)
		if err != nil {
			return core.View{}, err
		}
		if !found {
			return core.View{}, menus.ErrInvalid
		}
		if attempt.State == domain.Unavailable {
			m.craftSnapshots[session.Token] = restoreCraft(selection, attempt.Allocations, entries)
			session.Order = ""
			session.Screen = "shortage"
			return m.craftingView(ctx, scope, session)
		}
		view, page, pages = views.CraftAttempt(catalog, language, attempt, selection.ResultPage)
		selection.ResultPage = page
		m.craftResultControls(&keyboard, attempt, control)
	case "review":
		review, found := m.craftReviews[session.Token]
		if !found {
			session.Screen = "composition"
			return m.craftingView(ctx, scope, session)
		}
		if _, err := m.craftAllocations(ctx, scope, session); err != nil {
			if err != menus.ErrInvalid && err != domain.ErrIngredients {
				return core.View{}, err
			}
			delete(m.craftReviews, session.Token)
			session.Screen = "shortage"
			return m.craftingView(ctx, scope, session)
		}
		view, page, pages = views.CraftReview(catalog, language, review.Allocations, "", selection.ResultPage)
		selection.ResultPage = page
		confirm := control("confirm", "confirm")
		confirm.action.Quote = review.Token
		keyboard.row(confirm, back)
		keyboard.row(closeControl)
	case "editor":
		if selection.Draft == nil {
			session.Screen = "composition"
			return m.craftingView(ctx, scope, session)
		}
		draft := selection.Draft
		entry := inventory.Entry{
			ID:       draft.Entry,
			Snapshot: m.craftSnapshots[session.Token][draft.Entry],
		}
		for _, current := range entries {
			if current.ID == draft.Entry && current.Snapshot == m.craftSnapshots[session.Token][draft.Entry] {
				entry = current
				break
			}
		}
		view = views.CraftEditor(catalog, language, entry, draft.Quantity)
		if entry.Snapshot.Mode == items.Stackable {
			var choices []craftControl
			for _, choice := range menus.CraftQuantityChoices(draft.Quantity, entry.Available(), catalog.Text(language, "menu.craft.max")) {
				choices = append(choices, craftControl{
					caption: choice.Caption,
					action: menus.Action{
						Kind:     "quantity",
						Quantity: choice.Quantity,
					},
				})
			}
			keyboard.row(choices...)
		}
		if selection.Quantity(draft.Entry) > 0 {
			keyboard.row(control("save", "save"), control("remove", "remove"))
		} else {
			keyboard.row(control("add", "save"))
		}
		keyboard.row(back)
	case "materials":
		var visible []inventory.Entry
		view, visible, page, pages = views.CraftMaterials(catalog, language, entries, selection.Quantities(), selection.MaterialPage)
		selection.MaterialPage = page
		var numbers []craftControl
		for i, entry := range visible {
			numbers = append(numbers, craftControl{
				caption: strconv.Itoa(i + 1),
				action: menus.Action{
					Kind:  "select",
					Entry: entry.ID,
				},
			})
		}
		keyboard.row(numbers...)
		keyboard.row(back)
	case "disabled":
		view = views.CraftStatus(catalog, language, "disabled", nil)
		keyboard.row(control("change", "change"), closeControl)
	default:
		var visible []menus.CraftPick
		selectedPage := selection.Page
		if session.Screen == "shortage" {
			selectedPage = selection.ResultPage
		}
		view, visible, page, pages = views.CraftComposition(catalog, language, selection, m.craftSnapshots[session.Token], entries, selectedPage)
		if session.Screen == "shortage" {
			selection.ResultPage = page
			view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, "menu.craft.unavailable")})
			keyboard.row(control("change", "change"), control("clear", "clear"))
			keyboard.row(closeControl)
		} else {
			selection.Page = page
			var numbers []craftControl
			for i, pick := range visible {
				numbers = append(numbers, craftControl{
					caption: strconv.Itoa(i + 1),
					action: menus.Action{
						Kind:  "edit",
						Entry: pick.Entry,
					},
				})
			}
			keyboard.row(numbers...)
			keyboard.row(control("add", "add"), control("clear", "clear"))
			keyboard.row(control("compose-confirm", "review"), closeControl)
		}
	}

	var navigation []craftControl
	if page > 0 {
		navigation = append(navigation, craftControl{
			caption: catalog.Text(language, "menu.previous"),
			action: menus.Action{
				Kind: "page",
				Page: page - 1,
			},
		})
	}
	if page+1 < pages {
		navigation = append(navigation, craftControl{
			caption: catalog.Text(language, "menu.next"),
			action: menus.Action{
				Kind: "page",
				Page: page + 1,
			},
		})
	}
	if len(navigation) > 0 {
		// Navigation precedes action rows, and follows numbered choices if present.
		index := 0
		if len(keyboard.rows) > 0 && len(keyboard.rows[0]) > 0 {
			kind := keyboard.rows[0][0].action.Kind
			if kind == "select" || kind == "edit" {
				index = 1
			}
		}
		keyboard.rows = append(keyboard.rows, nil)
		copy(keyboard.rows[index+1:], keyboard.rows[index:])
		keyboard.rows[index] = navigation
	}
	if err := keyboard.bind(m.sessions, session, &view); err != nil {
		return core.View{}, err
	}
	return view, nil
}

func (m *modules) craftResultControls(keyboard *craftKeyboard, attempt domain.Attempt, control func(string, string) craftControl) {
	switch attempt.State {
	case domain.Pending:
		keyboard.row(control("refresh", "refresh"), control("close", "close"))
	case domain.Success:
		if attempt.Outcome.Item != nil && attempt.Outcome.Item.Components.Material {
			keyboard.row(control("use-result", "use-result"))
		}
		keyboard.row(control("repeat", "repeat"), control("new", "new"))
		keyboard.row(control("inventory", "inventory"), control("close", "close"))
	default:
		keyboard.row(control("retry", "retry"))
		keyboard.row(control("change", "change"), control("close", "close"))
	}
}
