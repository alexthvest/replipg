package sharing

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// LocaleResolver reads the group's current locale inside the projection transaction.
type LocaleResolver func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error)

// Projector reconciles durable delivery evidence without performing network work.
// Its caller owns worker supervision and transaction boundaries for Refresh.
type Projector struct {
	tx      runtimecontract.Transactor
	ledger  DeliveryLedger
	catalog *localization.Catalog
	resolve LocaleResolver
	now     func() time.Time
}

// NewProjector constructs the independently supervised sharing projection worker.
func NewProjector(tx runtimecontract.Transactor, ledger DeliveryLedger, catalog *localization.Catalog, resolve LocaleResolver, now func() time.Time) (*Projector, error) {
	if tx == nil || ledger == nil || catalog == nil || resolve == nil || now == nil {
		return nil, errors.New("sharing: missing projector dependency")
	}

	return &Projector{tx: tx, ledger: ledger, catalog: catalog, resolve: resolve, now: now}, nil
}

// Refresh projects outstanding actions before a callback evaluates view eligibility,
// even when the worker's next scheduled inspection has not arrived yet.
func (p *Projector) Refresh(ctx context.Context, tx runtimecontract.DBTX, token string) error {
	actions, err := actionsForView(ctx, tx, token)
	if err != nil {
		return err
	}

	now := p.now().UTC()
	for _, action := range actions {
		if err := p.project(ctx, tx, action, now); err != nil {
			return err
		}
	}
	return nil
}

// RunOnce processes at most 100 due actions in one short atomic transaction.
func (p *Projector) RunOnce(ctx context.Context) (bool, error) {
	worked := false
	err := p.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := p.now().UTC()
		actions, err := dueActions(ctx, tx, now, 100)
		if err != nil {
			return err
		}

		for _, action := range actions {
			if err := p.project(ctx, tx, action, now); err != nil {
				return err
			}
		}

		worked = len(actions) > 0
		return retireExpiredViews(ctx, tx, now, 100)
	})
	return worked && err == nil, err
}

func (p *Projector) project(ctx context.Context, tx runtimecontract.DBTX, action actionRecord, now time.Time) error {
	feedbackWasQueued := action.FeedbackState == "queued"
	if action.PublicState == "pending" {
		if err := p.projectPublic(ctx, tx, action, now); err != nil {
			return err
		}

		// Re-read the revision after the public projection's fenced update.
		var err error

		action, err = readAction(ctx, tx, action.UpdateID)
		if err != nil {
			return err
		}

		if action.PublicState == "pending" {
			return nil
		}
	}
	if feedbackWasQueued {
		evidence, err := p.ledger.Inspect(ctx, tx, action.FeedbackDeliveryID)
		if err != nil {
			return err
		}

		switch evidence.State {
		case Pending:
		case Completed, Rejected:
			if evidence.TerminalAt.IsZero() {
				return errors.New("sharing: missing terminal feedback evidence")
			}

			action.FeedbackState = string(evidence.State)
			action.FeedbackOutcomeAt = evidence.TerminalAt
		case Unknown:
			if evidence.UpdatedAt.IsZero() {
				return errors.New("sharing: missing uncertainty feedback evidence")
			}

			action.FeedbackState = "unknown"
			action.FeedbackOutcomeAt = evidence.UpdatedAt
		default:
			return errors.New("sharing: invalid feedback evidence")
		}

		if action.FeedbackState != "queued" {
			action.FeedbackPurgeAfter = retentionDeadline(now, action.FeedbackOutcomeAt)
		}
	}
	if action.FeedbackState == "skipped" {
		action.FeedbackPurged = true
	}
	if !action.PublicPurged && !now.Before(action.PublicPurgeAfter) {
		purged, err := p.ledger.Purge(ctx, tx, PublicKind, action.PublicDeliveryID, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}

		action.PublicPurged = purged
	}
	if !action.FeedbackPurged && !action.FeedbackPurgeAfter.IsZero() && !now.Before(action.FeedbackPurgeAfter) {
		purged, err := p.ledger.Purge(ctx, tx, FeedbackKind, action.FeedbackDeliveryID, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}

		action.FeedbackPurged = purged
	}
	// Choose the earliest remaining deadline. Ineligible purges and queued
	// feedback are revisited after one second without delaying other cleanup.
	action.NextCheckAt = time.Time{}
	schedule := func(deadline time.Time) {
		if !deadline.After(now) {
			deadline = now.Add(time.Second)
		}
		if action.NextCheckAt.IsZero() || deadline.Before(action.NextCheckAt) {
			action.NextCheckAt = deadline
		}
	}
	if !action.PublicPurged {
		schedule(action.PublicPurgeAfter)
	}
	if !action.FeedbackPurged {
		if action.FeedbackState == "queued" {
			schedule(now.Add(time.Second))
		} else {
			schedule(action.FeedbackPurgeAfter)
		}
	}
	return updateAction(ctx, tx, action)
}

func retentionDeadline(now, outcome time.Time) time.Time {
	if outcome.After(now) {
		now = outcome
	}
	return now.Add(24 * time.Hour)
}

func (p *Projector) projectPublic(ctx context.Context, tx runtimecontract.DBTX, action actionRecord, now time.Time) error {
	if action.PublicState != "pending" {
		return nil
	}

	evidence, err := p.ledger.Inspect(ctx, tx, action.PublicDeliveryID)
	if err != nil {
		return err
	}

	action.NextCheckAt = now.Add(time.Second)
	if evidence.State == Pending {
		return updateAction(ctx, tx, action)
	}

	view, err := readView(ctx, tx, action.ViewToken)
	if err != nil {
		return err
	}

	var key localization.Key

	switch evidence.State {
	case Completed:
		if evidence.TerminalAt.IsZero() {
			return errors.New("sharing: missing terminal evidence")
		}

		action.PublicState = "succeeded"
		action.PublicOutcomeAt = evidence.TerminalAt
		// Old evidence cannot shorten a more recent successful-share cooldown.
		if evidence.TerminalAt.After(view.LastSuccessAt) {
			view.LastSuccessAt = evidence.TerminalAt
		}
	case Rejected:
		if evidence.TerminalAt.IsZero() {
			return errors.New("sharing: missing terminal evidence")
		}

		action.PublicState = "failed"
		action.PublicOutcomeAt = evidence.TerminalAt
		key = localization.SharingFailure
	case Unknown:
		if evidence.UpdatedAt.IsZero() {
			return errors.New("sharing: missing uncertainty evidence")
		}

		action.PublicState = "unknown"
		action.PublicOutcomeAt = evidence.UpdatedAt
		until := evidence.UpdatedAt.Add(30 * time.Second)
		if until.After(view.UnknownUntil) {
			view.UnknownUntil = until
		}
		key = localization.SharingUnknown
	default:
		return errors.New("sharing: invalid delivery evidence")
	}

	action.PublicPurgeAfter = retentionDeadline(now, action.PublicOutcomeAt)
	if err := updateView(ctx, tx, view); err != nil {
		return err
	}

	if err := p.queueFeedback(ctx, tx, view, &action, key, now); err != nil {
		return err
	}

	if action.FeedbackState == "skipped" {
		action.NextCheckAt = action.PublicPurgeAfter
	}
	return updateAction(ctx, tx, action)
}

func (p *Projector) queueFeedback(ctx context.Context, tx runtimecontract.DBTX, view viewRecord, action *actionRecord, key localization.Key, now time.Time) error {
	if action.FeedbackState != "waiting" {
		return nil
	}

	if key == "" || action.CallbackQueryID == "" || !now.Before(action.CallbackDeadline) {
		action.FeedbackState = "skipped"
	} else {
		locale, err := p.resolve(ctx, tx, view.Destination.ChatID)
		if err != nil {
			return err
		}

		text, err := p.catalog.Text(locale, key, nil)
		if err != nil {
			return err
		}

		request, err := NewFeedbackRequest(FeedbackPayload{
			ActionUpdateID: action.UpdateID, Destination: view.Destination, TargetUserID: view.Owner,
			CallbackQueryID: action.CallbackQueryID, ValidUntilMS: action.CallbackDeadline.UnixMilli(),
			Content: runtimecontract.MessageContent{Text: text},
		})
		if err != nil {
			return err
		}

		id, err := p.ledger.Enqueue(ctx, tx, request)
		if err != nil {
			return err
		}

		action.FeedbackDeliveryID = id
		action.FeedbackState = "queued"
	}
	action.CallbackQueryID = ""
	action.CallbackDeadline = time.Time{}
	return nil
}
