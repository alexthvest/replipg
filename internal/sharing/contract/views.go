// Package contract defines the injected presentation boundary for menu sharing.
package contract

import (
	"context"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// ViewKind identifies a supported personal production view.
type ViewKind string

const (
	CharacterSummary ViewKind = "character_summary"
	InventoryPage    ViewKind = "inventory_page"
	ItemDetails      ViewKind = "item_details"
)

// View contains a final localized body and its authenticated menu identity.
// EditMessageID is the known source edit target, or zero for a new send.
// UsableUntil inherits the existing menu deadline; zero means no deadline.
type View struct {
	Owner         runtimecontract.UserID
	Destination   runtimecontract.GroupDestination
	Kind          ViewKind
	Locale        localization.Locale
	Body          runtimecontract.MessageContent
	EditMessageID int64
	UsableUntil   time.Time
}

// Decorator appends a Share row to cloned content without changing its body or
// existing controls. Each call records a distinct view in the caller's transaction;
// the result remains tentative until that transaction commits.
type Decorator interface {
	Decorate(context.Context, runtimecontract.DBTX, View) (runtimecontract.MessageContent, error)
}
