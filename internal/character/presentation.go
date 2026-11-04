package character

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	sharingcontract "github.com/alexthvest/replipg/internal/sharing/contract"
)

type Presentation struct {
	Catalog *localization.Catalog
	Resolve func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error)
	Sharing sharingcontract.Decorator
}

func (p Presentation) validate() error {
	if p.Catalog == nil || p.Resolve == nil {
		return errors.New("character: missing presentation dependency")
	}
	return nil
}
