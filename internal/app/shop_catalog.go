package app

import (
	"cmp"
	"context"
	"slices"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/shop"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/alexthvest/replipg/internal/ui/views"
)

type shopChoice struct {
	key       core.ID
	snapshot  items.Snapshot
	offer     shop.Offer
	entry     core.ID
	owned     int64
	reserved  int64
	available int64
}

// Snapshot identities keep selection stable across list reordering and preserve
// separate sellable versions when an owned item differs from today's catalog.
func (m *modules) shopChoices(ctx context.Context, scope core.Scope, character core.ID) ([]shopChoice, error) {
	var choices []shopChoice
	index := map[items.Snapshot]int{}
	appendChoice := func(snapshot items.Snapshot, offer shop.Offer) (int, error) {
		data, err := snapshot.Canonical()
		if err != nil {
			return 0, err
		}
		position := len(choices)
		choices = append(choices, shopChoice{
			key:      core.ID(items.Digest(data)),
			snapshot: snapshot,
			offer:    offer,
		})
		index[snapshot] = position
		return position, nil
	}
	for _, offer := range m.bundle.Offers() {
		if offer.BuyPrice == "" {
			continue
		}
		snapshot, err := m.bundle.Capture(offer.CatalogID)
		if err != nil {
			return nil, err
		}
		if _, err = appendChoice(snapshot, offer); err != nil {
			return nil, err
		}
	}

	entries, err := m.inventory.List(ctx, scope.Group, character)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		offer, found := m.bundle.Offer(entry.Snapshot.CatalogID, entry.Snapshot.Mode)
		if !found || offer.SellPrice == "" {
			continue
		}
		position, exists := index[entry.Snapshot]
		if !exists {
			// A retired saved version is eligible for sale, not a new purchase.
			offer.BuyPrice = ""
			position, err = appendChoice(entry.Snapshot, offer)
			if err != nil {
				return nil, err
			}
		}
		choice := &choices[position]
		if entry.Available() > 0 && choice.available == 0 {
			choice.entry = entry.ID
		}
		choice.owned += entry.Quantity
		choice.reserved += entry.Reserved
		choice.available += entry.Available()
	}
	slices.SortStableFunc(choices, func(a, b shopChoice) int {
		return cmp.Compare(a.snapshot.CatalogID, b.snapshot.CatalogID)
	})
	return choices, nil
}

func (m *modules) selectedShopChoice(ctx context.Context, scope core.Scope, session *menus.Session) (shopChoice, error) {
	choices, err := m.shopChoices(ctx, scope, session.Character)
	if err != nil {
		return shopChoice{}, err
	}
	for _, choice := range choices {
		if choice.key == session.Selection {
			return choice, nil
		}
	}
	return shopChoice{}, menus.ErrInvalid
}

func (m *modules) shopItemView(
	ctx context.Context,
	scope core.Scope,
	session *menus.Session,
	language string,
	add func(string, menus.Action),
) (core.View, error) {
	choice, err := m.selectedShopChoice(ctx, scope, session)
	if err != nil {
		return core.View{}, err
	}
	catalog := m.bundle.Locales()
	text := choice.snapshot.Text(language)
	view := core.View{Text: core.Text{
		{
			Text:  text.Name,
			Style: core.Bold,
		},
		{Text: "\n\n"},
		{
			Text:  text.Description,
			Style: core.Italic,
		},
	}}
	pricePrefix := "\n\n"
	for _, direction := range []struct {
		kind  string
		price shop.Amount
	}{
		{
			kind:  "buy",
			price: choice.offer.BuyPrice,
		},
		{
			kind:  "sell",
			price: choice.offer.SellPrice,
		},
	} {
		if direction.price == "" {
			continue
		}
		view.Text = append(view.Text,
			core.Span{Text: pricePrefix + catalog.Text(language, "menu.shop."+direction.kind) + ": "},
			core.Span{
				Text:  string(direction.price) + "∅",
				Style: core.Code,
			},
		)
		pricePrefix = "\n"
		if direction.kind == "buy" || choice.available > 0 {
			add(catalog.Text(language, "menu.shop."+direction.kind), menus.Action{
				Kind:  direction.kind,
				Entry: choice.entry,
			})
		}
	}
	if choice.offer.SellPrice != "" {
		view.Text = append(view.Text, views.ShopAvailability(catalog, language, choice.owned, choice.reserved, choice.available)...)
	}
	add(catalog.Text(language, "menu.back"), menus.Action{Kind: "back"})
	add(catalog.Text(language, "menu.shop.orders"), menus.Action{Kind: "orders"})
	return view, nil
}

// The maximum is bounded by quantity/ownership and the bank's total digit limit.
func shopQuantityLimit(unit shop.Amount, limit int64) int64 {
	lower := int64(1)
	for lower < limit {
		middle := lower + (limit-lower+1)/2
		if _, err := unit.Total(middle); err != nil {
			limit = middle - 1
		} else {
			lower = middle
		}
	}
	return lower
}
