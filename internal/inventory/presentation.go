package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	sharingcontract "github.com/alexthvest/replipg/internal/sharing/contract"
)

type Presentation struct {
	Catalog *localization.Catalog
	Resolve func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error)
	Sharing sharingcontract.Decorator
}

func (p *Presentation) decorate(ctx context.Context, tx runtimecontract.DBTX, now time.Time, identity interaction.Identity, locale localization.Locale, kind sharingcontract.ViewKind, editMessageID int64, body runtimecontract.MessageContent) (runtimecontract.MessageContent, error) {
	if p == nil || p.Sharing == nil {
		return body, nil
	}
	// Controls persist their deadline in UTC milliseconds; inherit that exact value.
	usableUntil := time.UnixMilli(now.UTC().Add(controlLifetime).UnixMilli()).UTC()
	return p.Sharing.Decorate(ctx, tx, sharingcontract.View{
		Owner:       identity.User,
		Destination: runtimecontract.GroupDestination{ChatID: identity.Chat, Kind: identity.ChatKind},
		Kind:        kind, Locale: locale, Body: body, EditMessageID: editMessageID,
		UsableUntil: usableUntil,
	})
}

func (p Presentation) validate() error {
	if p.Catalog == nil || p.Resolve == nil {
		return errors.New("inventory: missing presentation dependency")
	}
	return nil
}
