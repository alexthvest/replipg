package sharing

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type projectorLedger struct {
	evidence               map[runtimecontract.DeliveryID]Evidence
	requests               []runtimecontract.DeliveryRequest
	inspected              []runtimecontract.DeliveryID
	enqueueErr, inspectErr error
	purges                 []purgeCall
	purgeFalse             map[runtimecontract.DeliveryID]bool
	purgeErr               error
}

func (l *projectorLedger) Enqueue(_ context.Context, _ runtimecontract.DBTX, r runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	if l.enqueueErr != nil {
		return "", l.enqueueErr
	}

	l.requests = append(l.requests, r)
	return runtimecontract.DeliveryID(fmt.Sprintf("feedback-%d", len(l.requests))), nil
}

func (l *projectorLedger) Inspect(_ context.Context, _ runtimecontract.DBTX, id runtimecontract.DeliveryID) (Evidence, error) {
	l.inspected = append(l.inspected, id)
	if l.inspectErr != nil {
		return Evidence{}, l.inspectErr
	}

	e, ok := l.evidence[id]
	if !ok {
		return Evidence{State: Pending}, nil
	}

	return e, nil
}

type purgeCall struct {
	kind   runtimecontract.DeliveryKind
	id     runtimecontract.DeliveryID
	cutoff time.Time
}

func (l *projectorLedger) Purge(_ context.Context, _ runtimecontract.DBTX, kind runtimecontract.DeliveryKind, id runtimecontract.DeliveryID, cutoff time.Time) (bool, error) {
	l.purges = append(l.purges, purgeCall{kind, id, cutoff})
	return !l.purgeFalse[id], l.purgeErr
}

func projectorFixture(t *testing.T, now *time.Time, l *projectorLedger) (*persistence.DB, *Projector, viewRecord, actionRecord) {
	t.Helper()
	db := sharingTestDB(t)
	v := fixtureView()
	a := fixtureAction(1, v.Token)
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertView(ctx, tx, v); err != nil {
			return err
		}

		return insertAction(ctx, tx, a)
	})
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	p, err := NewProjector(db, l, c, func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.Russian, nil
	}, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}

	return db, p, v, a
}

func projectorRead(t *testing.T, db *persistence.DB, token string) (v viewRecord, a actionRecord) {
	t.Helper()
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		v, err = readView(ctx, tx, token)
		if err != nil {
			return err
		}

		a, err = readAction(ctx, tx, 1)
		return err
	})
	return
}

func TestProjectorTerminalEvidence(t *testing.T) {
	for _, tc := range []struct {
		state DeliveryState
		want  string
		key   localization.Key
	}{{Completed, "succeeded", localization.SharingSuccess}, {Rejected, "failed", localization.SharingFailure}, {Unknown, "unknown", localization.SharingUnknown}} {
		t.Run(string(tc.state), func(t *testing.T) {
			now := time.Unix(110, 0).UTC()
			terminal := time.Unix(105, 0).UTC()
			l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
			db, p, v, a := projectorFixture(t, &now, l)
			l.evidence[a.PublicDeliveryID] = Evidence{State: tc.state, TerminalAt: terminal, UpdatedAt: terminal}
			worked, err := p.RunOnce(context.Background())
			if err != nil || !worked {
				t.Fatalf("RunOnce=%v,%v", worked, err)
			}

			got, action := projectorRead(t, db, v.Token)

			if action.PublicState != tc.want || !action.PublicOutcomeAt.Equal(terminal) || action.FeedbackState != "queued" {
				t.Fatalf("projection=%+v", action)
			}

			if tc.state == Completed {
				if !got.LastSuccessAt.Equal(terminal) {
					t.Fatal("cooldown uses projection time")
				}

				for _, delta := range []time.Duration{30*time.Second - time.Millisecond, 30 * time.Second} {
					eligible := !terminal.Add(delta).Before(got.LastSuccessAt.Add(30 * time.Second))

					if eligible != (delta == 30*time.Second) {
						t.Fatal("cooldown boundary")
					}
				}
			} else if !got.LastSuccessAt.IsZero() {
				t.Fatal("failure started success cooldown")
			}

			if tc.state == Unknown && !got.UnknownUntil.Equal(terminal.Add(30*time.Second)) {
				t.Fatal("unknown deadline")
			}

			if len(l.requests) != 1 || l.requests[0].Kind != FeedbackKind {
				t.Fatal("expected one private feedback")
			}

			response, err := DecodeFeedback(l.requests[0].Payload)
			if err != nil {
				t.Fatal(err)
			}

			feedback := response.(runtimecontract.EphemeralResponse)
			c, _ := localization.Load()
			text, _ := c.Text(localization.Russian, tc.key, nil)

			if feedback.Content.Text != text {
				t.Fatal("feedback did not use current locale")
			}

			auth := feedback.Authorization.(runtimecontract.CallbackAuthorization)

			if !auth.ValidUntil.Equal(a.CallbackDeadline) {
				t.Fatal("deadline renewed")
			}

			now = now.Add(time.Hour)
			l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: now, UpdatedAt: now}
			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return p.Refresh(ctx, tx, v.Token) })
			after, _ := projectorRead(t, db, v.Token)

			if len(l.requests) != 1 || !after.UnknownUntil.Equal(got.UnknownUntil) || !after.LastSuccessAt.Equal(got.LastSuccessAt) {
				t.Fatal("terminal evidence reprojected or feedback repeated")
			}
		})
	}
}

func TestProjectorRefreshBypassesDueTimeAndExpiredFeedback(t *testing.T) {
	for _, offset := range []time.Duration{-time.Millisecond, 0, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			now := time.Unix(115, 0).Add(offset)
			l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
			db, p, v, a := projectorFixture(t, &now, l)
			l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: time.Unix(104, 0)}
			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				work, err := readAction(ctx, tx, a.UpdateID)
				if err != nil {
					return err
				}

				work.NextCheckAt = now.Add(time.Hour)
				if err = updateAction(ctx, tx, work); err != nil {
					return err
				}

				return p.Refresh(ctx, tx, v.Token)
			})
			_, work := projectorRead(t, db, v.Token)

			if work.PublicState != "succeeded" {
				t.Fatal("refresh ignored future scheduled action")
			}

			want := "skipped"
			if offset < 0 {
				want = "queued"
			}

			if work.FeedbackState != want || work.CallbackQueryID != "" || !work.CallbackDeadline.IsZero() {
				t.Fatalf("feedback=%+v", work)
			}
		})
	}
}

func TestProjectorRollbackOnFeedbackError(t *testing.T) {
	for _, failure := range []string{"enqueue", "locale", "inspect"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Unix(110, 0)
			sentinel := errors.New("injected failure")
			l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
			db, p, v, a := projectorFixture(t, &now, l)
			l.evidence[a.PublicDeliveryID] = Evidence{State: Completed, TerminalAt: time.Unix(105, 0)}
			switch failure {
			case "enqueue":
				l.enqueueErr = sentinel
			case "inspect":
				l.inspectErr = sentinel
			case "locale":
				p.resolve = func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
					return "", sentinel
				}
			}

			worked, err := p.RunOnce(context.Background())
			if err == nil || worked {
				t.Fatalf("RunOnce=%v,%v", worked, err)
			}

			got, work := projectorRead(t, db, v.Token)

			if work.PublicState != "pending" || !got.LastSuccessAt.IsZero() {
				t.Fatal("projection survived rollback")
			}

			l.enqueueErr = nil
			l.inspectErr = nil
			p.resolve = func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
				return localization.English, nil
			}
			if _, err = p.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			_, work = projectorRead(t, db, v.Token)

			if work.PublicState != "succeeded" {
				t.Fatal("projection retry failed")
			}
		})
	}
}

func TestProjectorFairBoundedPendingWork(t *testing.T) {
	now := time.Unix(110, 0)
	l := &projectorLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}
	db, p, _, _ := projectorFixture(t, &now, l)
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for i := 2; i <= 105; i++ {
			v := fixtureView()
			v.Token = fmt.Sprintf("%032x", i)
			if err := insertView(ctx, tx, v); err != nil {
				return err
			}

			a := fixtureAction(runtimecontract.UpdateID(i), v.Token)
			if err := insertAction(ctx, tx, a); err != nil {
				return err
			}

			if i == 105 {
				l.evidence[a.PublicDeliveryID] = Evidence{State: Rejected, TerminalAt: now}
			}
		}
		return nil
	})
	for _, want := range []int{100, 105} {
		worked, err := p.RunOnce(context.Background())
		if err != nil || !worked {
			t.Fatal(worked, err)
		}

		if len(l.inspected) != want {
			t.Fatalf("inspections=%d want=%d", len(l.inspected), want)
		}
	}

	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		a, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}

		if a.PublicState != "pending" || !a.NextCheckAt.Equal(now.Add(time.Second)) {
			t.Fatal("pending reservation or scheduling lost")
		}

		a, err = readAction(ctx, tx, 105)
		if err == nil && a.PublicState != "failed" {
			t.Fatal("pending work starved terminal work")
		}

		return err
	})
	worked, err := p.RunOnce(context.Background())
	if err != nil || worked {
		t.Fatalf("idle=%v,%v", worked, err)
	}
}
