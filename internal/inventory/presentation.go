package inventory

import (
	"context"
	"errors"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type Presentation struct {
	Catalog *localization.Catalog
	Resolve func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error)
}

func (p Presentation) validate() error {
	if p.Catalog == nil || p.Resolve == nil {
		return errors.New("inventory: missing presentation dependency")
	}
	return nil
}
