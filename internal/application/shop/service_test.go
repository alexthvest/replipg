package shop_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	app "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

type catalog struct {
	snapshot items.Snapshot
	price    domain.Amount
	removed  bool
}

func (c *catalog) Offer(id string, mode items.Mode) (domain.Offer, bool) {
	return domain.Offer{
		Target: domain.Target{
			CatalogID: id,
			Mode:      mode,
		},
		BuyPrice:  c.price,
		SellPrice: c.price,
	}, !c.removed
}

func (c *catalog) Capture(string) (items.Snapshot, error) {
	if c.removed {
		return items.Snapshot{}, errors.New("removed item")
	}
	return c.snapshot, nil
}

type failingUOW struct {
	db     *sqlite.Database
	calls  int
	failAt int
}

var errInjected = errors.New("injected commit failure")

func (u *failingUOW) WithinShop(ctx context.Context, fn func(app.Transaction) error) error {
	u.calls++
	return u.db.WithinShop(ctx, func(tx app.Transaction) error {
		if err := fn(tx); err != nil {
			return err
		}
		if u.failAt == u.calls {
			return errInjected
		}
		return nil
	})
}

type fixture struct {
	db      *sqlite.Database
	path    string
	uow     *failingUOW
	service *app.Service
	catalog *catalog
	now     time.Time
	scope   core.Scope
	quote   domain.Quote
}

func (f *fixture) clock() time.Time {
	return f.now
}

func setup(t *testing.T, sale bool) *fixture {
	t.Helper()
	ctx := context.Background()
	f := &fixture{
		path: filepath.Join(t.TempDir(), "shop.db"),
		now:  time.Unix(1000, 0),
		scope: core.Scope{
			Group:    -1,
			Actor:    42,
			BotID:    100,
			UpdateID: 1,
		},
	}
	db, err := sqlite.Open(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	t.Cleanup(func() {
		_ = f.db.Close()
	})
	character, err := db.EnsureCharacter(ctx, characters.Character{
		ID:    "character",
		Group: -1,
		User:  42,
		Name:  "Player",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.catalog = &catalog{
		price: "10",
		snapshot: items.Snapshot{
			Version:   1,
			CatalogID: "bread",
			Mode:      items.Stackable,
			Rarity:    "common",
			English: items.Text{
				Name:        "Bread",
				Description: "Food",
			},
			Russian: items.Text{
				Name:        "Хлеб",
				Description: "Еда",
			},
		},
	}
	f.quote = domain.Quote{
		ID:           "confirmation",
		Group:        -1,
		Player:       42,
		Character:    character.ID,
		Direction:    domain.Buy,
		Snapshot:     f.catalog.snapshot,
		Quantity:     2,
		Unit:         "10",
		Total:        "20",
		BankID:       99,
		TreasuryID:   2,
		DeliveryChat: -1,
	}
	if sale {
		if err = db.WithinShop(ctx, func(tx app.Transaction) error {
			entries, err := inventory.Grant(ctx, tx.Inventory, core.NewID, -1, character.ID, f.catalog.snapshot, 5)
			if err == nil {
				f.quote.Entry = entries[0].ID
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		f.quote.Direction = domain.Sell
	}
	f.uow = &failingUOW{db: db}
	f.service = newService(t, f.uow, f.clock, 99, 2)
	return f
}

func newService(
	t *testing.T,
	uow app.UnitOfWork,
	now func() time.Time,
	bankID, treasuryID int64,
) *app.Service {
	t.Helper()
	service, err := app.New(uow, bank.Protocol{}, core.NewID, now, bankID, treasuryID)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func confirm(t *testing.T, f *fixture) domain.Order {
	t.Helper()
	order, err := f.service.Confirm(context.Background(), f.scope, f.quote, f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func due(t *testing.T, f *fixture) app.Work {
	t.Helper()
	work, err := f.service.DueWork(context.Background(), f.now, 8)
	if err != nil || len(work) != 1 {
		t.Fatalf("due work: %+v %v", work, err)
	}
	return work[0]
}

func paid(work app.Work, sale bool) string {
	proof := `{"transactionId":"501","senderAccountId":456,"recipient":{"accountId":2},"amount":"20","createdAt":"2026-01-01T00:00:00Z"}`
	data := `{"requestId":"payment","state":"paid","transaction":` + proof + `}`
	if sale {
		data = `{"transactionId":"501","senderAccountId":2,"recipient":{"accountId":456,"telegramUserId":42},"amount":"20","createdAt":"2026-01-01T00:00:00Z"}`
	}
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"data":%s}}`, work.RequestID, data)
}

func TestConfirmFrozenTermsAndReplay(t *testing.T) {
	f := setup(t, false)
	f.catalog.price = "11"
	if _, err := f.service.Confirm(context.Background(), f.scope, f.quote, f.catalog); !errors.Is(err, app.ErrQuoteChanged) {
		t.Fatalf("changed quote: %v", err)
	}
	f.catalog.price = "10"
	order := confirm(t, f)
	f.catalog.removed = true
	f.scope.UpdateID++
	replayed := confirm(t, f)
	if replayed.ID != order.ID {
		t.Fatal("repeated confirmation created new order")
	}
	work := due(t, f)
	if work.OrderID != order.ID {
		t.Fatal("financial intent lost")
	}
	if more, err := f.service.DueWork(context.Background(), f.now, 8); err != nil || len(more) != 0 {
		t.Fatalf("duplicate immediate claim: %+v %v", more, err)
	}
	foreign := f.scope
	foreign.Group = -2
	if _, err := f.service.Get(context.Background(), foreign, order.ID); err == nil {
		t.Fatal("foreign group read order")
	}
}

func TestPurchaseImmediatePaidDuplicateAndRemovedOffer(t *testing.T) {
	f := setup(t, false)
	confirm(t, f)
	work := due(t, f)
	f.catalog.removed = true
	result, err := f.service.HandleReply(context.Background(), work.RequestID, paid(work, false))
	if err != nil || result.Order.State != domain.Completed || len(result.Entries) != 1 || result.Entries[0].Quantity != 2 {
		t.Fatalf("immediate paid fulfillment: %+v %v", result, err)
	}
	for range 2 {
		if _, err := f.service.HandleReply(context.Background(), work.RequestID, paid(work, false)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 2 {
		t.Fatalf("duplicate settlement granted twice: %+v %v", entries, err)
	}
	pending := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"data":{"requestId":"payment","state":"pending"}}}`, work.RequestID)
	result, err = f.service.HandleReply(context.Background(), work.RequestID, pending)
	if err != nil || result.Order.State != domain.Completed {
		t.Fatalf("late pending regressed outcome: %+v %v", result, err)
	}
}

func TestPendingUsesFreshStatusReadsAndGrantsNothing(t *testing.T) {
	f := setup(t, false)
	confirm(t, f)
	work := due(t, f)
	pending := func(id string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"data":{"requestId":"payment","state":"pending"},"operation":{"actionId":"bank.payment.request.public.v1","operationId":"not-payment"}}}`, id)
	}
	result, err := f.service.HandleReply(context.Background(), work.RequestID, pending(work.RequestID))
	if err != nil || result.Order.State != domain.AwaitingPayment {
		t.Fatalf("pending invoice: %+v %v", result, err)
	}
	entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 0 {
		t.Fatal("pending/operation-only invoice granted items")
	}
	f.now = f.now.Add(10 * time.Second)
	status := due(t, f)
	if status.RequestID == work.RequestID || status.Body == work.Body {
		t.Fatal("status reused financial transport ID")
	}
	f.now = f.now.Add(10 * time.Second)
	next := due(t, f)
	if next.RequestID == status.RequestID {
		t.Fatal("status retry reused a cached read ID")
	}
	result, err = f.service.HandleReply(context.Background(), next.RequestID, paid(next, false))
	if err != nil || result.Order.State != domain.Completed {
		t.Fatalf("status settlement: %+v %v", result, err)
	}
}

func TestUnknownSaleRetainsReservationAndOriginalKey(t *testing.T) {
	f := setup(t, true)
	order := confirm(t, f)
	first := due(t, f)
	if err := f.service.ObserveSend(context.Background(), first.RequestID, errors.New("lost response")); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	retry := due(t, f)
	if first.RequestID != retry.RequestID || first.Body != retry.Body {
		t.Fatal("unknown transfer changed original identity/payload")
	}
	permissionError := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":-32601,"message":"Method unavailable"}}`, retry.RequestID)
	result, err := f.service.HandleReply(context.Background(), retry.RequestID, permissionError)
	if err != nil || result.Order.State != domain.ReconciliationRequired {
		t.Fatalf("unknown then revoked permission: %+v %v", result, err)
	}
	entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 5 || entries[0].Reserved != 2 {
		t.Fatalf("unknown payout released items: %+v %v", entries, err)
	}
	result, err = f.service.HandleReply(context.Background(), first.RequestID, paid(first, true))
	if err != nil || result.Order.State != domain.Completed || result.Order.ID != order.ID {
		t.Fatalf("late successful original payout: %+v %v", result, err)
	}
	entries, err = f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 3 || entries[0].Reserved != 0 {
		t.Fatalf("sale ownership: %+v %v", entries, err)
	}
}

func TestFirstSaleRejectionReleasesWithoutInventingReason(t *testing.T) {
	f := setup(t, true)
	confirm(t, f)
	work := due(t, f)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":-32601,"message":"Method unavailable"}}`, work.RequestID)
	result, err := f.service.HandleReply(context.Background(), work.RequestID, body)
	if err != nil || result.Order.State != domain.Failed || result.Order.Diagnostic != "Method unavailable" {
		t.Fatalf("definitive first rejection: %+v %v", result, err)
	}
	entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 5 || entries[0].Reserved != 0 {
		t.Fatalf("failed sale consumed items: %+v %v", entries, err)
	}
}

func TestRestartRecoversLostInvoiceCreation(t *testing.T) {
	f := setup(t, false)
	confirm(t, f)
	first := due(t, f)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.uow.db = db
	f.now = f.now.Add(time.Hour)
	f.service = newService(t, f.uow, f.clock, 99, 2)
	retry := due(t, f)
	if first.Body != retry.Body || first.RequestID != retry.RequestID {
		t.Fatal("restart created new financial intent")
	}
	if _, err = f.service.HandleReply(context.Background(), retry.RequestID, paid(retry, false)); err != nil {
		t.Fatal(err)
	}
}

func TestFailureInjectionAtDurableBoundaries(t *testing.T) {
	for _, stage := range []string{"intent", "observation", "proof", "ownership"} {
		t.Run(stage, func(t *testing.T) {
			f := setup(t, false)
			ctx := context.Background()
			if stage == "intent" {
				f.uow.failAt = f.uow.calls + 1
				if _, err := f.service.Confirm(ctx, f.scope, f.quote, f.catalog); !errors.Is(err, errInjected) {
					t.Fatal(err)
				}
				f.uow.failAt = 0
			}
			order := confirm(t, f)
			work := due(t, f)
			if stage == "observation" {
				f.uow.failAt = f.uow.calls + 1
				if err := f.service.ObserveSend(ctx, work.RequestID, nil); !errors.Is(err, errInjected) {
					t.Fatal(err)
				}
				f.uow.failAt = 0
			}
			switch stage {
			case "proof":
				f.uow.failAt = f.uow.calls + 1
			case "ownership":
				f.uow.failAt = f.uow.calls + 2
			}
			result, err := f.service.HandleReply(ctx, work.RequestID, paid(work, false))
			if stage == "proof" || stage == "ownership" {
				if !errors.Is(err, errInjected) {
					t.Fatalf("injection did not fail: %+v %v", result, err)
				}
				f.uow.failAt = 0
				if stage == "ownership" {
					saved, savedErr := f.service.Get(ctx, f.scope, order.ID)
					if savedErr != nil || saved.State != domain.Paid {
						t.Fatalf("paid proof lost: %+v %v", saved, savedErr)
					}
					result, err = f.service.Reconcile(ctx, order.ID)
				} else {
					result, err = f.service.HandleReply(ctx, work.RequestID, paid(work, false))
				}
			}
			if err != nil || result.Order.State != domain.Completed {
				t.Fatalf("recovery: %+v %v", result, err)
			}
			entries, err := f.db.Entries(ctx, -1, f.quote.Character)
			if err != nil || len(entries) != 1 || entries[0].Quantity != 2 {
				t.Fatalf("recovery ownership: %+v %v", entries, err)
			}
		})
	}
}

func TestPaidOverflowRemainsRecoverableWithoutAnotherCharge(t *testing.T) {
	f := setup(t, false)
	ctx := context.Background()
	if err := f.db.WithinShop(ctx, func(tx app.Transaction) error {
		entries, err := inventory.Grant(ctx, tx.Inventory, core.NewID, -1, f.quote.Character, f.quote.Snapshot, 1)
		if err != nil {
			return err
		}
		entry := entries[0]
		entry.Quantity = math.MaxInt64
		return tx.Inventory.PutEntry(ctx, entry)
	}); err != nil {
		t.Fatal(err)
	}
	order := confirm(t, f)
	work := due(t, f)
	result, err := f.service.HandleReply(ctx, work.RequestID, paid(work, false))
	if !errors.Is(err, inventory.ErrOverflow) || result.Order.State != domain.Paid {
		t.Fatalf("paid overflow: %+v %v", result, err)
	}
	f.now = f.now.Add(time.Hour)
	recovery := due(t, f)
	if !recovery.Fulfill || recovery.Body != "" {
		t.Fatal("overflow recovery attempted another charge")
	}
	entries, err := f.db.Entries(ctx, -1, f.quote.Character)
	if err != nil || len(entries) != 1 {
		t.Fatalf("overflow ownership: %+v %v", entries, err)
	}
	if err = f.db.WithinShop(ctx, func(tx app.Transaction) error {
		entry := entries[0]
		entry.Quantity = 1
		return tx.Inventory.PutEntry(ctx, entry)
	}); err != nil {
		t.Fatal(err)
	}
	result, err = f.service.Reconcile(ctx, order.ID)
	if err != nil || result.Order.State != domain.Completed || result.Entries[0].Quantity != 3 {
		t.Fatalf("overflow resolved: %+v %v", result, err)
	}
}

func TestBankDisabledAndOutstandingIdentityMismatch(t *testing.T) {
	f := setup(t, false)
	ctx := context.Background()
	disabled := newService(t, f.db, f.clock, 0, 2)
	if _, err := disabled.Confirm(ctx, f.scope, f.quote, f.catalog); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("disabled bank: %v", err)
	}
	if work, err := disabled.DueWork(ctx, f.now, 1); err != nil || len(work) != 0 {
		t.Fatal("disabled bank transmitted work")
	}
	confirm(t, f)
	if err := disabled.CheckIdentity(ctx); err != nil {
		t.Fatalf("disable with obligations: %v", err)
	}
	for _, identity := range [][2]int64{{98, 2}, {99, 3}} {
		changed := newService(t, f.db, f.clock, identity[0], identity[1])
		if err := changed.CheckIdentity(ctx); err == nil {
			t.Fatal("changed bank/account accepted outstanding intent")
		}
	}
}

func TestConflictParksAndLateSuccessCanSettle(t *testing.T) {
	f := setup(t, true)
	confirm(t, f)
	work := due(t, f)
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"error":{"code":-32001,"message":"Request conflict"}}`, work.RequestID)
	result, err := f.service.HandleReply(context.Background(), work.RequestID, body)
	if err != nil || result.Order.State != domain.ReconciliationRequired {
		t.Fatalf("conflict: %+v %v", result, err)
	}
	f.now = f.now.Add(365 * 24 * time.Hour)
	if work, err := f.service.DueWork(context.Background(), f.now, 8); err != nil || len(work) != 0 {
		t.Fatalf("conflict retried automatically: %+v %v", work, err)
	}
	result, err = f.service.HandleReply(context.Background(), work.RequestID, paid(work, true))
	if err != nil || result.Order.State != domain.Completed {
		t.Fatalf("late conflict settlement: %+v %v", result, err)
	}
}

func TestReusedProofAndLearnedAccountMismatchRequireReconciliation(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			f := setup(t, false)
			confirm(t, f)
			first := due(t, f)
			if _, err := f.service.HandleReply(context.Background(), first.RequestID, paid(first, false)); err != nil {
				t.Fatal(err)
			}
			f.quote.ID = "second-confirmation"
			f.scope.UpdateID++
			confirm(t, f)
			second := due(t, f)
			body := paid(second, false)
			if mismatch {
				body = strings.ReplaceAll(body, `"senderAccountId":456`, `"senderAccountId":457`)
				body = strings.ReplaceAll(body, `"transactionId":"501"`, `"transactionId":"502"`)
			}
			result, err := f.service.HandleReply(context.Background(), second.RequestID, body)
			if err != nil || result.Order.State != domain.ReconciliationRequired {
				t.Fatalf("conflicting proof: %+v %v", result, err)
			}
			entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
			if err != nil || len(entries) != 1 || entries[0].Quantity != 2 {
				t.Fatalf("conflicting proof granted inventory: %+v %v", entries, err)
			}
		})
	}
}

func TestUnsolicitedResponseCannotCreateOrder(t *testing.T) {
	f := setup(t, false)
	if _, err := f.service.HandleReply(context.Background(), "foreign", `{"jsonrpc":"2.0","id":"foreign","result":{"data":{}}}`); err == nil {
		t.Fatal("unsolicited response accepted")
	}
	orders, err := f.service.List(context.Background(), f.scope)
	if err != nil || len(orders) != 0 {
		t.Fatal("unsolicited response created state")
	}
}

func TestVerifiedPaymentSurvivesRestartBeforeFulfillment(t *testing.T) {
	for _, sale := range []bool{false, true} {
		t.Run(fmt.Sprint(sale), func(t *testing.T) {
			f := setup(t, sale)
			order := confirm(t, f)
			work := due(t, f)
			f.uow.failAt = f.uow.calls + 2
			result, err := f.service.HandleReply(context.Background(), work.RequestID, paid(work, sale))
			if !errors.Is(err, errInjected) || (result.Order.State != domain.Paid && result.Order.State != domain.PayoutConfirmed) {
				t.Fatalf("paid proof boundary: %+v %v", result, err)
			}
			if err = f.db.Close(); err != nil {
				t.Fatal(err)
			}
			f.db, err = sqlite.Open(context.Background(), f.path)
			if err != nil {
				t.Fatal(err)
			}
			f.uow = &failingUOW{db: f.db}
			f.service = newService(t, f.uow, f.clock, 99, 2)
			f.now = f.now.Add(time.Hour)
			recovery := due(t, f)
			if !recovery.Fulfill || recovery.Body != "" || recovery.OrderID != order.ID {
				t.Fatal("restart attempted another financial request after verified payment")
			}
			result, err = f.service.Reconcile(context.Background(), order.ID)
			if err != nil || result.Order.State != domain.Completed {
				t.Fatalf("restart fulfillment: %+v %v", result, err)
			}
		})
	}
}

func TestExtendedBankOutagePreservesOriginalFinancialIntent(t *testing.T) {
	f := setup(t, true)
	order := confirm(t, f)
	first := due(t, f)
	for range 12 {
		if err := f.service.ObserveSend(context.Background(), first.RequestID, errors.New("bank offline")); err != nil {
			t.Fatal(err)
		}
		if immediate, err := f.service.DueWork(context.Background(), f.now, 8); err != nil || len(immediate) != 0 {
			t.Fatal("outage retry ignored backoff")
		}
		f.now = f.now.Add(time.Hour)
		retry := due(t, f)
		if retry.RequestID != first.RequestID || retry.Body != first.Body || retry.OrderID != order.ID {
			t.Fatal("outage retry changed financial intent")
		}
	}
	entries, err := f.db.Entries(context.Background(), -1, f.quote.Character)
	if err != nil || len(entries) != 1 || entries[0].Quantity != 5 || entries[0].Reserved != 2 {
		t.Fatalf("outage lost reservation: %+v %v", entries, err)
	}
}
