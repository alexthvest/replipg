package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

// This fixture composes real sharing routes, transactions, durable delivery
// storage, attempt recovery, delivery workers, and projection. Only time and the
// Telegram DeliveryPort are fake. The adapter reads authoritative outbox evidence;
// it has no independently supplied outcome or delivery state machine.
type recoveryLedger struct{ store *outbox.Store }

func (l recoveryLedger) Enqueue(ctx context.Context, tx runtimecontract.DBTX, r runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	return l.store.Enqueue(ctx, tx, r)
}

func (l recoveryLedger) Inspect(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.DeliveryID) (Evidence, error) {
	s, err := l.store.StatusInTransaction(ctx, tx, id)
	if err != nil {
		return Evidence{}, err
	}

	e := Evidence{UpdatedAt: s.UpdatedAt, TerminalAt: s.TerminalAt}
	switch s.State {
	case outbox.StatePending, outbox.StateAttempting, outbox.StateRetrying:
		e.State = Pending
	case outbox.StateCompleted:
		e.State = Completed
	case outbox.StatePermanentFailure:
		e.State = Rejected
	case outbox.StateReconciliationRequired:
		e.State = Unknown
	default:
		return Evidence{}, errors.New("unexpected durable delivery state")
	}
	return e, nil
}

func (l recoveryLedger) Purge(ctx context.Context, tx runtimecontract.DBTX, kind runtimecontract.DeliveryKind, id runtimecontract.DeliveryID, cutoff time.Time) (bool, error) {
	return l.store.PurgeResolved(ctx, tx, kind, id, cutoff)
}

type recoveryTransport struct {
	mu       sync.Mutex
	requests []runtimecontract.ResponseRequest
	outcome  runtimecontract.DeliveryOutcomeKind
	entered  chan struct{}
	release  chan struct{}
}

func (p *recoveryTransport) Deliver(ctx context.Context, r runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	p.mu.Lock()
	p.requests = append(p.requests, r)
	outcome, entered, release := p.outcome, p.entered, p.release
	p.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryCancelled}
		case <-release:
		}
	}
	if outcome == "" || outcome == runtimecontract.DeliveryAccepted {
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryAccepted, Receipt: &runtimecontract.DeliveryReceipt{ChatID: -1, MessageID: 9}}
	}

	return runtimecontract.DeliveryOutcome{Kind: outcome, ReasonCode: "authorization"}
}

func (p *recoveryTransport) configure(outcome runtimecontract.DeliveryOutcomeKind, entered, release chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.outcome, p.entered, p.release = outcome, entered, release
}

func (p *recoveryTransport) sent() []runtimecontract.ResponseRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]runtimecontract.ResponseRequest(nil), p.requests...)
}

type sharingRecovery struct {
	t         *testing.T
	dir       string
	db        *persistence.DB
	clock     *testsupport.ManualClock
	transport *recoveryTransport
	store     *outbox.Store
	worker    *outbox.Worker
	projector *Projector
	service   *Service
	registry  *interaction.Registry
	cb        interaction.Callback
	locale    localization.Locale
}

func newSharingRecovery(t *testing.T, locale localization.Locale) *sharingRecovery {
	t.Helper()
	f := &sharingRecovery{t: t, dir: t.TempDir(), clock: testsupport.NewManualClock(time.Unix(100, 0).UTC()), transport: &recoveryTransport{}, locale: locale}
	t.Cleanup(func() {
		if f.db != nil {
			if err := f.db.Close(); err != nil {
				t.Error(err)
			}
		}
	})

	f.restart()
	v := decorationView()
	v.Locale = locale
	if locale == localization.Russian {
		v.Body.Text = "😀 Жизнь 100"
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := f.service.Decorate(ctx, tx, v)
		return err
	})
	f.cb = interaction.Callback{Identity: interaction.Identity{Update: 1, User: v.Owner, Chat: v.Destination.ChatID, ChatKind: v.Destination.Kind}, QueryID: "original-query", EphemeralMessageID: 7, Data: sharePrefix + strings.Repeat("a", 32), Source: &interaction.CallbackSource{Body: cloneContent(v.Body), CallbackData: []string{sharePrefix + strings.Repeat("a", 32)}, EphemeralMessageID: 7}}
	return f
}

func (f *sharingRecovery) restart() {
	f.t.Helper()
	if f.db != nil {
		if err := f.db.Close(); err != nil {
			f.t.Fatal(err)
		}
	}
	var err error

	f.db, err = persistence.Open(context.Background(), persistence.Options{StorageDir: f.dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		f.t.Fatal(err)
	}
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		f.t.Fatal(err)
	}
	if err = f.db.Migrate(context.Background(), migrations); err != nil {
		f.t.Fatal(err)
	}
	f.store = outbox.NewStore(f.db, outbox.Options{Now: f.clock.Now})
	if err = f.store.Register(PublicKind, PayloadVersion, DecodePublic); err != nil {
		f.t.Fatal(err)
	}
	if err = f.store.Register(FeedbackKind, PayloadVersion, DecodeFeedback); err != nil {
		f.t.Fatal(err)
	}
	f.store.Seal()
	if err = f.store.Recover(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	f.worker, err = outbox.NewWorker(f.store, outbox.WorkerOptions{Delivery: f.transport, Sleep: f.clock.Sleep})
	if err != nil {
		f.t.Fatal(err)
	}
	f.service = decorationService(f.t, strings.Repeat("a", 32), f.clock.Now())
	f.service.now = f.clock.Now
	ledger := recoveryLedger{f.store}
	resolve := func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return f.locale, nil
	}
	f.projector, err = NewProjector(f.db, ledger, f.service.catalog, resolve, f.clock.Now)
	if err != nil {
		f.t.Fatal(err)
	}
	f.registry = interaction.NewRegistry()
	if err = Register(f.registry, f.service, ledger, f.projector.Refresh, resolve); err != nil {
		f.t.Fatal(err)
	}
	f.registry.Seal()
}

func (f *sharingRecovery) dispatch(id runtimecontract.UpdateID) (interaction.HandlerResult, error) {
	cb := f.cb
	cb.Update = id
	handler, ok := f.registry.Resolve(cb)
	if !ok {
		return interaction.HandlerResult{}, errors.New("sharing route unavailable")
	}

	var result interaction.HandlerResult

	err := f.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		result, err = handler.Handle(ctx, shareScope{tx}, cb)
		return err
	})
	return result, err
}

func (f *sharingRecovery) press(id runtimecontract.UpdateID) interaction.HandlerResult {
	f.t.Helper()
	r, err := f.dispatch(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *sharingRecovery) action(id runtimecontract.UpdateID) actionRecord {
	f.t.Helper()
	var a actionRecord

	mustTx(f.t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		a, err = readAction(ctx, tx, id)
		return err
	})
	return a
}

func (f *sharingRecovery) view() viewRecord {
	f.t.Helper()
	var v viewRecord

	mustTx(f.t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		v, err = readView(ctx, tx, strings.Repeat("a", 32))
		return err
	})
	return v
}

func (f *sharingRecovery) count(kind runtimecontract.DeliveryKind) int {
	f.t.Helper()
	var n int

	if err := f.db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_deliveries WHERE kind=?`, kind).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *sharingRecovery) project() {
	f.t.Helper()
	if _, err := f.projector.RunOnce(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *sharingRecovery) run(wantErr error) {
	f.t.Helper()
	_, err := f.worker.RunOnce(context.Background())
	if !errors.Is(err, wantErr) {
		f.t.Fatalf("delivery worker error=%v want=%v", err, wantErr)
	}
}

func (f *sharingRecovery) status(id runtimecontract.DeliveryID, state outbox.State) outbox.DeliveryStatus {
	f.t.Helper()
	s, err := f.store.Status(context.Background(), id)
	if err != nil || s.State != state {
		f.t.Fatalf("outbox status=%+v error=%v want=%s", s, err, state)
	}
	return s
}

func (f *sharingRecovery) payload(id runtimecontract.DeliveryID) []byte {
	f.t.Helper()
	var b []byte

	if err := f.db.QueryRowContext(context.Background(), `SELECT content FROM outbound_deliveries WHERE id=?`, id).Scan(&b); err != nil {
		f.t.Fatal(err)
	}
	return b
}

func (f *sharingRecovery) guidance(result interaction.HandlerResult, key localization.Key) {
	f.t.Helper()
	want, err := f.service.catalog.Text(f.locale, key, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if got := feedbackText(f.t, result); got != want {
		f.t.Fatalf("guidance=%q want=%q", got, want)
	}
}

func TestSharingRecoveryPendingPreservesFrozenDelivery(t *testing.T) {
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		t.Run(string(locale), func(t *testing.T) {
			f := newSharingRecovery(t, locale)
			captured := cloneContent(f.cb.Source.Body)
			f.press(1)
			accepted := f.action(1)
			before := f.payload(accepted.PublicDeliveryID)
			f.restart()
			f.clock.Advance(time.Second)
			if got := f.action(1); got.PublicDeliveryID != accepted.PublicDeliveryID || got.UpdateID != 1 || !got.AcceptedAt.Equal(accepted.AcceptedAt) {
				t.Fatal("restart lost accepted action identity")
			}

			if !reflect.DeepEqual(before, f.payload(accepted.PublicDeliveryID)) {
				t.Fatal("pending payload changed on restart")
			}

			f.press(1)
			f.guidance(f.press(2), localization.SharingPending)

			if f.count(PublicKind) != 1 {
				t.Fatal("replay or pending press enqueued another publication")
			}

			// Neither later presentation/source changes nor current group language
			// may rewrite the already authorized public payload.
			f.cb.Source.Body.Text = "replacement view after acceptance"
			f.locale = localization.Russian
			if locale == localization.Russian {
				f.locale = localization.English
			}
			f.status(accepted.PublicDeliveryID, outbox.StatePending)
			f.run(nil)
			sent := f.transport.sent()

			if len(sent) != 1 {
				t.Fatalf("sends=%d want=1", len(sent))
			}

			public, ok := sent[0].(runtimecontract.PublicResponse)

			if !ok {
				t.Fatal("pending public work became personal")
			}

			attribution, err := Attribution(f.service.catalog, locale, 42)
			if err != nil {
				t.Fatal(err)
			}

			if public.Content.Text != captured.Text+"\n\n"+attribution || !reflect.DeepEqual(public.Content.Entities, captured.Entities) || len(public.Content.Buttons) != 0 || public.Destination != decorationView().Destination {
				t.Fatal("restart changed snapshot, attribution, formatting, destination or controls")
			}

			var payload PublicPayload

			if err = json.Unmarshal(before, &payload); err != nil {
				t.Fatal(err)
			}

			if payload.ActionUpdateID != 1 {
				t.Fatal("serialized action identity lost")
			}

			f.clock.Advance(time.Second) // pending inspection was deferred by the repeated press
			f.project()
			f.guidanceFromFeedback(localization.SharingSuccess)
		})
	}
}

func (f *sharingRecovery) guidanceFromFeedback(key localization.Key) {
	f.t.Helper()
	a := f.action(1)
	if a.FeedbackState != "queued" {
		f.t.Fatalf("feedback state=%s want=queued", a.FeedbackState)
	}
	payload, err := runtimecontract.NewVersionedPayload(PayloadVersion, f.payload(a.FeedbackDeliveryID))
	if err != nil {
		f.t.Fatal(err)
	}
	r, err := DecodeFeedback(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	got := r.(runtimecontract.EphemeralResponse)
	want, err := f.service.catalog.Text(f.locale, key, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	auth := got.Authorization.(runtimecontract.CallbackAuthorization)
	if got.Content.Text != want || got.TargetUserID != 42 || got.Destination != decorationView().Destination || auth.CallbackQueryID != "original-query" || !auth.ValidUntil.Equal(time.Unix(115, 0)) {
		f.t.Fatal("feedback changed guidance, owner, destination or original bounded authorization")
	}
}

func TestSharingRecoveryAttemptingNeverAutomaticallyReposts(t *testing.T) {
	f := newSharingRecovery(t, localization.English)
	f.press(1)
	id := f.action(1).PublicDeliveryID
	before := f.payload(id)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.transport.configure("", entered, release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { _, err := f.worker.RunOnce(ctx); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transport barrier not reached")
	}

	f.status(id, outbox.StateAttempting)
	f.guidance(f.press(2), localization.SharingPending)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted worker=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not cancel")
	}

	f.clock.Advance(2 * time.Second)
	f.restart()
	unknownAt := f.clock.Now()
	status := f.status(id, outbox.StateReconciliationRequired)

	if !status.UpdatedAt.Equal(unknownAt) || !status.NextAttemptAt.IsZero() {
		t.Fatal("recovery scheduled uncertain resend")
	}

	f.project()
	f.guidanceFromFeedback(localization.SharingUnknown)
	until := unknownAt.Add(30 * time.Second)

	if !f.view().UnknownUntil.Equal(until) {
		t.Fatal("unknown hold not anchored to durable recovery evidence")
	}

	f.transport.configure("", nil, nil)
	f.run(nil) // only the private uncertainty feedback is due
	f.clock.Advance(5 * time.Second)
	f.restart()
	f.press(1)
	f.guidance(f.press(3), localization.SharingUnknown)

	if !f.view().UnknownUntil.Equal(until) {
		t.Fatal("restart or polling slid uncertainty hold")
	}

	f.clock.Advance(25*time.Second - time.Millisecond)
	f.guidance(f.press(4), localization.SharingUnknown)
	f.run(nil)

	if len(f.transport.sent()) != 2 || f.count(PublicKind) != 1 || !reflect.DeepEqual(before, f.payload(id)) {
		t.Fatal("uncertain public work resent or changed")
	}

	f.clock.Advance(time.Millisecond)
	f.guidance(f.press(5), localization.SharingUnknownRetry)
	newer := f.action(5)

	if newer.PublicDeliveryID == id || f.count(PublicKind) != 2 {
		t.Fatal("manual retry did not create distinct action")
	}

	f.run(nil)
	f.status(id, outbox.StateReconciliationRequired)
	f.status(newer.PublicDeliveryID, outbox.StateCompleted)

	if len(f.transport.sent()) != 3 {
		t.Fatal("manual reshare transmitted old delivery")
	}
}

func TestSharingRecoveryCompletionBeforeProjection(t *testing.T) {
	f := newSharingRecovery(t, localization.English)
	f.press(1)
	id := f.action(1).PublicDeliveryID
	f.clock.Advance(2 * time.Second)
	f.run(nil)
	terminal := f.clock.Now()

	if f.action(1).PublicState != "pending" {
		t.Fatal("fixture projected before crash boundary")
	}

	f.restart()
	f.clock.Advance(29*time.Second + 999*time.Millisecond)
	f.press(2) // callback refresh must enforce cooldown despite background projector lag
	if f.count(PublicKind) != 1 || !f.view().LastSuccessAt.Equal(terminal) {
		t.Fatal("projection lag bypassed cooldown or changed terminal timestamp")
	}

	if f.action(1).FeedbackState != "skipped" {
		t.Fatal("expired original authorization was renewed after restart")
	}

	f.restart()
	f.press(1)
	f.clock.Advance(time.Millisecond)
	f.press(3)

	if f.count(PublicKind) != 2 {
		t.Fatal("persisted cooldown blocked exact 30-second boundary")
	}

	f.status(id, outbox.StateCompleted)

	if len(f.transport.sent()) != 1 {
		t.Fatal("projection or replay sent a public copy")
	}
}

func TestSharingRecoveryQueuedFeedback(t *testing.T) {
	for _, mode := range []string{"accepted", "rejected", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newSharingRecovery(t, localization.Russian)
			f.press(1)
			f.run(nil)
			f.clock.Advance(time.Second)
			f.project()
			f.guidanceFromFeedback(localization.SharingSuccess)
			before := f.action(1)
			frozen := f.payload(before.FeedbackDeliveryID)
			f.restart()

			if !reflect.DeepEqual(frozen, f.payload(before.FeedbackDeliveryID)) {
				t.Fatal("feedback authorization changed across restart")
			}

			switch mode {
			case "rejected":
				f.transport.configure(runtimecontract.DeliveryPermanentFailure, nil, nil)
			case "expired":
				f.clock.Advance(14 * time.Second)
			}

			wantErr := error(nil)
			if mode != "accepted" {
				wantErr = outbox.ErrDeliveryNotCompleted
			}
			f.run(wantErr)
			f.clock.Advance(time.Second)
			f.project()
			after := f.action(1)
			wantFeedback := "completed"
			if mode != "accepted" {
				wantFeedback = "rejected"
			}

			if after.PublicState != "succeeded" || after.FeedbackState != wantFeedback || after.PublicDeliveryID != before.PublicDeliveryID || after.FeedbackDeliveryID != before.FeedbackDeliveryID {
				t.Fatalf("feedback altered confirmed public outcome: %+v", after)
			}

			wantSends := 2
			if mode == "expired" {
				wantSends = 1
			}
			f.restart()
			f.press(1)
			f.press(2)
			f.project()
			f.run(nil)

			if f.count(PublicKind) != 1 || f.count(FeedbackKind) != 1 || len(f.transport.sent()) != wantSends {
				t.Fatal("feedback failure/expiry/restart requested compensating delivery")
			}
		})
	}
}

func TestSharingRecoveryDefiniteRejectionAllowsRetry(t *testing.T) {
	f := newSharingRecovery(t, localization.English)
	f.press(1)
	first := f.action(1).PublicDeliveryID
	f.transport.configure(runtimecontract.DeliveryPermanentFailure, nil, nil)
	f.run(outbox.ErrDeliveryNotCompleted)
	f.restart()
	f.clock.Advance(time.Second)
	f.press(2) // Refresh terminal evidence in callback; no background projector run.
	f.guidanceFromFeedback(localization.SharingFailure)

	if f.action(1).PublicState != "failed" || !f.view().LastSuccessAt.IsZero() || f.count(PublicKind) != 2 {
		t.Fatal("definite rejection started cooldown or blocked retry")
	}

	f.status(first, outbox.StatePermanentFailure)
	f.transport.configure("", nil, nil)
	f.run(nil)
	f.run(nil) // one failure feedback and one deliberate new public action
	f.status(f.action(2).PublicDeliveryID, outbox.StateCompleted)

	if len(f.transport.sent()) != 3 {
		t.Fatal("retry resubmitted rejected action")
	}
}

func TestSharingRecoveryConcurrentDispatchProjectionAndCompletion(t *testing.T) {
	f := newSharingRecovery(t, localization.English)
	f.press(1)
	entered, release := make(chan struct{}), make(chan struct{})
	f.transport.configure("", entered, release)
	done := make(chan error, 1)
	go func() { _, err := f.worker.RunOnce(context.Background()); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transport barrier not reached")
	}

	// All contenders start together while the durable public attempt is active.
	// Completion then competes with their SQLite transactions; either ordering
	// must preserve the reservation and derive cooldown from durable completion.
	start := make(chan struct{})
	errs := make(chan error, 17)
	var wg sync.WaitGroup

	for i := range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := f.dispatch(runtimecontract.UpdateID(i + 2)); errs <- err }()
	}

	wg.Add(1)
	go func() { defer wg.Done(); <-start; _, err := f.projector.RunOnce(context.Background()); errs <- err }()
	close(start)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	f.transport.configure("", nil, nil)
	f.press(100)

	if f.count(PublicKind) != 1 || len(f.transport.sent()) != 1 || !f.view().LastSuccessAt.Equal(f.clock.Now()) {
		t.Fatal("concurrent callback/projector/outbox completion admitted duplicate public work")
	}

	f.clock.Advance(time.Second)
	f.project()

	if f.count(FeedbackKind) != 1 {
		t.Fatal("competing projections queued duplicate outcome feedback")
	}

	f.restart()
	f.press(1)

	if f.count(PublicKind) != 1 {
		t.Fatal("concurrent accepted identity was lost on restart")
	}
}

func TestSharingRecoveryProjectionFailureDoesNotResend(t *testing.T) {
	f := newSharingRecovery(t, localization.English)
	f.press(1)
	id := f.action(1).PublicDeliveryID
	f.run(nil)
	// A real storage fault aborts projection after feedback enqueue. The transaction
	// must roll back the feedback and projection together, while completed public
	// work remains authoritative and is never sent to compensate.
	if _, err := f.db.ExecContext(context.Background(), `CREATE TRIGGER interrupt_projection BEFORE UPDATE ON sharing_actions WHEN NEW.public_state='succeeded' BEGIN SELECT RAISE(ABORT,'injected projection interruption'); END`); err != nil {
		t.Fatal(err)
	}

	f.clock.Advance(time.Second)
	if _, err := f.projector.RunOnce(context.Background()); err == nil {
		t.Fatal("injected projection storage failure was ignored")
	}

	if f.action(1).PublicState != "pending" || f.count(FeedbackKind) != 0 || !f.view().LastSuccessAt.IsZero() {
		t.Fatal("failed projection transaction committed partial state")
	}

	f.restart()
	f.run(nil)
	f.status(id, outbox.StateCompleted)

	if len(f.transport.sent()) != 1 {
		t.Fatal("projection storage failure resent completed public work")
	}

	if _, err := f.db.ExecContext(context.Background(), `DROP TRIGGER interrupt_projection`); err != nil {
		t.Fatal(err)
	}

	f.project()
	f.guidanceFromFeedback(localization.SharingSuccess)

	if f.action(1).PublicState != "succeeded" || f.count(FeedbackKind) != 1 || f.count(PublicKind) != 1 {
		t.Fatal("projection did not recover atomically")
	}
}
