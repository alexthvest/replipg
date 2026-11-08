package views

import (
	"strconv"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/localization"
	"github.com/alexthvest/replipg/internal/modules/shop"
)

func ShopTitle(catalog localization.Catalog, language, key string) core.View {
	return core.View{Text: core.Text{{
		Text:  Preview(catalog.Text(language, key), 100),
		Style: core.Bold,
	}}}
}

func ShopTerms(catalog localization.Catalog, language string, quote shop.Quote, available int64) core.View {
	view := core.View{Text: core.Text{
		{
			Text:  quote.Snapshot.Text(language).Name,
			Style: core.Bold,
		},
		{Text: " • "},
		{
			Text:  Preview(catalog.Text(language, "menu.shop."+string(quote.Direction)), 80),
			Style: core.Code,
		},
		{Text: "\n\n"},
		{
			Text:  quote.Snapshot.Text(language).Description,
			Style: core.Italic,
		},
	}}
	type field struct {
		key   string
		value string
		style core.Style
	}
	var fields []field
	if quote.Snapshot.Mode == items.Stackable {
		fields = append(fields, field{
			key:   "menu.shop.quantity",
			value: strconv.FormatInt(quote.Quantity, 10),
			style: core.Code,
		})
	}
	fields = append(fields,
		field{
			key:   "menu.shop.unit",
			value: string(quote.Unit) + "∅",
			style: core.Code,
		},
		field{
			key:   "menu.shop.total",
			value: string(quote.Total) + "∅",
			style: core.Bold,
		},
	)
	for i, field := range fields {
		prefix := "\n"
		if i == 0 {
			prefix = "\n\n"
		}
		view.Text = append(view.Text,
			core.Span{Text: prefix + Preview(catalog.Text(language, field.key), 80) + ": "},
			core.Span{
				Text:  field.value,
				Style: field.style,
			},
		)
	}
	if quote.Direction == shop.Sell && available >= 0 {
		view.Text = append(view.Text,
			core.Span{Text: "\n\n" + catalog.Text(language, "menu.shop.available") + ": "},
			core.Span{
				Text:  strconv.FormatInt(available, 10),
				Style: core.Bold,
			},
		)
	}
	return view
}

func ShopOrder(catalog localization.Catalog, language string, order shop.Order) core.View {
	view := ShopTitle(catalog, language, "menu.order.title")
	view.Text = append(view.Text,
		core.Span{Text: " • "},
		core.Span{
			Text:  catalog.Text(language, "menu.shop."+string(order.Quote.Direction)),
			Style: core.Code,
		},
		core.Span{Text: "\n"},
		core.Span{
			Text:  string(order.ID),
			Style: core.Code,
		},
		core.Span{Text: "\n\n"},
		core.Span{
			Text:  order.Quote.Snapshot.Text(language).Name,
			Style: core.Bold,
		},
		core.Span{Text: " × "},
		core.Span{
			Text:  strconv.FormatInt(order.Quote.Quantity, 10),
			Style: core.Code,
		},
		core.Span{Text: "\n" + catalog.Text(language, "menu.shop.unit") + ": "},
		core.Span{
			Text:  string(order.Quote.Unit) + "∅",
			Style: core.Code,
		},
		core.Span{Text: "\n" + catalog.Text(language, "menu.shop.total") + ": "},
		core.Span{
			Text:  string(order.Quote.Total) + "∅",
			Style: core.Bold,
		},
		core.Span{Text: "\n" + catalog.Text(language, "menu.order.status") + ": "},
		core.Span{
			Text:  Preview(catalog.Text(language, "menu.order."+string(order.State)), 240),
			Style: core.Italic,
		},
	)
	notice := ""
	switch order.State {
	case shop.ReconciliationRequired:
		notice = "menu.shop.unresolved_payment"
		if order.Quote.Direction == shop.Sell {
			notice = "menu.shop.unresolved_hold"
		}
	case shop.Failed:
		notice = "menu.shop.account_help"
		if order.Quote.Direction == shop.Buy && order.Diagnostic == "insufficient_funds" {
			notice = "menu.shop.buyer_funds"
		}
	case shop.Rejected:
		notice = "menu.shop.buyer_refused"
	}
	if notice != "" {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, notice)})
	}
	return view
}

func ShopAvailability(catalog localization.Catalog, language string, owned, reserved, available int64) core.Text {
	return core.Text{
		{Text: "\n\n" + catalog.Text(language, "menu.shop.owned") + ": "},
		{
			Text:  strconv.FormatInt(owned, 10),
			Style: core.Code,
		},
		{Text: "\n" + catalog.Text(language, "menu.shop.reserved") + ": "},
		{
			Text:  strconv.FormatInt(reserved, 10),
			Style: core.Code,
		},
		{Text: "\n" + catalog.Text(language, "menu.shop.sale_available") + ": "},
		{
			Text:  strconv.FormatInt(available, 10),
			Style: core.Bold,
		},
	}
}
