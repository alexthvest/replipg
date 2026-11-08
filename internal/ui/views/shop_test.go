package views

import (
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/shop"
)

func TestShopTermsAndOrderOutcomesUseExactLiteralValues(t *testing.T) {
	bundle, err := cueassets.Load("../../../assets")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Capture("bread")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.English.Name = "[<&` Bread]"
	quote := shop.Quote{
		Direction: shop.Sell,
		Snapshot:  snapshot,
		Quantity:  3,
		Unit:      "9007199254740993",
		Total:     "27021597764222979",
	}
	for _, language := range []string{"en", "ru"} {
		view := ShopTerms(bundle.Locales(), language, quote, 8)
		var total string
		styledTotal, styledDirection, styledDescription := false, false, false
		for _, span := range view.Text {
			total += span.Text
			styledTotal = styledTotal || (span.Text == string(quote.Total)+"∅" && span.Style == core.Bold)
			styledDirection = styledDirection || (span.Text == bundle.Locales().Text(language, "menu.shop.sell") && span.Style == core.Code)
			styledDescription = styledDescription || (span.Text == quote.Snapshot.Text(language).Description && span.Style == core.Italic)
		}
		if !styledTotal || !styledDirection || !styledDescription || view.Text[0].Style != core.Bold {
			t.Fatal("review lost the reference title, context, description, or total styling")
		}
		if !strings.Contains(total, string(quote.Total)) || !strings.Contains(total, string(quote.Unit)) {
			t.Fatal("exact decimal values lost")
		}
		for _, state := range []shop.State{shop.Created, shop.Requesting, shop.AwaitingPayment, shop.Paid, shop.Reserved, shop.PayoutRequesting, shop.PayoutConfirmed, shop.Completed, shop.Rejected, shop.Failed, shop.ReconciliationRequired} {
			order := ShopOrder(bundle.Locales(), language, shop.Order{
				ID:    "order",
				Quote: quote,
				State: state,
			})
			if len(order.Text) == 0 || bundle.Locales().Text(language, "menu.order."+string(state)) == "" {
				t.Fatalf("missing localized order outcome %s/%s", language, state)
			}
		}
	}
}
