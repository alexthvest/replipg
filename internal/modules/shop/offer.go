package shop

import (
	"fmt"
	"strings"

	"github.com/alexthvest/replipg/internal/modules/items"
)

type Target struct {
	CatalogID string
	Mode      items.Mode
}

type Offer struct {
	Target
	BuyPrice  Amount
	SellPrice Amount
}

func (o Offer) Validate() error {
	if strings.TrimSpace(o.CatalogID) == "" || len(o.CatalogID) > 128 {
		return fmt.Errorf("invalid offer catalog ID %q", o.CatalogID)
	}

	if o.Mode != items.Stackable && o.Mode != items.Individual {
		return fmt.Errorf("offer %s: explicit ownership mode required", o.CatalogID)
	}

	if o.BuyPrice == "" && o.SellPrice == "" {
		return fmt.Errorf("offer %s: at least one direction required", o.CatalogID)
	}

	for _, price := range []Amount{o.BuyPrice, o.SellPrice} {
		if price != "" {
			if err := price.Validate(); err != nil {
				return fmt.Errorf("offer %s: %w", o.CatalogID, err)
			}
		}
	}

	if o.BuyPrice != "" && o.SellPrice != "" && !o.SellPrice.less(o.BuyPrice) {
		return fmt.Errorf("offer %s: sell price must be below buy price", o.CatalogID)
	}
	return nil
}

// Index rejects duplicate targets rather than silently replacing a policy.
func Index(offers []Offer) (map[Target]Offer, error) {
	index := make(map[Target]Offer, len(offers))
	for _, offer := range offers {
		if err := offer.Validate(); err != nil {
			return nil, err
		}

		if _, exists := index[offer.Target]; exists {
			return nil, fmt.Errorf("duplicate offer target %s/%s", offer.CatalogID, offer.Mode)
		}
		index[offer.Target] = offer
	}
	return index, nil
}
