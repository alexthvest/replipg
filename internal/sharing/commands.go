package sharing

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// RefreshFunc refreshes durable evidence in the callback transaction. Composition
// binds it to the supervised projector before admitting callbacks.
type RefreshFunc func(context.Context, runtimecontract.DBTX, string) error

// Register registers the authenticated Share callback route.
func Register(registry *interaction.Registry, service *Service, ledger DeliveryLedger, refresh RefreshFunc, resolve LocaleResolver) error {
	if registry == nil || service == nil || ledger == nil || refresh == nil || resolve == nil {
		return errors.New("sharing: missing callback dependency")
	}

	return registry.RegisterCallback(sharePrefix, shareHandler{service: service, ledger: ledger, refresh: refresh, resolve: resolve})
}

type shareHandler struct {
	service *Service
	ledger  DeliveryLedger
	refresh RefreshFunc
	resolve LocaleResolver
}

func (h shareHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	cb, ok := event.(interaction.Callback)
	if pointer, isPointer := event.(*interaction.Callback); isPointer && pointer != nil {
		cb = *pointer
		ok = true
	}
	if !ok || cb.Update <= 0 {
		return interaction.HandlerResult{}, nil
	}

	tx := scope.DBTX()
	// Accepted identities outlive delivery content, source usability and cooldowns.
	if _, err := readAction(ctx, tx, cb.Update); err == nil {
		return interaction.HandlerResult{}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return interaction.HandlerResult{}, err
	}
	now := h.service.now().UTC()
	view, body, decision, err := verifySource(ctx, tx, cb, now)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	switch decision {
	case sourceInvalid:
		return interaction.HandlerResult{}, nil
	case sourceExpired:
		return h.guidance(ctx, tx, cb, now.Add(15*time.Second), localization.SharingReopen, nil)
	case sourceUnavailable:
		return h.guidance(ctx, tx, cb, now.Add(15*time.Second), localization.SharingReopen, nil)
	}

	// Refresh can change revision, cooldown and reservation state. Always reread.
	if err = h.refresh(ctx, tx, view.Token); err != nil {
		return interaction.HandlerResult{}, err
	}

	view, err = readView(ctx, tx, view.Token)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	deadline := now.Add(15 * time.Second)
	if !view.UsableUntil.IsZero() && view.UsableUntil.Before(deadline) {
		deadline = view.UsableUntil
	}
	var pending bool

	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sharing_actions WHERE view_token=? AND public_state='pending')`, view.Token).Scan(&pending); err != nil {
		return interaction.HandlerResult{}, storeFailure("read reservation", err)
	}

	if pending {
		return h.guidance(ctx, tx, cb, deadline, localization.SharingPending, nil)
	}

	if !view.LastSuccessAt.IsZero() {
		until := view.LastSuccessAt.Add(30 * time.Second)
		if now.Before(until) {
			seconds := int64((until.Sub(now) + time.Second - 1) / time.Second)
			return h.guidance(ctx, tx, cb, deadline, localization.SharingCooldown, localization.Args{"seconds": localization.Integer(seconds)})
		}
	}
	if now.Before(view.UnknownUntil) {
		return h.guidance(ctx, tx, cb, deadline, localization.SharingUnknown, nil)
	}

	attribution, err := Attribution(h.service.catalog, view.Locale, view.Owner)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	body.Text += "\n\n" + attribution
	request, err := NewPublicRequest(PublicPayload{ActionUpdateID: cb.Update, Destination: view.Destination, Content: body})
	if err != nil {
		return h.guidance(ctx, tx, cb, deadline, localization.SharingUnavailable, nil)
	}

	// Rendering guidance before enqueue means all fallible preparation is complete;
	// the caller still owns atomic commit of the ledger and acceptance identity.
	key := localization.SharingPending
	if !view.UnknownUntil.IsZero() {
		// The hold timestamp remains fixed historical evidence. Warn only when
		// the latest deliberate attempt is uncertain, rather than carrying an
		// old warning through subsequent confirmed outcomes.
		var latestState string

		err = tx.QueryRowContext(ctx, `SELECT public_state FROM sharing_actions WHERE view_token=? ORDER BY accepted_at DESC,update_id DESC LIMIT 1`, view.Token).Scan(&latestState)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return interaction.HandlerResult{}, storeFailure("read latest outcome", err)
		}

		if latestState == "unknown" {
			key = localization.SharingUnknownRetry
		}
	}
	result, err := h.guidance(ctx, tx, cb, deadline, key, nil)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	id, err := h.ledger.Enqueue(ctx, tx, request)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	err = insertAction(ctx, tx, actionRecord{UpdateID: cb.Update, ViewToken: view.Token, PublicDeliveryID: id, AcceptedAt: now, NextCheckAt: now, CallbackQueryID: string(cb.QueryID), CallbackDeadline: deadline})
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	return result, nil
}

func (h shareHandler) guidance(ctx context.Context, tx runtimecontract.DBTX, cb interaction.Callback, deadline time.Time, key localization.Key, args localization.Args) (interaction.HandlerResult, error) {
	locale, err := h.resolve(ctx, tx, cb.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	text, err := h.service.catalog.Text(locale, key, args)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: cb.Chat, Kind: cb.ChatKind}, TargetUserID: cb.User, Authorization: runtimecontract.CallbackAuthorization{CallbackQueryID: string(cb.QueryID), OriginatedFromEphemeral: true, ValidUntil: deadline}, Content: runtimecontract.MessageContent{Text: text}}
	if err = runtimecontract.ValidateResponse(response); err != nil {
		return interaction.HandlerResult{}, err
	}

	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response}}, nil
}
