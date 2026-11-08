package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/shop"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

func configureShop(
	registry *runtime.Registry,
	m *modules,
	db *sqlite.Database,
	routes map[string]boundRoute,
	bankID, treasuryID int64,
) error {
	if treasuryID == 0 {
		treasuryID = 2
	}
	service, err := appshop.New(db, bank.Protocol{}, core.NewID, time.Now, bankID, treasuryID)
	if err != nil {
		return err
	}
	m.shop = service
	m.bankID = bankID
	m.treasuryID = treasuryID
	m.quotes = map[string]shop.Quote{}
	if err = registry.Hook(runtime.Hook{
		Name:     "shop",
		Requires: []string{"inventory"},
		Run: func(ctx context.Context, _ donburi.World) error {
			return service.CheckIdentity(ctx)
		},
	}); err != nil {
		return err
	}
	route, err := runtime.Bind(registry, "shop", func(
		ctx context.Context,
		world donburi.World,
		scope core.Scope,
		input telegram.Input,
	) (core.View, error) {
		if strings.TrimSpace(input.Args) != "" {
			return m.text(ctx, scope, "command.shop.usage")
		}
		character, err := m.characters.Profile(ctx, scope, input.User)
		if err != nil {
			return m.text(ctx, scope, "error.persistence")
		}
		if err = m.projectCharacter(ctx, world, character); err != nil {
			return core.View{}, err
		}
		session, err := m.sessions.Create("shop", scope, scope.Actor)
		if err != nil {
			return core.View{}, err
		}
		session.Character = character.ID
		session.Screen = "catalog"
		session.Quantity = 1
		m.sessions.Depend(session,
			menus.Dependency{
				Kind:      "inventory",
				Group:     scope.Group,
				Character: character.ID,
			},
			menus.Dependency{
				Kind:      "orders",
				Group:     scope.Group,
				Character: character.ID,
			},
		)
		view, err := m.shopView(ctx, scope, session)
		view.Menu = session.Token
		return view, err
	})
	if err == nil {
		routes["shop"] = route
	}
	return err
}

func (m *modules) shopAction(
	ctx context.Context,
	world donburi.World,
	scope core.Scope,
	session *menus.Session,
	action menus.Action,
) (core.View, error) {
	if session.Opener != scope.Actor {
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
	switch action.Kind {
	case "orders":
		session.Screen = "orders"
		session.Selection = ""
		session.Entry = ""
		session.Order = ""
		session.Page = 0
	case "buy", "sell":
		session.Screen = action.Kind + "-review"
		session.Entry = action.Entry
		session.Quantity = 1
	case "page":
		session.Page = action.Page
	case "select":
		session.Selection = action.Entry
		session.Entry = ""
		session.Screen = "item"
		session.Quantity = 1
	case "quantity":
		session.Quantity = action.Quantity
	case "back":
		switch session.Screen {
		case "order":
			session.Screen = "orders"
		case "orders":
			session.Screen = "catalog"
			session.Page = 0
			session.Selection = ""
		case "item":
			session.Screen = "catalog"
			session.Selection = ""
		default:
			session.Screen = "item"
		}
		session.Entry = ""
	case "refresh":
		// Rendering reads committed order state; recovery remains independent.
	case "order":
		session.Screen = "order"
		session.Order = action.Order
	case "confirm":
		quote := m.quotes[session.Token]
		if quote.ID != action.Quote {
			quote = shop.Quote{
				ID:     action.Quote,
				Group:  scope.Group,
				Player: scope.Actor,
			}
		}
		order, err := m.shop.Confirm(ctx, scope, quote, m.bundle)
		if errors.Is(err, appshop.ErrUnavailable) {
			return m.feedback(ctx, scope, "menu.shop.unavailable")
		}
		if errors.Is(err, appshop.ErrQuoteChanged) {
			view, renderErr := m.shopView(ctx, scope, session)
			if renderErr == nil {
				language, _ := m.localization.Language(ctx, scope.Group)
				view.Feedback = views.Preview(m.bundle.Locales().Text(language, "menu.shop.changed"), 90)
			}
			return view, renderErr
		}
		if err != nil {
			return m.feedback(ctx, scope, "menu.shop.failed")
		}
		m.sessions.AcceptConfirmation(session, action.Quote)
		delete(m.quotes, session.Token)
		session.Screen = "order"
		session.Order = order.ID
		if err = m.projectShopResult(ctx, world, appshop.Result{Order: order}); err != nil {
			return core.View{}, err
		}
	default:
		return m.feedback(ctx, scope, "error.invalid_menu")
	}
	return m.shopView(ctx, scope, session)
}

func (m *modules) shopView(ctx context.Context, scope core.Scope, session *menus.Session) (result core.View, resultErr error) {
	m.sweepMenus()
	defer func() {
		if resultErr == nil {
			result = views.WrapShop(result)
			m.sessions.Stage(session, result)
		}
	}()
	character, err := m.characters.Current(ctx, scope.Group, scope.Actor)
	if err != nil || character.ID != session.Character || session.Opener != scope.Actor {
		return core.View{}, menus.ErrInvalid
	}
	language, err := m.localization.Language(ctx, scope.Group)
	if err != nil {
		return core.View{}, err
	}
	catalog := m.bundle.Locales()
	view := views.ShopTitle(catalog, language, "menu.shop.title")
	var actions []menus.Action
	var captions []string
	add := func(caption string, action menus.Action) {
		captions = append(captions, caption)
		actions = append(actions, action)
	}
	if session.Screen == "item" {
		var err error
		view, err = m.shopItemView(ctx, scope, session, language, add)
		if errors.Is(err, menus.ErrInvalid) {
			session.Screen = "catalog"
			session.Selection = ""
			return m.shopView(ctx, scope, session)
		}
		if err != nil {
			return core.View{}, err
		}
	} else if session.Screen == "buy-review" || session.Screen == "sell-review" {
		quote, available, changed, err := m.reviewQuote(ctx, scope, session)
		if err != nil {
			session.Screen = "item"
			return m.shopView(ctx, scope, session)
		}
		view = views.ShopTerms(catalog, language, quote, -1)
		if quote.Direction == shop.Sell {
			entry, err := m.inventory.Inspect(ctx, scope.Group, session.Character, quote.Entry)
			if err != nil {
				return core.View{}, err
			}
			view.Text = append(view.Text, views.ShopAvailability(catalog, language, entry.Quantity, entry.Reserved, entry.Available())...)
		}
		if changed {
			view.Text = append(view.Text, core.Span{Text: "\n\n" + views.Preview(catalog.Text(language, "menu.shop.changed"), 240)})
		}
		limit := int64(1000)
		if quote.Direction == shop.Sell {
			limit = min(limit, available)
			if quote.Snapshot.Mode == items.Individual {
				limit = 1
			}
		}
		limit = shopQuantityLimit(quote.Unit, limit)
		if quote.Snapshot.Mode == items.Stackable {
			for _, choice := range []struct {
				caption  string
				quantity int64
			}{
				{
					caption:  "1",
					quantity: 1,
				},
				{
					caption:  "−",
					quantity: max(1, quote.Quantity-1),
				},
				{
					caption:  "+",
					quantity: min(limit, quote.Quantity+1),
				},
				{
					caption:  catalog.Text(language, "menu.shop.max"),
					quantity: limit,
				},
			} {
				if choice.quantity <= limit {
					add(choice.caption, menus.Action{
						Kind:     "quantity",
						Quantity: choice.quantity,
					})
				}
			}
		}
		if m.bankID > 0 {
			add(catalog.Text(language, "menu.shop.confirm"), menus.Action{
				Kind:  "confirm",
				Quote: quote.ID,
			})
		}
		add(catalog.Text(language, "menu.back"), menus.Action{Kind: "back"})
		add(catalog.Text(language, "menu.shop.orders"), menus.Action{Kind: "orders"})
	} else if session.Screen == "orders" || session.Screen == "order" {
		orders, err := m.shop.List(ctx, scope)
		if err != nil {
			return core.View{}, err
		}
		if session.Screen == "order" {
			found := false
			for _, order := range orders {
				if order.ID == session.Order {
					view = views.ShopOrder(catalog, language, order)
					found = true
					break
				}
			}
			if !found {
				session.Screen = "orders"
				return m.shopView(ctx, scope, session)
			}
			add(catalog.Text(language, "menu.shop.refresh"), menus.Action{Kind: "refresh"})
			add(catalog.Text(language, "menu.back"), menus.Action{Kind: "back"})
		} else {
			view.Text = append(view.Text, core.Span{Text: " • "}, core.Span{
				Text:  catalog.Text(language, "menu.shop.orders"),
				Style: core.Code,
			})
			start, end, pages := shopPage(session, len(orders))
			if len(orders) == 0 {
				view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, "menu.shop.no_orders")})
			}
			for i, order := range orders[start:end] {
				name := views.Preview(order.Quote.Snapshot.Text(language).Name, 128)
				state := views.Preview(catalog.Text(language, "menu.order."+string(order.State)), 160)
				prefix := "\n"
				if i == 0 {
					prefix = "\n\n"
				}
				view.Text = append(view.Text,
					core.Span{Text: fmt.Sprintf("%s%d. %s ", prefix, i+1, name)},
					core.Span{
						Text:  "(" + strconv.FormatInt(order.Quote.Quantity, 10) + ")",
						Style: core.Code,
					},
					core.Span{Text: "\n"},
					core.Span{
						Text:  state,
						Style: core.Italic,
					},
				)
				add(strconv.Itoa(i+1), menus.Action{
					Kind:  "order",
					Order: order.ID,
				})
			}
			shopPages(session, pages, add, catalog.Text(language, "menu.previous"), catalog.Text(language, "menu.next"))
			add(catalog.Text(language, "menu.back"), menus.Action{Kind: "back"})
		}
	} else {
		choices, err := m.shopChoices(ctx, scope, session.Character)
		if err != nil {
			return core.View{}, err
		}
		start, end, pages := shopPage(session, len(choices))
		if len(choices) == 0 {
			view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, "menu.shop.empty")})
		}
		for i, choice := range choices[start:end] {
			prefix := "\n"
			if i == 0 {
				prefix = "\n\n"
			}
			view.Text = append(view.Text, core.Span{Text: fmt.Sprintf("%s%d. %s", prefix, i+1, views.Preview(choice.snapshot.Text(language).Name, 128))})
			add(strconv.Itoa(i+1), menus.Action{
				Kind:  "select",
				Entry: choice.key,
			})
		}
		if m.bankID == 0 {
			view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, "menu.shop.unavailable")})
		}
		view.Text = append(view.Text,
			core.Span{Text: "\n\n" + catalog.Text(language, "menu.inventory.page") + " "},
			core.Span{
				Text:  fmt.Sprintf("%d/%d", session.Page+1, pages),
				Style: core.Code,
			},
		)
		shopPages(session, pages, add, catalog.Text(language, "menu.previous"), catalog.Text(language, "menu.next"))
		add(catalog.Text(language, "menu.shop.orders"), menus.Action{Kind: "orders"})
	}
	if m.bankID == 0 && session.Screen != "catalog" {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + views.Preview(catalog.Text(language, "menu.shop.unavailable"), 240)})
	}
	refs, err := m.sessions.Actions(session, actions)
	if err != nil {
		return core.View{}, err
	}
	for i, caption := range captions {
		button := core.Button{
			Caption: views.Preview(caption, 60),
			Data:    refs[i],
		}
		kind := actions[i].Kind
		groupedAction := false
		if i > 0 {
			previous := actions[i-1].Kind
			groupedAction = (previous == kind && (kind == "select" || kind == "order" || kind == "page" || kind == "quantity")) ||
				(previous == "buy" && kind == "sell") ||
				(previous == "back" && kind == "orders") ||
				(previous == "refresh" && kind == "back")
		}
		if groupedAction {
			last := len(view.Rows) - 1
			view.Rows[last] = append(view.Rows[last], button)
		} else {
			view.Rows = append(view.Rows, []core.Button{button})
		}
	}
	return view, nil
}

func shopPage(session *menus.Session, count int) (int, int, int) {
	pages := max(1, (count+views.PageSize-1)/views.PageSize)
	session.Page = max(0, min(session.Page, pages-1))
	start := session.Page * views.PageSize
	return start, min(start+views.PageSize, count), pages
}

func shopPages(
	session *menus.Session,
	pages int,
	add func(string, menus.Action),
	previous, next string,
) {
	if session.Page > 0 {
		add(previous, menus.Action{
			Kind: "page",
			Page: session.Page - 1,
		})
	}
	if session.Page+1 < pages {
		add(next, menus.Action{
			Kind: "page",
			Page: session.Page + 1,
		})
	}
}

func (m *modules) reviewQuote(ctx context.Context, scope core.Scope, session *menus.Session) (shop.Quote, int64, bool, error) {
	quote := shop.Quote{
		Group:        scope.Group,
		Player:       scope.Actor,
		Character:    session.Character,
		Quantity:     max(1, min(session.Quantity, 1000)),
		BankID:       m.bankID,
		TreasuryID:   m.treasuryID,
		DeliveryChat: int64(scope.Group),
	}
	available := int64(1000)
	if session.Screen == "buy-review" {
		quote.Direction = shop.Buy
		choice, err := m.selectedShopChoice(ctx, scope, session)
		if err != nil {
			return quote, 0, false, err
		}
		if choice.offer.BuyPrice == "" {
			return quote, 0, false, menus.ErrInvalid
		}
		quote.Snapshot = choice.snapshot
	} else {
		quote.Direction = shop.Sell
		quote.Entry = session.Entry
		entry, err := m.inventory.Inspect(ctx, scope.Group, session.Character, quote.Entry)
		if err != nil || entry.Available() <= 0 {
			return quote, 0, false, menus.ErrInvalid
		}
		quote.Snapshot = entry.Snapshot
		available = entry.Available()
		quote.Quantity = min(quote.Quantity, available)
	}
	if quote.Snapshot.Mode == items.Individual {
		quote.Quantity = 1
	}
	offer, found := m.bundle.Offer(quote.Snapshot.CatalogID, quote.Snapshot.Mode)
	if !found {
		return quote, 0, false, menus.ErrInvalid
	}
	quote.Unit = offer.BuyPrice
	if quote.Direction == shop.Sell {
		quote.Unit = offer.SellPrice
	}
	total, err := quote.Unit.Total(quote.Quantity)
	if err != nil {
		return quote, 0, false, err
	}
	quote.Total = total
	previous := m.quotes[session.Token]
	quote.ID = previous.ID
	changed := previous.ID != "" && quote != previous
	if previous.ID == "" || changed {
		quote.ID, err = core.NewID()
		if err != nil {
			return quote, 0, false, err
		}
	}
	m.quotes[session.Token] = quote
	session.Quantity = quote.Quantity
	return quote, available, changed, nil
}
