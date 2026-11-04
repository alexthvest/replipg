package sharing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type shareScope struct{ tx runtimecontract.DBTX }

func (s shareScope) DBTX() runtimecontract.DBTX { return s.tx }
func (shareScope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	panic("use injected ledger")
}
func (shareScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	panic("no jobs")
}

// SQL-backed fake makes rollback and concurrent publication assertions meaningful.
type callbackLedger struct {
	fail     bool
	evidence map[runtimecontract.DeliveryID]Evidence
}

func (l *callbackLedger) Enqueue(ctx context.Context, tx runtimecontract.DBTX, r runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	if l.fail {
		return "", errors.New("injected enqueue failure")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO callback_deliveries(kind,payload) VALUES(?,?)`, r.Kind, r.Payload.Data())
	if err != nil {
		return "", err
	}
	id, err := result.LastInsertId()
	return runtimecontract.DeliveryID(fmt.Sprintf("callback-%d", id)), err
}
func (l *callbackLedger) Inspect(_ context.Context, _ runtimecontract.DBTX, id runtimecontract.DeliveryID) (Evidence, error) {
	if e, ok := l.evidence[id]; ok {
		return e, nil
	}
	return Evidence{State: Pending}, nil
}
func (*callbackLedger) Purge(context.Context, runtimecontract.DBTX, runtimecontract.DeliveryKind, runtimecontract.DeliveryID, time.Time) (bool, error) {
	return false, nil
}

type callbackFixture struct {
	db        *persistence.DB
	reg       *interaction.Registry
	service   *Service
	ledger    *callbackLedger
	cb        interaction.Callback
	now       time.Time
	projector *Projector
}

func newCallbackFixture(t *testing.T, body string) *callbackFixture {
	t.Helper()
	f := &callbackFixture{db: sharingTestDB(t), now: time.Unix(100, 0).UTC(), ledger: &callbackLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}}
	f.service = decorationService(t, strings.Repeat("a", 32), f.now)
	f.service.now = func() time.Time { return f.now }
	resolve := func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.Russian, nil
	}
	var err error
	f.projector, err = NewProjector(f.db, f.ledger, f.service.catalog, resolve, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	f.reg = interaction.NewRegistry()
	if err = Register(f.reg, f.service, f.ledger, f.projector.Refresh, resolve); err != nil {
		t.Fatal(err)
	}
	f.reg.Seal()
	v := decorationView()
	if body != "" {
		v.Body = runtimecontract.MessageContent{Text: body}
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE callback_deliveries(id INTEGER PRIMARY KEY,kind TEXT,payload BLOB)`); err != nil {
			return err
		}
		_, err := f.service.Decorate(ctx, tx, v)
		return err
	})
	f.cb = interaction.Callback{Identity: interaction.Identity{Update: 1, User: v.Owner, Chat: v.Destination.ChatID, ChatKind: v.Destination.Kind}, QueryID: "query", EphemeralMessageID: 7, Data: sharePrefix + strings.Repeat("a", 32), Source: &interaction.CallbackSource{Body: cloneContent(v.Body), CallbackData: []string{sharePrefix + strings.Repeat("a", 32)}, EphemeralMessageID: 7}}
	return f
}
func (f *callbackFixture) dispatch(cb interaction.Callback) (result interaction.HandlerResult, err error) {
	handler, ok := f.reg.Resolve(cb)
	if !ok {
		return result, errors.New("route not resolved")
	}
	err = f.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		var e error
		result, e = handler.Handle(ctx, shareScope{tx}, cb)
		return e
	})
	return
}
func (f *callbackFixture) count(t *testing.T, kind runtimecontract.DeliveryKind) int {
	t.Helper()
	var n int
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM callback_deliveries WHERE kind=?`, kind).Scan(&n)
	})
	return n
}
func feedbackText(t *testing.T, r interaction.HandlerResult) string {
	t.Helper()
	if len(r.ImmediateResponses) != 1 {
		t.Fatalf("expected personal guidance, got %+v", r)
	}
	e, ok := r.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if !ok {
		t.Fatal("guidance must be ephemeral")
	}
	if err := runtimecontract.ValidateResponse(e); err != nil {
		t.Fatal(err)
	}
	return e.Content.Text
}
func TestShareAcceptanceExactSnapshotAndReplay(t *testing.T) {
	f := newCallbackFixture(t, "")
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	if got := f.count(t, PublicKind); got != 1 {
		t.Fatalf("public requests=%d want=1", got)
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var data []byte
		if err := tx.QueryRowContext(ctx, `SELECT payload FROM callback_deliveries WHERE kind=?`, PublicKind).Scan(&data); err != nil {
			return err
		}
		p, err := runtimecontract.NewVersionedPayload(1, data)
		if err != nil {
			return err
		}
		r, err := DecodePublic(p)
		if err != nil {
			return err
		}
		got := r.(runtimecontract.PublicResponse)
		attribution, _ := Attribution(f.service.catalog, localization.English, 42)
		if got.Content.Text != f.cb.Source.Body.Text+"\n\n"+attribution || !reflect.DeepEqual(got.Content.Entities, f.cb.Source.Body.Entities) || len(got.Content.Buttons) != 0 || got.Destination.ChatID != -1 {
			t.Fatalf("snapshot changed: %+v", got)
		}
		a, err := readAction(ctx, tx, 1)
		if err == nil && (!a.CallbackDeadline.Equal(f.now.Add(15*time.Second)) || a.CallbackQueryID != "query") {
			t.Fatal("callback authorization changed")
		}
		return err
	})
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	if f.count(t, PublicKind) != 1 {
		t.Fatal("replay published")
	}
	// Completion and a much later replay must retain the accepted identity.
	f.ledger.evidence["callback-1"] = Evidence{State: Completed, TerminalAt: f.now, UpdatedAt: f.now}
	f.now = f.now.Add(time.Minute)
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	if f.count(t, PublicKind) != 1 {
		t.Fatal("terminal replay published")
	}
}
func TestShareConcurrentPending(t *testing.T) {
	f := newCallbackFixture(t, "")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb := f.cb
			cb.Update = runtimecontract.UpdateID(i + 1)
			_, err := f.dispatch(cb)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, PublicKind) != 1 {
		t.Fatal("concurrent callbacks published more than once")
	}
}
func TestShareRefreshCooldownAndRetry(t *testing.T) {
	for _, state := range []DeliveryState{Completed, Rejected, Unknown} {
		t.Run(string(state), func(t *testing.T) {
			f := newCallbackFixture(t, "")
			if _, err := f.dispatch(f.cb); err != nil {
				t.Fatal(err)
			}
			terminal := f.now
			f.ledger.evidence["callback-1"] = Evidence{State: state, TerminalAt: terminal, UpdatedAt: terminal}
			f.now = terminal.Add(30*time.Second - time.Millisecond)
			cb := f.cb
			cb.Update = 2
			r, err := f.dispatch(cb)
			if err != nil {
				t.Fatal(err)
			}
			if state == Rejected {
				if f.count(t, PublicKind) != 2 {
					t.Fatal("definite rejection started cooldown")
				}
				return
			}
			if f.count(t, PublicKind) != 1 {
				t.Fatal("early retry published")
			}
			text := feedbackText(t, r)
			key := localization.SharingCooldown
			args := localization.Args{"seconds": localization.Integer(1)}
			if state == Unknown {
				key = localization.SharingUnknown
				args = nil
			}
			want, _ := f.service.catalog.Text(localization.Russian, key, args)
			if text != want {
				t.Fatalf("guidance=%q want=%q", text, want)
			}
			f.now = terminal.Add(30 * time.Second)
			cb.Update = 3
			r, err = f.dispatch(cb)
			if err != nil {
				t.Fatal(err)
			}
			if f.count(t, PublicKind) != 2 {
				t.Fatal("exact 30s boundary blocked")
			}
			if state == Unknown {
				want, _ = f.service.catalog.Text(localization.Russian, localization.SharingUnknownRetry, nil)
				if feedbackText(t, r) != want {
					t.Fatal("missing uncertainty retry warning")
				}
			}
		})
	}
}
func TestShareRejectsInvalidAndUnavailableSources(t *testing.T) {
	for _, name := range []string{"owner", "group", "bot", "token", "keyboard", "body", "receiver", "binding", "missing", "expired", "oversize"} {
		t.Run(name, func(t *testing.T) {
			body := ""
			if name == "oversize" {
				body = strings.Repeat("x", 4096)
			}
			f := newCallbackFixture(t, body)
			owned := false
			switch name {
			case "owner":
				f.cb.User = 43
			case "group":
				f.cb.Chat = -2
			case "bot":
				f.cb.ActorIsBot = true
			case "token":
				f.cb.Data = sharePrefix + strings.Repeat("b", 32)
			case "keyboard":
				f.cb.Source.CallbackData = nil
				owned = true
			case "body":
				f.cb.Source.Body.Text = "modified"
				owned = true
			case "receiver":
				f.cb.Source.ReceiverUserID = 43
			case "binding":
				mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					v, err := readView(ctx, tx, strings.Repeat("a", 32))
					if err != nil {
						return err
					}
					v.BoundMessageID = 8
					return updateView(ctx, tx, v)
				})
				owned = true
			case "missing":
				f.cb.Source = nil
				owned = true
			case "expired":
				mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					_, err := tx.ExecContext(ctx, `UPDATE sharing_views SET view_kind='inventory_page',usable_until=?`, f.now.UnixMilli())
					return err
				})
				owned = true
			case "oversize":
				owned = true
			}
			r, err := f.dispatch(f.cb)
			if err != nil {
				t.Fatal(err)
			}
			if f.count(t, PublicKind) != 0 {
				t.Fatal("unsafe publication")
			}
			if owned {
				feedbackText(t, r)
			} else if len(r.ImmediateResponses) != 0 {
				t.Fatal("foreign source got guidance")
			}
		})
	}
}
func TestShareAcceptanceRollback(t *testing.T) {
	f := newCallbackFixture(t, "")
	f.ledger.fail = true
	if _, err := f.dispatch(f.cb); err == nil {
		t.Fatal("enqueue failure ignored")
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		v, err := readView(ctx, tx, strings.Repeat("a", 32))
		if err != nil {
			return err
		}
		if v.BoundMessageID != 0 {
			t.Fatal("binding survived rollback")
		}
		_, err = readAction(ctx, tx, 1)
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("action survived rollback", err)
		}
		return nil
	})
	f.ledger.fail = false
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	if f.count(t, PublicKind) != 1 {
		t.Fatal("rollback prevented retry")
	}
}

func TestShareAcceptanceRollbackAfterEnqueue(t *testing.T) {
	f := newCallbackFixture(t, "")
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_share_action BEFORE INSERT ON sharing_actions BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	})
	if _, err := f.dispatch(f.cb); err == nil {
		t.Fatal("action persistence failure ignored")
	}
	if f.count(t, PublicKind) != 0 {
		t.Fatal("outbound intent survived action rollback")
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		v, err := readView(ctx, tx, strings.Repeat("a", 32))
		if err != nil {
			return err
		}
		if v.BoundMessageID != 0 {
			t.Fatal("source binding survived failed acceptance")
		}
		_, err = tx.ExecContext(ctx, `DROP TRIGGER reject_share_action`)
		return err
	})
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	if f.count(t, PublicKind) != 1 {
		t.Fatal("retry after rollback failed")
	}
}

func TestShareInheritedDeadlineAndCharacterWithoutExpiry(t *testing.T) {
	for _, inventory := range []bool{false, true} {
		t.Run(fmt.Sprint(inventory), func(t *testing.T) {
			f := newCallbackFixture(t, "")
			if inventory {
				mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					_, err := tx.ExecContext(ctx, `UPDATE sharing_views SET view_kind='inventory_page',usable_until=?`, f.now.Add(3*time.Second).UnixMilli())
					return err
				})
			} else {
				f.now = f.now.Add(24 * time.Hour)
			}
			result, err := f.dispatch(f.cb)
			if err != nil {
				t.Fatal(err)
			}
			if f.count(t, PublicKind) != 1 {
				t.Fatal("usable source rejected")
			}
			feedbackText(t, result)
			auth := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Authorization.(runtimecontract.CallbackAuthorization)
			want := f.now.Add(15 * time.Second)
			if inventory {
				want = f.now.Add(3 * time.Second)
			}
			if !auth.ValidUntil.Equal(want) {
				t.Fatal("immediate deadline renewed", auth.ValidUntil, want)
			}
			mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				a, err := readAction(ctx, tx, 1)
				if err == nil && !a.CallbackDeadline.Equal(want) {
					t.Fatal("stored deadline renewed")
				}
				return err
			})
		})
	}
}

func TestShareUnknownHoldDoesNotSlideAndPendingGuidance(t *testing.T) {
	f := newCallbackFixture(t, "")
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	cb := f.cb
	cb.Update = 2
	result, err := f.dispatch(cb)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := f.service.catalog.Text(localization.Russian, localization.SharingPending, nil)
	if feedbackText(t, result) != want || f.count(t, PublicKind) != 1 {
		t.Fatal("pending action admitted another publication")
	}
	f.ledger.evidence["callback-1"] = Evidence{State: Unknown, UpdatedAt: f.now}
	for i := range 3 {
		f.now = f.now.Add(5 * time.Second)
		cb.Update = runtimecontract.UpdateID(i + 3)
		if _, err = f.dispatch(cb); err != nil {
			t.Fatal(err)
		}
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		v, err := readView(ctx, tx, strings.Repeat("a", 32))
		if err == nil && !v.UnknownUntil.Equal(time.Unix(130, 0)) {
			t.Fatal("uncertainty hold slid on repeated press", v.UnknownUntil)
		}
		return err
	})
	if f.count(t, PublicKind) != 1 {
		t.Fatal("uncertain delivery resent")
	}
}

func TestShareRegisterRejectsMissingDependencies(t *testing.T) {
	f := newCallbackFixture(t, "")
	resolve := LocaleResolver(func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.English, nil
	})
	cases := []struct {
		reg     *interaction.Registry
		s       *Service
		l       DeliveryLedger
		refresh RefreshFunc
		resolve LocaleResolver
	}{
		{nil, f.service, f.ledger, f.projector.Refresh, resolve},
		{interaction.NewRegistry(), nil, f.ledger, f.projector.Refresh, resolve},
		{interaction.NewRegistry(), f.service, nil, f.projector.Refresh, resolve},
		{interaction.NewRegistry(), f.service, f.ledger, nil, resolve},
		{interaction.NewRegistry(), f.service, f.ledger, f.projector.Refresh, nil},
	}
	for i, tc := range cases {
		if err := Register(tc.reg, tc.s, tc.l, tc.refresh, tc.resolve); err == nil {
			t.Fatalf("case %d accepted missing dependency", i)
		}
	}
}

func TestShareKnownLatestOutcomeClearsHistoricalUncertaintyGuidance(t *testing.T) {
	f := newCallbackFixture(t, "")
	if _, err := f.dispatch(f.cb); err != nil {
		t.Fatal(err)
	}
	f.ledger.evidence["callback-1"] = Evidence{State: Unknown, UpdatedAt: f.now}
	f.now = f.now.Add(30 * time.Second)
	cb := f.cb
	cb.Update = 2
	if _, err := f.dispatch(cb); err != nil {
		t.Fatal(err)
	}
	var latestID runtimecontract.DeliveryID
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		a, err := readAction(ctx, tx, 2)
		latestID = a.PublicDeliveryID
		return err
	})
	f.ledger.evidence[latestID] = Evidence{State: Completed, TerminalAt: f.now, UpdatedAt: f.now}
	f.now = f.now.Add(30 * time.Second)
	cb.Update = 3
	result, err := f.dispatch(cb)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := f.service.catalog.Text(localization.Russian, localization.SharingPending, nil)
	if got := feedbackText(t, result); got != want {
		t.Fatalf("historical uncertainty guidance=%q want=%q", got, want)
	}
	if f.count(t, PublicKind) != 3 {
		t.Fatal("known successful outcome prevented later share")
	}
}
