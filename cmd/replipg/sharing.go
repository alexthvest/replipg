package main

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing"
)

// sharingLedger confines feature work to the two registered sharing schemas.
// All operations use the caller's transaction, including outcome inspection.
type sharingLedger struct{ store *outbox.Store }

func newSharingLedger(store *outbox.Store) (*sharingLedger, error) {
	if store == nil {
		return nil, errors.New("sharing: outbox required")
	}

	if err := store.Register(sharing.PublicKind, sharing.PayloadVersion, sharing.DecodePublic); err != nil {
		return nil, err
	}

	if err := store.Register(sharing.FeedbackKind, sharing.PayloadVersion, sharing.DecodeFeedback); err != nil {
		return nil, err
	}

	return &sharingLedger{store: store}, nil
}

func sharingDeliveryKind(kind runtimecontract.DeliveryKind) bool {
	return kind == sharing.PublicKind || kind == sharing.FeedbackKind
}

func (l *sharingLedger) Enqueue(ctx context.Context, tx runtimecontract.DBTX, request runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	if !sharingDeliveryKind(request.Kind) {
		return "", errors.New("sharing: unsupported delivery kind")
	}

	return l.store.Enqueue(ctx, tx, request)
}

func (l *sharingLedger) Inspect(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.DeliveryID) (sharing.Evidence, error) {
	status, err := l.store.StatusInTransaction(ctx, tx, id)
	if err != nil {
		return sharing.Evidence{}, err
	}

	evidence := sharing.Evidence{UpdatedAt: status.UpdatedAt, TerminalAt: status.TerminalAt}
	switch status.State {
	case outbox.StatePending, outbox.StateAttempting, outbox.StateRetrying:
		evidence.State = sharing.Pending
	case outbox.StateCompleted:
		evidence.State = sharing.Completed
	case outbox.StatePermanentFailure:
		evidence.State = sharing.Rejected
	case outbox.StateReconciliationRequired:
		evidence.State = sharing.Unknown
	default:
		return sharing.Evidence{}, errors.New("sharing: invalid delivery state")
	}
	return evidence, nil
}

func (l *sharingLedger) Purge(ctx context.Context, tx runtimecontract.DBTX, kind runtimecontract.DeliveryKind, id runtimecontract.DeliveryID, cutoff time.Time) (bool, error) {
	if !sharingDeliveryKind(kind) {
		return false, errors.New("sharing: unsupported delivery kind")
	}

	return l.store.PurgeResolved(ctx, tx, kind, id, cutoff)
}
