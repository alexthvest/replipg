package sharing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

func TestProjectorIndependentRetention(t *testing.T) {
	for _, state := range []DeliveryState{Completed, Rejected, Unknown} {
		t.Run(string(state), func(t *testing.T) {
			now := time.Unix(110, 0).UTC()
			l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
			db, p, v, a := projectorFixture(t, &now, l)
			l.evidence[a.PublicDeliveryID] = Evidence{State: Unknown, UpdatedAt: now.Add(-time.Hour)}
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, first := projectorRead(t, db, v.Token)

			if !first.PublicPurgeAfter.Equal(now.Add(24 * time.Hour)) {
				t.Fatalf("public retention starts at projection: %+v", first)
			}

			now = now.Add(2 * time.Second)
			l.evidence[first.FeedbackDeliveryID] = Evidence{State: state, TerminalAt: now.Add(-time.Second), UpdatedAt: now.Add(-time.Second)}
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, feedback := projectorRead(t, db, v.Token)

			if feedback.FeedbackState != string(state) || !feedback.FeedbackPurgeAfter.Equal(now.Add(24*time.Hour)) {
				t.Fatalf("feedback projection=%+v", feedback)
			}

			// A later delivery observation cannot rewrite either terminal outcome or resend.
			l.evidence[first.FeedbackDeliveryID] = Evidence{State: Completed, TerminalAt: now.Add(time.Hour)}
			now = first.PublicPurgeAfter.Add(-time.Millisecond)
			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return p.Refresh(ctx, tx, v.Token) })

			if len(l.purges) != 0 {
				t.Fatal("early purge")
			}

			now = first.PublicPurgeAfter
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, partial := projectorRead(t, db, v.Token)

			if !partial.PublicPurged || partial.FeedbackPurged || !partial.NextCheckAt.Equal(feedback.FeedbackPurgeAfter) {
				t.Fatalf("partial=%+v", partial)
			}

			if len(l.purges) != 1 || l.purges[0].kind != PublicKind || l.purges[0].id != a.PublicDeliveryID || !l.purges[0].cutoff.Equal(now.Add(-24*time.Hour)) {
				t.Fatalf("public purge=%+v", l.purges)
			}

			now = feedback.FeedbackPurgeAfter
			l.purgeFalse = map[runtimecontract.DeliveryID]bool{feedback.FeedbackDeliveryID: true}
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, refused := projectorRead(t, db, v.Token)

			if refused.FeedbackPurged || !refused.NextCheckAt.Equal(now.Add(time.Second)) {
				t.Fatal("refused purge not retried")
			}

			l.purgeFalse = nil
			now = now.Add(time.Second)
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, done := projectorRead(t, db, v.Token)

			if !done.PublicPurged || !done.FeedbackPurged || !done.NextCheckAt.IsZero() || done.FeedbackState != feedback.FeedbackState || !done.PublicOutcomeAt.Equal(first.PublicOutcomeAt) {
				t.Fatalf("done=%+v", done)
			}

			last := l.purges[len(l.purges)-1]

			if last.kind != FeedbackKind || last.id != feedback.FeedbackDeliveryID {
				t.Fatal("incorrect feedback kind")
			}

			calls := len(l.inspected)
			purges := len(l.purges)
			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := p.Refresh(ctx, tx, v.Token); err != nil {
					return err
				}

				if err := insertAction(ctx, tx, a); err == nil {
					t.Fatal("purge removed replay tombstone")
				}

				return nil
			})

			if len(l.requests) != 1 || len(l.inspected) != calls || len(l.purges) != purges {
				t.Fatal("cleanup repeated or resent")
			}
		})
	}
}

func TestProjectorSkippedRetentionRollbackAndRetirement(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
	db, p, v, a := projectorFixture(t, &now, l)
	// Expiry does not retire a pending view.
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, _ := projectorRead(t, db, v.Token)

	if view.Retired {
		t.Fatal("pending view retired")
	}

	now = now.Add(time.Second)
	l.evidence[a.PublicDeliveryID] = Evidence{State: Rejected, TerminalAt: now}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, action := projectorRead(t, db, v.Token)

	if !view.Retired || view.BodyHash != nil || action.FeedbackState != "skipped" || !action.FeedbackPurged {
		t.Fatalf("retirement=%+v action=%+v", view, action)
	}

	now = action.PublicPurgeAfter
	sentinel := errors.New("purge storage failure")
	l.purgeErr = sentinel
	if worked, err := p.RunOnce(context.Background()); worked || !errors.Is(err, sentinel) {
		t.Fatalf("rollback=%v,%v", worked, err)
	}

	_, action = projectorRead(t, db, v.Token)

	if action.PublicPurged {
		t.Fatal("purge flag survived rollback")
	}

	l.purgeErr = nil
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, action = projectorRead(t, db, v.Token)

	if !action.PublicPurged || !action.NextCheckAt.IsZero() {
		t.Fatalf("skipped cleanup=%+v", action)
	}
}

func TestProjectorRetiresOnlyExpiredInventory(t *testing.T) {
	now := time.Unix(300, 0).UTC()
	l := &projectorLedger{}
	db, p, _, _ := projectorFixture(t, &now, l)
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for _, item := range []struct {
			token string
			kind  contract.ViewKind
			until time.Time
		}{{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", contract.CharacterSummary, time.Time{}}, {"cccccccccccccccccccccccccccccccc", contract.InventoryPage, now.Add(time.Millisecond)}, {"dddddddddddddddddddddddddddddddd", contract.ItemDetails, now}} {
			v := fixtureView()
			v.Token = item.token
			v.Kind = item.kind
			v.UsableUntil = item.until
			if err := insertView(ctx, tx, v); err != nil {
				return err
			}
		}
		return nil
	})
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for _, token := range []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddddd"} {
			v, err := readView(ctx, tx, token)
			if err != nil {
				return err
			}

			if v.Retired != (token[0] == 'd') {
				t.Fatalf("unexpected retirement=%+v", v)
			}
		}
		return nil
	})
}

func TestProjectorPendingFeedbackSurvivesPublicCleanup(t *testing.T) {
	now := time.Unix(110, 0).UTC()
	l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
	db, p, v, a := projectorFixture(t, &now, l)
	l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: now}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, action := projectorRead(t, db, v.Token)
	now = action.PublicPurgeAfter
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, action := projectorRead(t, db, v.Token)

	if !action.PublicPurged || action.FeedbackPurged || action.FeedbackState != "queued" || !action.FeedbackPurgeAfter.IsZero() || view.Retired {
		t.Fatalf("pending feedback=%+v view=%+v", action, view)
	}

	if len(l.purges) != 1 || l.purges[0].kind != PublicKind {
		t.Fatal("pending feedback purged")
	}

	if len(l.requests) != 1 {
		t.Fatal("pending feedback resent")
	}

	now = now.Add(time.Second)
	l.evidence[action.FeedbackDeliveryID] = Evidence{State: Unknown, UpdatedAt: now}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	view, action = projectorRead(t, db, v.Token)

	if !view.Retired || action.FeedbackState != "unknown" || !action.FeedbackPurgeAfter.Equal(now.Add(24*time.Hour)) {
		t.Fatal("terminal feedback not retired independently")
	}
}

func TestProjectorFutureEvidenceRetention(t *testing.T) {
	now := time.Unix(110, 0).UTC()
	l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
	db, p, v, a := projectorFixture(t, &now, l)
	future := now.Add(time.Minute)
	l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: future}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, action := projectorRead(t, db, v.Token)

	if !action.PublicPurgeAfter.Equal(future.Add(24 * time.Hour)) {
		t.Fatal("public minimum evidence retention shortened")
	}

	now = now.Add(time.Second)
	l.evidence[action.FeedbackDeliveryID] = Evidence{State: Completed, TerminalAt: future}
	if _, err := p.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, action = projectorRead(t, db, v.Token)

	if !action.FeedbackPurgeAfter.Equal(future.Add(24 * time.Hour)) {
		t.Fatal("feedback minimum evidence retention shortened")
	}
}

func TestProjectorInvalidFeedbackEvidenceRollsBack(t *testing.T) {
	for _, evidence := range []Evidence{{State: Completed}, {State: Rejected}, {State: Unknown}, {State: "invalid"}} {
		t.Run(string(evidence.State), func(t *testing.T) {
			now := time.Unix(110, 0).UTC()
			l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
			db, p, v, a := projectorFixture(t, &now, l)
			l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: now}
			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, before := projectorRead(t, db, v.Token)
			now = now.Add(time.Second)
			l.evidence[before.FeedbackDeliveryID] = evidence
			if worked, err := p.RunOnce(context.Background()); worked || err == nil {
				t.Fatal("invalid evidence accepted")
			}

			_, after := projectorRead(t, db, v.Token)

			if after.Revision != before.Revision || after.FeedbackState != "queued" {
				t.Fatal("invalid projection committed")
			}
		})
	}
}
