package shop

import (
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/modules/items"
)

func TestAmountsAndTotals(t *testing.T) {
	for _, value := range []Amount{"", "0", "01", "-1", "+1", "1.5", " 1", "١", Amount(strings.Repeat("9", 101))} {
		if err := value.Validate(); err == nil {
			t.Errorf("accepted amount %q", value)
		}
	}

	for _, quantity := range []int64{-1, 0, 1001} {
		if _, err := Amount("1").Total(quantity); err == nil {
			t.Errorf("accepted quantity %d", quantity)
		}
	}

	for _, quantity := range []int64{1, 1000} {
		if _, err := Amount("1").Total(quantity); err != nil {
			t.Fatal(err)
		}
	}

	maximum := Amount(strings.Repeat("9", 100))
	if total, err := maximum.Total(1); err != nil || total != maximum {
		t.Fatalf("100 digit boundary: %s %v", total, err)
	}
	if _, err := maximum.Total(2); err == nil {
		t.Fatal("accepted overflowing total")
	}

	if total, err := Amount("9007199254740993").Total(3); err != nil || total != "27021597764222979" {
		t.Fatalf("exact multiplication: %s %v", total, err)
	}
}

func TestOfferDirectionsOrderingAndDuplicates(t *testing.T) {
	target := Target{
		CatalogID: "bread",
		Mode:      items.Stackable,
	}
	for _, prices := range []struct {
		buy   Amount
		sell  Amount
		valid bool
	}{
		{
			buy:   "10",
			valid: true,
		},
		{
			sell:  "5",
			valid: true,
		},
		{
			buy:   "10",
			sell:  "9",
			valid: true,
		},
		{
			buy:   "100",
			sell:  "99",
			valid: true,
		},
		{
			buy:  "9",
			sell: "10",
		},
		{
			buy:  "10",
			sell: "10",
		},
		{buy: "0"},
		{},
	} {
		offer := Offer{
			Target:    target,
			BuyPrice:  prices.buy,
			SellPrice: prices.sell,
		}
		if err := offer.Validate(); (err == nil) != prices.valid {
			t.Errorf("offer %+v: %v", offer, err)
		}
	}

	offer := Offer{
		Target:   target,
		BuyPrice: "10",
	}
	if _, err := Index([]Offer{offer, offer}); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if index, err := Index(nil); err != nil || len(index) != 0 {
		t.Fatalf("empty offers: %v", err)
	}
}

func TestQuoteFrozenTerms(t *testing.T) {
	quote := Quote{
		ID:        "quote",
		Group:     -1,
		Player:    1,
		Character: "character",
		Direction: Buy,
		Snapshot: items.Snapshot{
			Version:   1,
			CatalogID: "bread",
			Mode:      items.Stackable,
			Rarity:    "common",
			English: items.Text{
				Name:        "Bread",
				Description: "Food",
			},
			Russian: items.Text{
				Name:        "Хлеб",
				Description: "Еда",
			},
		},
		Quantity:     2,
		Unit:         "10",
		Total:        "20",
		BankID:       5,
		TreasuryID:   2,
		DeliveryChat: -1,
	}
	if err := quote.Validate(); err != nil {
		t.Fatal(err)
	}

	changed := quote
	changed.Total = "21"
	if err := changed.Validate(); err == nil {
		t.Fatal("inconsistent total accepted")
	}
	changed = quote
	changed.DeliveryChat = -2
	if err := changed.Validate(); err == nil {
		t.Fatal("changed delivery group accepted")
	}
	changed = quote
	changed.Direction = Sell
	if err := changed.Validate(); err == nil {
		t.Fatal("sale without an entry accepted")
	}
}
