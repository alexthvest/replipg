package sharing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

// ServiceOptions supplies deterministic clock and token generation boundaries.
// NewToken returns exactly 32 lowercase hexadecimal characters, without a prefix.
type ServiceOptions struct {
	Now      func() time.Time
	NewToken func() (string, error)
}

// Service decorates authenticated final personal views without reading gameplay.
type Service struct {
	catalog  *localization.Catalog
	now      func() time.Time
	newToken func() (string, error)
}

// NewService constructs a sharing presentation service.
func NewService(catalog *localization.Catalog, options ServiceOptions) (*Service, error) {
	if catalog == nil {
		return nil, errors.New("sharing: catalog required")
	}

	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewToken == nil {
		options.NewToken = randomToken
	}
	return &Service{catalog: catalog, now: options.Now, newToken: options.NewToken}, nil
}

func randomToken() (string, error) {
	var token [16]byte

	if _, err := rand.Read(token[:]); err != nil {
		return "", errors.New("sharing: token generation failed")
	}

	return hex.EncodeToString(token[:]), nil
}

// Decorate appends one Share row and stores only capability metadata and a hash
// inside the caller's transaction. The caller retains ownership of its content.
func (s *Service) Decorate(ctx context.Context, tx runtimecontract.DBTX, v contract.View) (runtimecontract.MessageContent, error) {
	if tx == nil || v.Owner <= 0 || v.EditMessageID < 0 {
		return runtimecontract.MessageContent{}, errors.New("sharing: invalid view")
	}

	switch v.Kind {
	case contract.CharacterSummary:
		if !v.UsableUntil.IsZero() {
			return runtimecontract.MessageContent{}, errors.New("sharing: character deadline unsupported")
		}
	case contract.InventoryPage, contract.ItemDetails:
		if v.UsableUntil.IsZero() {
			return runtimecontract.MessageContent{}, errors.New("sharing: inventory deadline required")
		}
	default:
		return runtimecontract.MessageContent{}, errors.New("sharing: unsupported view kind")
	}

	if _, err := localization.ParseLocale(string(v.Locale)); err != nil {
		return runtimecontract.MessageContent{}, err
	}

	if err := runtimecontract.ValidateResponse(runtimecontract.PublicResponse{Destination: v.Destination, Content: v.Body}); err != nil {
		return runtimecontract.MessageContent{}, err
	}

	digest, err := fingerprint(v.Body)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	token, err := s.newToken()
	if err != nil {
		return runtimecontract.MessageContent{}, errors.New("sharing: token generation failed")
	}

	if _, ok := parseShareToken(sharePrefix + token); !ok {
		return runtimecontract.MessageContent{}, errors.New("sharing: invalid generated token")
	}

	label, err := s.catalog.Text(v.Locale, localization.SharingShare, nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	body := cloneContent(v.Body)
	body.Buttons = append(body.Buttons, []runtimecontract.InlineButton{{Text: label, CallbackData: sharePrefix + token, Style: runtimecontract.ButtonPrimary}})
	if err = runtimecontract.ValidateResponse(runtimecontract.PublicResponse{Destination: v.Destination, Content: body}); err != nil {
		return runtimecontract.MessageContent{}, err
	}

	err = insertView(ctx, tx, viewRecord{Token: token, Owner: v.Owner, Destination: v.Destination, Kind: v.Kind, Locale: v.Locale, FingerprintVersion: 1, BodyHash: digest, BoundMessageID: v.EditMessageID, UsableUntil: v.UsableUntil, CreatedAt: s.now().UTC()})
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	return body, nil
}

func cloneContent(body runtimecontract.MessageContent) runtimecontract.MessageContent {
	body.Entities = append([]runtimecontract.TextEntity(nil), body.Entities...)
	if body.Buttons != nil {
		rows := make([][]runtimecontract.InlineButton, len(body.Buttons))
		for i, row := range body.Buttons {
			rows[i] = append([]runtimecontract.InlineButton(nil), row...)
		}

		body.Buttons = rows
	}
	return body
}
