package sharing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

const retentionOtherKind runtimecontract.DeliveryKind = "retention_unrelated"

const retentionBody = "private-retention-body-sentinel"

const retentionUsername = "private_retention_username"

const retentionCredential = "123456:private-retention-credential"

const retentionQuery = "private-retention-callback"

// This adapter has the same narrow kind allowlist as production. All inspection
// and deletion are real outbox operations; projection preconditions remain owned
// by Projector, rather than being simulated by a fake ledger.
type retentionLedger struct{ recoveryLedger }

func (l retentionLedger) Purge(ctx context.Context, tx runtimecontract.DBTX, kind runtimecontract.DeliveryKind, id runtimecontract.DeliveryID, cutoff time.Time) (bool, error) {
	if kind != PublicKind && kind != FeedbackKind {
		return false, errors.New("sharing: unsupported cleanup kind")
	}

	return l.store.PurgeResolved(ctx, tx, kind, id, cutoff)
}

type retentionTransport struct{ *recoveryTransport }

func (p retentionTransport) Deliver(ctx context.Context, r runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	outcome := p.recoveryTransport.Deliver(ctx, r)
	if outcome.Kind != runtimecontract.DeliveryAccepted {
		outcome.ReasonCode = retentionBody + retentionUsername + retentionCredential + retentionQuery
	}
	return outcome
}

type sharingRetention struct {
	*sharingRecovery
	ledger      retentionLedger
	diagnostics bytes.Buffer
	router      *interaction.Router
}

func newSharingRetention(t *testing.T) *sharingRetention {
	t.Helper()
	f := &sharingRetention{sharingRecovery: newSharingRecovery(t, localization.English)}
	f.store = outbox.NewStore(f.db, outbox.Options{Now: f.clock.Now, Diagnostics: observability.NewJSONSink(&f.diagnostics)})
	for _, schema := range []struct {
		kind    runtimecontract.DeliveryKind
		decoder outbox.Decoder
	}{{PublicKind, DecodePublic}, {FeedbackKind, DecodeFeedback}, {retentionOtherKind, DecodePublic}} {
		if err := f.store.Register(schema.kind, PayloadVersion, schema.decoder); err != nil {
			t.Fatal(err)
		}
	}

	f.store.Seal()
	var err error

	f.worker, err = outbox.NewWorker(f.store, outbox.WorkerOptions{Delivery: retentionTransport{f.transport}, Sleep: f.clock.Sleep})
	if err != nil {
		t.Fatal(err)
	}

	f.ledger = retentionLedger{recoveryLedger{f.store}}
	resolve := func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return f.locale, nil
	}
	f.projector, err = NewProjector(f.db, f.ledger, f.service.catalog, resolve, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	f.registry = interaction.NewRegistry()
	if err = Register(f.registry, f.service, f.ledger, f.projector.Refresh, resolve); err != nil {
		t.Fatal(err)
	}

	f.registry.Seal()
	f.router = interaction.NewRouter(f.registry, f.db, f.db, f.store, nil, interaction.RouterOptions{Now: f.clock.Now, Diagnostics: observability.NewJSONSink(&f.diagnostics)})
	f.selectView('b', contract.CharacterSummary)
	return f
}

func (f *sharingRetention) selectView(letter byte, kind contract.ViewKind) {
	f.t.Helper()
	token := strings.Repeat(string(letter), 32)
	v := decorationView()
	v.Kind = kind
	v.Body = runtimecontract.MessageContent{Text: retentionBody}
	if kind != contract.CharacterSummary {
		v.UsableUntil = f.clock.Now().Add(5 * time.Minute)
		v.EditMessageID = 7
	}
	service := decorationService(f.t, token, f.clock.Now())
	mustTx(f.t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := service.Decorate(ctx, tx, v)
		return err
	})
	f.cb.Data = sharePrefix + token
	f.cb.Source = &interaction.CallbackSource{Body: cloneContent(v.Body), CallbackData: []string{f.cb.Data}, EphemeralMessageID: 7}
	f.cb.ActorUsername = retentionUsername
	f.cb.QueryID = retentionQuery
}

func (f *sharingRetention) dispatchRetention(id runtimecontract.UpdateID) interaction.DispatchOutcome {
	f.t.Helper()
	cb := f.cb
	cb.Update = id
	r, err := f.router.Dispatch(context.Background(), cb)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *sharingRetention) rows(id runtimecontract.DeliveryID, wantDelivery, wantAttempts int) {
	f.t.Helper()
	for _, check := range []struct {
		query string
		want  int
	}{{`SELECT count(*) FROM outbound_deliveries WHERE id=?`, wantDelivery}, {`SELECT count(*) FROM outbound_attempts WHERE delivery_id=?`, wantAttempts}} {
		var n int

		if err := f.db.QueryRowContext(context.Background(), check.query, id).Scan(&n); err != nil {
			f.t.Fatal(err)
		}
		if n != check.want {
			f.t.Fatalf("retained delivery/attempt rows=%d want=%d", n, check.want)
		}
	}
}

func (f *sharingRetention) advanceTo(at time.Time) {
	f.t.Helper()
	f.clock.Advance(at.Sub(f.clock.Now()))
}

func (f *sharingRetention) outcome(kind runtimecontract.DeliveryOutcomeKind) {
	f.t.Helper()
	f.transport.configure(kind, nil, nil)
	var want error

	if kind != runtimecontract.DeliveryAccepted {
		want = outbox.ErrDeliveryNotCompleted
	}
	f.run(want)
}

func (f *sharingRetention) assertPrivate(values ...any) {
	f.t.Helper()
	// Hashes must not leak in the common byte/string JSON encodings either.
	var hash []byte

	if err := f.db.QueryRowContext(context.Background(), `SELECT body_hash FROM sharing_views WHERE token=?`, strings.TrimPrefix(f.cb.Data, sharePrefix)).Scan(&hash); err != nil {
		f.t.Fatal(err)
	}
	sentinels := []string{retentionBody, retentionUsername, retentionCredential, retentionQuery, f.cb.Data}
	if len(hash) > 0 {
		sentinels = append(sentinels, hex.EncodeToString(hash), base64.StdEncoding.EncodeToString(hash), string(hash))
	}
	data := f.diagnostics.String() + fmt.Sprint(values...)
	for _, sentinel := range sentinels {
		if strings.Contains(data, sentinel) {
			f.t.Fatal("private sentinel leaked into diagnostics or operational result")
		}
	}
}

func TestSharingRetentionIndependentOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		public, feedback           runtimecontract.DeliveryOutcomeKind
		publicState, feedbackState string
	}{
		{"completed", runtimecontract.DeliveryAccepted, runtimecontract.DeliveryAccepted, "succeeded", "completed"},
		{"rejected", runtimecontract.DeliveryPermanentFailure, runtimecontract.DeliveryPermanentFailure, "failed", "rejected"},
		{"abandoned_unknown", runtimecontract.DeliveryIndeterminate, runtimecontract.DeliveryIndeterminate, "unknown", "unknown"},
		{"feedback_unknown", runtimecontract.DeliveryAccepted, runtimecontract.DeliveryIndeterminate, "succeeded", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSharingRetention(t)
			f.dispatchRetention(1)
			accepted := f.action(1)

			if !bytes.Contains(f.payload(accepted.PublicDeliveryID), []byte(retentionBody)) {
				t.Fatal("missing frozen private payload")
			}

			f.outcome(tc.public)
			f.clock.Advance(time.Second)
			f.project()
			public := f.action(1)

			if public.PublicState != tc.publicState || public.FeedbackState != "queued" || public.CallbackQueryID != "" || !public.CallbackDeadline.IsZero() {
				t.Fatal("public projection or action authorization erasure failed")
			}

			// Feedback owns both an explicit JSON authorization copy and outbox columns.
			if !bytes.Contains(f.payload(public.FeedbackDeliveryID), []byte(retentionQuery)) {
				t.Fatal("missing original feedback authorization")
			}

			var query string
			var expiry int64

			if err := f.db.QueryRowContext(context.Background(), `SELECT callback_query_id,authorization_expires_at FROM outbound_deliveries WHERE id=?`, public.FeedbackDeliveryID).Scan(&query, &expiry); err != nil {
				t.Fatal(err)
			}

			if query != retentionQuery || expiry != time.Unix(115, 0).UnixMilli() {
				t.Fatal("feedback authorization changed")
			}

			f.clock.Advance(time.Second)
			f.outcome(tc.feedback)
			f.clock.Advance(time.Second)
			f.project()
			projected := f.action(1)

			if projected.FeedbackState != tc.feedbackState || !projected.FeedbackPurgeAfter.After(public.PublicPurgeAfter) {
				t.Fatal("feedback retention not independent")
			}

			for _, id := range []runtimecontract.DeliveryID{public.PublicDeliveryID, public.FeedbackDeliveryID} {
				status, err := f.store.Status(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}

				var reason string

				if err := f.db.QueryRowContext(context.Background(), `SELECT coalesce(reason_code,'') FROM outbound_attempts WHERE delivery_id=?`, id).Scan(&reason); err != nil {
					t.Fatal(err)
				}

				f.assertPrivate(status, reason)
			}

			if err := f.store.ReconcilePending(context.Background(), time.Second); err != nil {
				t.Fatal(err)
			}

			// A trigger proves attempts are physically removed BEFORE their parent.
			if _, err := f.db.ExecContext(context.Background(), `CREATE TRIGGER check_retention_delete_order BEFORE DELETE ON outbound_deliveries WHEN EXISTS(SELECT 1 FROM outbound_attempts WHERE delivery_id=OLD.id) BEGIN SELECT RAISE(ABORT,'attempts must be deleted first'); END`); err != nil {
				t.Fatal(err)
			}

			f.advanceTo(public.PublicPurgeAfter.Add(-time.Millisecond))
			f.project()
			f.rows(public.PublicDeliveryID, 1, 1)
			f.rows(public.FeedbackDeliveryID, 1, 1)
			f.advanceTo(public.PublicPurgeAfter)
			f.project()
			partial := f.action(1)
			f.rows(public.PublicDeliveryID, 0, 0)
			f.rows(public.FeedbackDeliveryID, 1, 1)

			if !partial.PublicPurged || partial.FeedbackPurged || !partial.NextCheckAt.Equal(projected.FeedbackPurgeAfter) {
				t.Fatal("partial cleanup lost remaining due work")
			}

			f.advanceTo(projected.FeedbackPurgeAfter)
			f.project()
			f.rows(public.FeedbackDeliveryID, 0, 0)
			done := f.action(1)

			if !done.PublicPurged || !done.FeedbackPurged || !done.NextCheckAt.IsZero() || done.PublicState != tc.publicState || done.FeedbackState != tc.feedbackState || done.PublicDeliveryID != accepted.PublicDeliveryID {
				t.Fatal("cleanup changed action tombstone")
			}

			var authCount int

			if err := f.db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_deliveries WHERE callback_query_id=? OR authorization_expires_at IS NOT NULL`, retentionQuery).Scan(&authCount); err != nil {
				t.Fatal(err)
			}

			if authCount != 0 {
				t.Fatal("private authorization survived cleanup")
			}

			// Router dedupe survives storage reopen; independently exercise the action
			// tombstone by deleting only runtime dedupe evidence in this isolated fixture.
			f.restart()
			f.router = interaction.NewRouter(f.registry, f.db, f.db, f.store, nil, interaction.RouterOptions{Now: f.clock.Now, Diagnostics: observability.NewJSONSink(&f.diagnostics)})
			f.dispatchRetention(1)
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM processed_updates WHERE update_id=1`); err != nil {
				t.Fatal(err)
			}

			f.dispatchRetention(1)
			f.project()
			f.run(nil)

			if f.count(PublicKind) != 0 || f.count(FeedbackKind) != 0 || len(f.transport.sent()) != 2 {
				t.Fatal("replay or cleanup resent purged delivery")
			}

			view := func() viewRecord {
				var v viewRecord

				mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					var err error

					v, err = readView(ctx, tx, strings.TrimPrefix(f.cb.Data, sharePrefix))
					return err
				})
				return v
			}()

			if view.Retired || len(view.BodyHash) != 32 || !view.UsableUntil.IsZero() {
				t.Fatal("cleanup expired usable character metadata")
			}

			f.dispatchRetention(2)

			if f.count(PublicKind) != 1 || f.action(2).PublicDeliveryID == accepted.PublicDeliveryID || len(f.transport.sent()) != 2 {
				t.Fatal("usable character control did not accept a new deliberate share after cleanup")
			}

			f.assertPrivate()

			if f.diagnostics.Len() == 0 {
				t.Fatal("privacy assertion captured no real router diagnostics")
			}
		})
	}
}

func TestSharingRetentionRollbackKeepsPayloadAndTombstoneAtomic(t *testing.T) {
	f := newSharingRetention(t)
	f.dispatchRetention(1)
	f.outcome(runtimecontract.DeliveryAccepted)
	f.clock.Advance(time.Second)
	f.project()
	f.outcome(runtimecontract.DeliveryAccepted)
	f.clock.Advance(time.Second)
	f.project()
	a := f.action(1)
	publicPayload, feedbackPayload := f.payload(a.PublicDeliveryID), f.payload(a.FeedbackDeliveryID)
	// Fail after both outbox deletions but before the durable purge flags commit.
	if _, err := f.db.ExecContext(context.Background(), `CREATE TRIGGER interrupt_retention BEFORE UPDATE ON sharing_actions WHEN NEW.public_purged=1 BEGIN SELECT RAISE(ABORT,'private-retention-body-sentinel private_retention_username 123456:private-retention-credential private-retention-callback'); END`); err != nil {
		t.Fatal(err)
	}

	f.advanceTo(a.FeedbackPurgeAfter)
	worked, err := f.projector.RunOnce(context.Background())
	if err == nil || worked {
		t.Fatal("injected cleanup storage failure ignored")
	}

	f.assertPrivate(err)
	f.rows(a.PublicDeliveryID, 1, 1)
	f.rows(a.FeedbackDeliveryID, 1, 1)
	after := f.action(1)

	if after.PublicPurged || after.FeedbackPurged || after.Revision != a.Revision || !bytes.Equal(publicPayload, f.payload(a.PublicDeliveryID)) || !bytes.Equal(feedbackPayload, f.payload(a.FeedbackDeliveryID)) {
		t.Fatal("cleanup rollback lost content, attempts or tombstone state")
	}

	f.dispatchRetention(1)

	if f.count(PublicKind) != 1 || len(f.transport.sent()) != 2 {
		t.Fatal("rollback replay requested compensating publication")
	}

	if _, err = f.db.ExecContext(context.Background(), `DROP TRIGGER interrupt_retention`); err != nil {
		t.Fatal(err)
	}

	f.project()
	f.rows(a.PublicDeliveryID, 0, 0)
	f.rows(a.FeedbackDeliveryID, 0, 0)

	if !f.action(1).PublicPurged || !f.action(1).FeedbackPurged {
		t.Fatal("cleanup did not recover")
	}

	f.assertPrivate()
}

func TestSharingRetentionGuardsPreserveUnresolvedAndUnrelatedWork(t *testing.T) {
	f := newSharingRetention(t)
	f.dispatchRetention(1)
	f.outcome(runtimecontract.DeliveryAccepted)
	f.clock.Advance(time.Second)
	f.project()
	first := f.action(1)
	claim, err := f.worker.ClaimDue(context.Background())
	if err != nil || claim == nil {
		t.Fatal("feedback claim failed", err)
	}

	f.status(first.FeedbackDeliveryID, outbox.StateAttempting)
	// A completed public attempt is deliberately left unprojected for a day.
	f.selectView('c', contract.CharacterSummary)
	f.dispatchRetention(2)
	f.outcome(runtimecontract.DeliveryAccepted)
	unprojected := f.action(2)
	f.status(unprojected.PublicDeliveryID, outbox.StateCompleted)
	// Registered unrelated content must remain outside sharing's cleanup surface.
	request, err := NewPublicRequest(PublicPayload{ActionUpdateID: 99, Destination: decorationView().Destination, Content: runtimecontract.MessageContent{Text: "unrelated durable content"}})
	if err != nil {
		t.Fatal(err)
	}

	request.Kind = retentionOtherKind
	var unrelated runtimecontract.DeliveryID

	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		unrelated, err = f.store.Enqueue(ctx, tx, request)
		return err
	})
	f.outcome(runtimecontract.DeliveryAccepted)
	unrelatedPayload := f.payload(unrelated)
	f.selectView('d', contract.CharacterSummary)
	f.dispatchRetention(3)
	pending := f.action(3)
	// A genuine retrying row adds the third active outbox state to the guard.
	request.Kind = PublicKind
	request.Retry.MaxAttempts = 2
	var retrying runtimecontract.DeliveryID

	// Claim the pending publication so the retrying fixture is next in FIFO.
	pendingClaim, err := f.worker.ClaimDue(context.Background())
	if err != nil || pendingClaim == nil {
		t.Fatal("public claim failed", err)
	}

	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		retrying, err = f.store.Enqueue(ctx, tx, request)
		return err
	})
	f.outcome(runtimecontract.DeliveryRetryableFailure)
	f.status(retrying, outbox.StateRetrying)
	f.selectView('e', contract.CharacterSummary)
	f.dispatchRetention(4)
	neverAttempted := f.action(4)
	before := map[runtimecontract.DeliveryID][]byte{}
	for _, id := range []runtimecontract.DeliveryID{first.FeedbackDeliveryID, unprojected.PublicDeliveryID, pending.PublicDeliveryID, retrying, neverAttempted.PublicDeliveryID} {
		before[id] = f.payload(id)
	}

	f.advanceTo(first.PublicPurgeAfter)
	// Even an overly broad requested cutoff cannot remove active states. The
	// composition allowlist must reject a registered, old unrelated kind.
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for _, id := range []runtimecontract.DeliveryID{first.FeedbackDeliveryID, pending.PublicDeliveryID, retrying, neverAttempted.PublicDeliveryID} {
			kind := PublicKind
			if id == first.FeedbackDeliveryID {
				kind = FeedbackKind
			}
			purged, err := f.ledger.Purge(ctx, tx, kind, id, f.clock.Now().Add(time.Hour))
			if err != nil {
				return err
			}

			if purged {
				t.Fatal("active delivery purged")
			}
		}

		if purged, err := f.ledger.Purge(ctx, tx, retentionOtherKind, unrelated, f.clock.Now()); err == nil || purged {
			t.Fatal("cleanup accepted unrelated registered kind")
		}

		return nil
	})
	f.project()
	f.rows(first.PublicDeliveryID, 0, 0)
	f.rows(first.FeedbackDeliveryID, 1, 1)
	after := f.action(1)

	if !after.PublicPurged || after.FeedbackPurged || after.FeedbackState != "queued" || after.NextCheckAt.IsZero() {
		t.Fatal("active feedback lost independent retention work")
	}

	projectedLate := f.action(2)

	if projectedLate.PublicState != "succeeded" || projectedLate.PublicPurged || !projectedLate.PublicPurgeAfter.Equal(f.clock.Now().Add(24*time.Hour)) {
		t.Fatal("unprojected work purged using old terminal time")
	}

	for id, payload := range before {
		if !bytes.Equal(payload, f.payload(id)) {
			t.Fatal("guarded work content changed")
		}
	}

	if !bytes.Equal(unrelatedPayload, f.payload(unrelated)) {
		t.Fatal("unrelated payload changed")
	}

	f.rows(unrelated, 1, 1)
	f.rows(neverAttempted.PublicDeliveryID, 1, 0)
	f.status(pending.PublicDeliveryID, outbox.StateAttempting)
	f.status(retrying, outbox.StateRetrying)
	// Missing feature projection is an independent guard for quarantined feedback.
	// Recover abandoned claims, then first project Unknown; its retention clock
	// must start now, rather than at the public cleanup deadline.
	if err := f.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	f.clock.Advance(time.Second)
	f.project()
	unknown := f.action(1)

	if unknown.FeedbackState != "unknown" || unknown.FeedbackPurged || !unknown.FeedbackPurgeAfter.Equal(f.clock.Now().Add(24*time.Hour)) {
		t.Fatal("unprojected quarantine was prematurely removed")
	}

	f.rows(first.FeedbackDeliveryID, 1, 1)
	f.assertPrivate()
}

func TestSharingRetentionRetiresInventoryAfterProjectionKeepsCharacterUsable(t *testing.T) {
	f := newSharingRetention(t)
	characterToken := strings.TrimPrefix(f.cb.Data, sharePrefix)
	f.selectView('c', contract.InventoryPage)
	inventoryToken := strings.TrimPrefix(f.cb.Data, sharePrefix)
	f.dispatchRetention(1)
	a := f.action(1)
	f.clock.Advance(5 * time.Minute)
	f.project()
	read := func(token string) viewRecord {
		var v viewRecord

		mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			var err error

			v, err = readView(ctx, tx, token)
			return err
		})
		return v
	}
	if v := read(inventoryToken); v.Retired || len(v.BodyHash) != 32 {
		t.Fatal("expired inventory retired before outstanding public projection")
	}

	f.outcome(runtimecontract.DeliveryAccepted)
	f.clock.Advance(time.Second)
	f.project()
	projected := f.action(1)

	if projected.FeedbackState != "skipped" || !projected.FeedbackPurged || projected.CallbackQueryID != "" || !projected.CallbackDeadline.IsZero() {
		t.Fatal("expired original authorization retained or renewed")
	}

	if v := read(inventoryToken); !v.Retired || v.BodyHash != nil {
		t.Fatal("expired resolved inventory fingerprint survived retirement")
	}

	f.advanceTo(projected.PublicPurgeAfter)
	f.project()
	f.rows(a.PublicDeliveryID, 0, 0)
	f.dispatchRetention(1)
	f.dispatchRetention(2)

	if f.count(PublicKind) != 0 || f.action(1).PublicDeliveryID != a.PublicDeliveryID || len(f.transport.sent()) != 1 {
		t.Fatal("retired replay recreated publication")
	}

	if v := read(characterToken); v.Retired || len(v.BodyHash) != 32 || !v.UsableUntil.IsZero() {
		t.Fatal("inventory cleanup retired character metadata")
	}

	f.assertPrivate()
	// The operational tombstone contains no body or callback authorization.
	tombstone, err := json.Marshal(f.action(1))
	if err != nil {
		t.Fatal(err)
	}

	f.assertPrivate(string(tombstone))
}
