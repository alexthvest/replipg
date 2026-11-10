package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appcraft "github.com/alexthvest/replipg/internal/application/crafting"
	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

func configureCrafting(
	registry *runtime.Registry,
	m *modules,
	db *sqlite.Database,
	routes map[string]boundRoute,
	enabled bool,
) error {
	service, err := appcraft.New(db, core.NewID, time.Now, enabled)
	if err != nil {
		return err
	}

	m.crafting = service
	m.craftReviews = map[string]appcraft.Confirmation{}
	m.craftSnapshots = map[string]map[core.ID]items.Snapshot{}

	if err = registry.Hook(runtime.Hook{
		Name:     "crafting",
		Requires: []string{"inventory"},
		Run: func(ctx context.Context, world donburi.World) error {
			if err := service.Recover(ctx); err != nil {
				return err
			}

			// Recovery can settle saved outcomes before Telegram intake begins.
			entries, err := db.LoadInventory(ctx)
			if err != nil {
				return err
			}

			characters, err := db.LoadCharacters(ctx)
			if err != nil {
				return err
			}

			for _, character := range characters {
				var owned []inventory.Entry
				for _, entry := range entries {
					if entry.Group == character.Group && entry.Character == character.ID {
						owned = append(owned, entry)
					}
				}
				if err = m.inventoryProjection.Reconcile(world, character.Group, character.ID, owned); err != nil {
					return err
				}
			}
			return nil
		},
	}); err != nil {
		return err
	}

	route, err := runtime.Bind(registry, "craft", func(ctx context.Context, world donburi.World, scope core.Scope, input telegram.Input) (core.View, error) {
		if strings.TrimSpace(input.Args) != "" {
			return m.text(ctx, scope, "command.craft.usage")
		}

		character, err := m.characters.Profile(ctx, scope, input.User)
		if err != nil {
			return m.text(ctx, scope, "error.persistence")
		}
		if err = m.projectCharacter(ctx, world, character); err != nil {
			return core.View{}, err
		}

		session, err := m.sessions.Create("craft", scope, scope.Actor)
		if err != nil {
			return core.View{}, err
		}
		session.Character = character.ID
		session.Craft = &menus.CraftSelection{}
		m.craftSnapshots[session.Token] = map[core.ID]items.Snapshot{}
		session.Screen = "composition"
		m.sessions.Depend(session, menus.Dependency{
			Kind:      "inventory",
			Group:     scope.Group,
			Character: character.ID,
		})
		view, err := m.craftingView(ctx, scope, session)
		view.Menu = session.Token
		return view, err
	})
	if err == nil {
		routes["craft"] = route
	}
	return err
}

func (m *modules) craftAllocations(ctx context.Context, scope core.Scope, session *menus.Session) ([]domain.Allocation, error) {
	entries, err := m.inventory.List(ctx, scope.Group, session.Character)
	if err != nil {
		return nil, err
	}

	var allocations []domain.Allocation
	for _, pick := range session.Craft.Picks() {
		found := false
		for _, entry := range entries {
			if entry.ID == pick.Entry && entry.Snapshot == m.craftSnapshots[session.Token][pick.Entry] && entry.Snapshot.Components.Material && pick.Quantity > 0 && pick.Quantity <= entry.Available() {
				allocations = append(allocations, domain.Allocation{
					Entry:    entry.ID,
					Snapshot: entry.Snapshot,
					Quantity: pick.Quantity,
				})
				found = true
				break
			}
		}
		if !found {
			return nil, menus.ErrInvalid
		}
	}
	if len(allocations) == 0 {
		return nil, menus.ErrInvalid
	}
	if _, err := domain.Identify(allocations); err != nil {
		return nil, err
	}
	return allocations, nil
}

func (m *modules) craftingAction(ctx context.Context, world donburi.World, scope core.Scope, session *menus.Session, action menus.Action) (core.View, error) {
	if session.Opener != scope.Actor || session.Craft == nil || session.Closing {
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
	if action.Kind == "confirm" {
		return m.confirmCraft(ctx, world, scope, session, action)
	}

	selection := session.Craft
	switch action.Kind {
	case "add":
		session.Screen = "materials"
	case "select", "edit":
		entry, err := m.inventory.Inspect(ctx, scope.Group, session.Character, action.Entry)
		if err != nil {
			if action.Kind != "edit" || selection.Quantity(action.Entry) == 0 {
				return m.craftFeedback(ctx, scope, session, domain.Unavailable)
			}
			entry = inventory.Entry{
				ID:       action.Entry,
				Snapshot: m.craftSnapshots[session.Token][action.Entry],
			}
		}
		if !entry.Snapshot.Components.Material {
			return m.craftFeedback(ctx, scope, session, domain.Unavailable)
		}
		m.craftSnapshots[session.Token][entry.ID] = entry.Snapshot
		selection.Edit(entry.ID, session.Screen)
		session.Screen = "editor"
	case "quantity":
		if selection.Draft == nil {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		entry, err := m.inventory.Inspect(ctx, scope.Group, session.Character, selection.Draft.Entry)
		if err != nil || entry.Snapshot != m.craftSnapshots[session.Token][selection.Draft.Entry] || action.Quantity <= 0 || action.Quantity > entry.Available() || entry.Snapshot.Mode != items.Stackable {
			return m.craftFeedback(ctx, scope, session, domain.Unavailable)
		}
		selection.Draft.Quantity = action.Quantity
	case "save":
		if selection.Draft == nil {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		entry, err := m.inventory.Inspect(ctx, scope.Group, session.Character, selection.Draft.Entry)
		if err != nil || !entry.Snapshot.Components.Material || entry.Snapshot != m.craftSnapshots[session.Token][selection.Draft.Entry] || selection.Save(entry.Available(), entry.Snapshot.Mode == items.Individual) != nil {
			return m.craftFeedback(ctx, scope, session, domain.Unavailable)
		}
		delete(m.craftReviews, session.Token)
		session.Screen = "composition"
	case "remove":
		if selection.Draft == nil {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		selection.Remove(selection.Draft.Entry)
		selection.Draft = nil
		delete(m.craftReviews, session.Token)
		session.Screen = "composition"
	case "back":
		session.Screen = "composition"
		if selection.Draft != nil {
			session.Screen = selection.Draft.Origin
			selection.Draft = nil
		}
		delete(m.craftReviews, session.Token)
	case "page":
		switch session.Screen {
		case "materials":
			selection.MaterialPage = action.Page
		case "result", "review", "shortage":
			selection.ResultPage = action.Page
		default:
			selection.Page = action.Page
		}
	case "review":
		return m.reviewCraft(ctx, scope, session)
	case "clear", "new":
		selection.Clear()
		m.craftSnapshots[session.Token] = map[core.ID]items.Snapshot{}
		session.Order = ""
		delete(m.craftReviews, session.Token)
		session.Screen = "composition"
	case "repeat", "retry", "change", "use-result":
		return m.continueCraft(ctx, scope, session, action.Kind)
	case "inventory":
		delete(m.craftReviews, session.Token)
		session.Craft = nil
		delete(m.craftSnapshots, session.Token)
		session.Kind = "inventory"
		session.Screen = ""
		session.Order = ""
		session.Selection = ""
		session.Page = 0
		return m.inventoryView(ctx, scope, session)
	case "close":
		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}
		delete(m.craftReviews, session.Token)
		view := core.View{Text: core.Literal(m.bundle.Locales().Text(language, "menu.craft.closed"))}
		m.sessions.Close(session, view)
		return view, nil
	case "refresh":
	default:
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
	return m.craftingView(ctx, scope, session)
}

func (m *modules) confirmCraft(ctx context.Context, world donburi.World, scope core.Scope, session *menus.Session, action menus.Action) (core.View, error) {
	attempt, found, err := m.crafting.Attempt(ctx, action.Quote)
	if err != nil {
		return m.feedback(ctx, scope, "error.persistence")
	}
	if found {
		if attempt.Scope.Group != scope.Group || attempt.Scope.Actor != scope.Actor || attempt.Character != session.Character || attempt.MessageID != session.Message {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		// Accepted callbacks keep their idempotency without navigating a newer draft.
		return m.craftingView(ctx, scope, session)
	}

	review, ok := m.craftReviews[session.Token]
	if !ok || review.Token != action.Quote {
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
	review.Scope = scope
	attempt, err = m.crafting.Confirm(ctx, review)
	if err != nil {
		delete(m.craftReviews, session.Token)
		if errors.Is(err, appcraft.ErrDisabled) {
			session.Screen = "disabled"
			return m.craftFeedback(ctx, scope, session, "disabled")
		}
		if errors.Is(err, domain.ErrUnavailable) || errors.Is(err, domain.ErrIngredients) {
			session.Screen = "shortage"
			return m.craftFeedback(ctx, scope, session, domain.Unavailable)
		}
		return m.feedback(ctx, scope, "error.persistence")
	}
	m.sessions.AcceptConfirmation(session, action.Quote)
	delete(m.craftReviews, session.Token)
	session.Order = attempt.Token
	session.Screen = "result"
	session.Craft.ResultPage = 0
	if err = m.projectCrafting(ctx, world, attempt.Recipe); err != nil {
		return core.View{}, err
	}
	return m.craftingView(ctx, scope, session)
}

func (m *modules) reviewCraft(ctx context.Context, scope core.Scope, session *menus.Session) (core.View, error) {
	if len(session.Craft.Picks()) == 0 {
		return m.craftFeedback(ctx, scope, session, "composition-empty")
	}
	allocations, err := m.craftAllocations(ctx, scope, session)
	if err != nil {
		if !errors.Is(err, menus.ErrInvalid) && !errors.Is(err, domain.ErrIngredients) {
			return m.feedback(ctx, scope, "error.persistence")
		}
		session.Screen = "shortage"
		session.Craft.ResultPage = 0
		return m.craftFeedback(ctx, scope, session, domain.Unavailable)
	}
	token, err := core.NewID()
	if err != nil {
		return core.View{}, err
	}
	m.craftReviews[session.Token] = appcraft.Confirmation{
		Token:       token,
		Scope:       scope,
		Character:   session.Character,
		MessageID:   session.Message,
		Expires:     time.Now().Add(m.menuTTL),
		Allocations: allocations,
	}
	session.Craft.Freeze(token)
	session.Craft.ResultPage = 0
	session.Screen = "review"
	return m.craftingView(ctx, scope, session)
}

func (m *modules) continueCraft(ctx context.Context, scope core.Scope, session *menus.Session, kind string) (core.View, error) {
	if session.Order != "" {
		attempt, found, err := m.crafting.Attempt(ctx, session.Order)
		if err != nil {
			return m.feedback(ctx, scope, "error.persistence")
		}
		if !found || attempt.State == domain.Pending {
			return m.feedback(ctx, scope, "error.invalid_menu")
		}
		entries, err := m.inventory.List(ctx, scope.Group, session.Character)
		if err != nil {
			return m.feedback(ctx, scope, "error.persistence")
		}
		if kind == "use-result" {
			if attempt.State != domain.Success || attempt.Outcome.Item == nil || !attempt.Outcome.Item.Components.Material {
				return m.feedback(ctx, scope, "error.invalid_menu")
			}
			output, available := availableCraftOutput(entries, *attempt.Outcome.Item)
			if !available {
				return m.craftFeedback(ctx, scope, session, domain.Unavailable)
			}
			session.Craft.Clear()
			if err = session.Craft.Set(output.ID, 1, output.Available(), output.Snapshot.Mode == items.Individual); err != nil {
				return core.View{}, err
			}
			m.craftSnapshots[session.Token] = map[core.ID]items.Snapshot{output.ID: output.Snapshot}
		} else {
			m.craftSnapshots[session.Token] = restoreCraft(session.Craft, attempt.Allocations, entries)
		}
	}
	delete(m.craftReviews, session.Token)
	session.Order = ""
	session.Screen = "composition"
	if kind == "retry" {
		return m.reviewCraft(ctx, scope, session)
	}
	return m.craftingView(ctx, scope, session)
}

func (m *modules) craftFeedback(ctx context.Context, scope core.Scope, session *menus.Session, state string) (core.View, error) {
	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}

	view := core.View{Text: core.Literal(m.bundle.Locales().Text(language, "menu.craft."+state))}
	view.Feedback = views.Preview(textOfCraft(view), 90)

	// Keep editing available so an invalid selection can be revised.
	current, err := m.craftingView(ctx, scope, session)
	current.Feedback = view.Feedback
	return current, err
}

func textOfCraft(view core.View) string {
	var text strings.Builder
	for _, span := range view.Text {
		text.WriteString(span.Text)
	}
	return text.String()
}

func (m *modules) projectCrafting(ctx context.Context, world donburi.World, recipe core.ID) error {
	attempts, err := m.crafting.Affected(ctx, recipe)
	if err != nil {
		return &runtime.ProjectionError{Cause: err}
	}

	for _, attempt := range attempts {
		entries, err := m.inventory.List(ctx, attempt.Scope.Group, attempt.Character)
		if err != nil {
			return &runtime.ProjectionError{Cause: err}
		}
		if err = m.inventoryProjection.Reconcile(world, attempt.Scope.Group, attempt.Character, entries); err != nil {
			return &runtime.ProjectionError{Cause: err}
		}
		m.invalidateInventory(ctx, attempt.Scope.Group, attempt.Character)
	}
	return nil
}
