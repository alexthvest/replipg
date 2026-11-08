package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

type bankEnvelope struct {
	Version string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func payoutData(id, amount string) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"transactionId":   id,
			"senderAccountId": 2,
			"recipient": map[string]any{
				"accountId":      456,
				"telegramUserId": 2,
			},
			"amount":    amount,
			"createdAt": "2026-11-08T12:00:00Z",
		},
	}
}

func openSale(
	t *testing.T,
	ctx context.Context,
	router *telegram.Router,
	fake *shopTestBot,
	update int64,
) (int, core.View) {
	t.Helper()
	router.Handle(ctx, message(update, -1, 2, "/shop", 0))
	messageID := len(fake.views)
	menu := waitMenu(t, fake.fakeBot, -1, messageID, nil)
	return messageID, menu
}

func reviewSale(
	t *testing.T,
	ctx context.Context,
	router *telegram.Router,
	fake *shopTestBot,
	messageID int,
	menu core.View,
	update int64,
	item, quantity string,
) core.View {
	t.Helper()
	router.Handle(ctx, callback(update, -1, 2, messageID, namedItemButton(t, menu, item)))
	card := changedMenu(t, fake.fakeBot, -1, messageID, menu)
	router.Handle(ctx, callback(update+1, -1, 2, messageID, buttonData(t, card, "Sell")))
	review := changedMenu(t, fake.fakeBot, -1, messageID, card)
	wanted, err := strconv.Atoi(quantity)
	if err != nil {
		t.Fatal(err)
	}
	for current := 1; current < wanted; current++ {
		router.Handle(ctx, callback(update+int64(current)+1, -1, 2, messageID, buttonData(t, review, "+")))
		review = changedMenu(t, fake.fakeBot, -1, messageID, review)
	}
	return review
}

func TestAdminGrantedSalesCompeteAndRecoverOriginalPayoutAfterRestart(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	cfg.AssetsPath = acceptanceShopAssets(t)
	var lost bankEnvelope
	effects := map[string]int{}
	applyEffect := func(request bankEnvelope) {
		var params struct {
			Key string `json:"idempotencyKey"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.Key == "" {
			t.Fatalf("missing frozen payout key: %s %v", request.Params, err)
		}
		if _, exists := effects[params.Key]; !exists {
			effects[params.Key] = 1
		}
	}
	for run := 0; run < 2; run++ {
		ctx, cancel := context.WithCancel(context.Background())
		fake := &shopTestBot{
			fakeBot:  &fakeBot{},
			requests: make(chan bankEnvelope, 32),
		}
		fake.script = func(ctx context.Context, router *telegram.Router) {
			defer cancel()
			if run == 0 {
				router.Handle(ctx, message(1, -1, 1, "/give bread 3", 2))
				router.Handle(ctx, message(2, -1, 1, "/give stick 1", 2))
				router.Handle(ctx, message(3, -1, 2, "/inventory", 0))
				inventoryID := len(fake.views)
				inventory := waitMenu(t, fake.fakeBot, -1, inventoryID, nil)
				router.Handle(ctx, callback(4, -1, 2, inventoryID, namedItemButton(t, inventory, "Bread")))
				inspection := changedMenu(t, fake.fakeBot, -1, inventoryID, inventory)

				firstID, first := openSale(t, ctx, router, fake, 10)
				firstReview := reviewSale(t, ctx, router, fake, firstID, first, 12, "Bread", "3")
				competingID, competing := openSale(t, ctx, router, fake, 20)
				competingReview := reviewSale(t, ctx, router, fake, competingID, competing, 22, "Bread", "3")
				router.Handle(ctx, callback(30, -1, 2, firstID, buttonData(t, firstReview, "Confirm")))
				router.Handle(ctx, callback(31, -1, 2, competingID, buttonData(t, competingReview, "Confirm")))
				reserved := changedMenu(t, fake.fakeBot, -1, inventoryID, inspection)
				if !strings.Contains(textOf(reserved), "Reserved") || !strings.Contains(textOf(reserved), "Available") {
					t.Fatal("sale did not refresh inventory reservation labels")
				}
				stackRequest := nextBankRequest(t, fake)
				if stackRequest.Method != bank.Transfer || !strings.Contains(string(stackRequest.Params), `"amount":"15"`) {
					t.Fatalf("wrong stack payout: %+v", stackRequest)
				}
				applyEffect(stackRequest)
				router.Handle(ctx, bankReply(t, stackRequest.ID, payoutData("sale-stack", "15")))
				waitMenu(t, fake.fakeBot, -1, inventoryID, func(view core.View) bool {
					return strings.Contains(textOf(view), "Stick") && !strings.Contains(textOf(view), "Bread") && len(view.Rows[0]) == 1
				})
				waitMenu(t, fake.fakeBot, -1, firstID, func(view core.View) bool {
					return strings.Contains(textOf(view), "Completed")
				})
				waitMenu(t, fake.fakeBot, -1, competingID, func(view core.View) bool {
					return strings.Contains(textOf(view), "Bread") && hasShopButton(view, "Buy") && !hasShopButton(view, "Sell")
				})

				instanceID, instance := openSale(t, ctx, router, fake, 40)
				instanceReview := reviewSale(t, ctx, router, fake, instanceID, instance, 42, "Stick", "1")
				otherID, other := openSale(t, ctx, router, fake, 50)
				otherReview := reviewSale(t, ctx, router, fake, otherID, other, 52, "Stick", "1")
				router.Handle(ctx, callback(60, -1, 2, instanceID, buttonData(t, instanceReview, "Confirm")))
				router.Handle(ctx, callback(61, -1, 2, otherID, buttonData(t, otherReview, "Confirm")))
				lost = nextBankRequest(t, fake)
				if lost.Method != bank.Transfer || !strings.Contains(string(lost.Params), `"amount":"5"`) {
					t.Fatalf("wrong instance payout: %+v", lost)
				}
				applyEffect(lost) // The bank pays, but its response is lost.
				return
			}

			// Menus are reopened after restart; durable ownership and reservation
			// remain until exact original-key recovery proves the prior payout.
			router.Handle(ctx, message(100, -1, 2, "/inventory", 0))
			inventoryID := len(fake.views)
			inventory := waitMenu(t, fake.fakeBot, -1, inventoryID, nil)
			router.Handle(ctx, callback(101, -1, 2, inventoryID, inventory.Rows[0][0].Data))
			inspection := changedMenu(t, fake.fakeBot, -1, inventoryID, inventory)
			if !strings.Contains(textOf(inspection), "Reserved") {
				t.Fatal("restart lost the unresolved sale reservation")
			}
			saleID, sale := openSale(t, ctx, router, fake, 110)
			router.Handle(ctx, callback(111, -1, 2, saleID, namedItemButton(t, sale, "Stick")))
			sale = changedMenu(t, fake.fakeBot, -1, saleID, sale)
			if hasShopButton(sale, "Sell") {
				t.Fatal("reserved instance remained available for another sale")
			}
			router.Handle(ctx, callback(112, -1, 2, saleID, buttonData(t, sale, "My orders")))
			orders := changedMenu(t, fake.fakeBot, -1, saleID, sale)
			router.Handle(ctx, callback(113, -1, 2, saleID, buttonData(t, orders, "1")))
			waitMenu(t, fake.fakeBot, -1, saleID, func(view core.View) bool {
				return strings.Contains(textOf(view), "Payout pending")
			})
			retry := nextBankRequest(t, fake)
			if retry.ID != lost.ID || retry.Method != lost.Method || string(retry.Params) != string(lost.Params) {
				t.Fatalf("restart replaced financial intent: original=%+v retry=%+v", lost, retry)
			}
			applyEffect(retry)
			paid := bankReply(t, retry.ID, payoutData("sale-instance", "5"))
			router.Handle(ctx, paid)
			router.Handle(ctx, paid)
			waitMenu(t, fake.fakeBot, -1, inventoryID, func(view core.View) bool {
				return strings.Contains(textOf(view), "empty")
			})
			waitMenu(t, fake.fakeBot, -1, saleID, func(view core.View) bool {
				return strings.Contains(textOf(view), "Completed")
			})
		}
		deps := defaults()
		deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
			return fake, nil
		}
		application, err := NewWith(cfg, logger(), deps)
		if err != nil {
			t.Fatal(err)
		}
		if err = application.Run(ctx); err != nil {
			t.Fatal(err)
		}
		db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := db.LoadInventory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			if len(entries) != 1 || entries[0].Snapshot.CatalogID != "stick" || entries[0].Quantity != 1 || entries[0].Reserved != 1 {
				t.Fatalf("lost payout consumed or released ownership: %+v", entries)
			}
			err = db.WithinShop(context.Background(), func(tx appshop.Transaction) error {
				request, _, err := tx.Requests.Request(context.Background(), lost.ID)
				if err != nil {
					return err
				}
				request.NextAttempt = time.Unix(0, 0)
				return tx.Requests.UpdateRequest(context.Background(), request)
			})
			if err != nil {
				t.Fatal(err)
			}
		} else if len(entries) != 0 {
			t.Fatalf("confirmed final-unit sale left ownership behind: %+v", entries)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(effects) != 2 {
		t.Fatalf("expected one payout for each of two orders: %+v", effects)
	}
}

func acceptanceShopAssets(t *testing.T) string {
	t.Helper()
	root := appAssets(t)
	dir := filepath.Join(root, "shop")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../assets/shop/schema.cue")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "schema.cue"), schema, 0600); err != nil {
		t.Fatal(err)
	}
	for _, offer := range []struct {
		id   string
		mode string
	}{
		{
			id:   "bread",
			mode: "stackable",
		},
		{
			id:   "stick",
			mode: "individual",
		},
	} {
		data := fmt.Sprintf("package shop\n%s: {mode: %q, buyPrice: \"10\", sellPrice: \"5\"}\n", offer.id, offer.mode)
		if err = os.WriteFile(filepath.Join(dir, offer.id+".cue"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func paidInvoiceData() map[string]any {
	return map[string]any{
		"data": map[string]any{
			"requestId": "invoice-one",
			"state":     "paid",
			"transaction": map[string]any{
				"transactionId":   "purchase-one",
				"senderAccountId": 456,
				"recipient":       map[string]any{"accountId": 2},
				"amount":          "10",
				"createdAt":       "2026-11-08T12:00:00Z",
			},
		},
	}
}

func TestPurchaseSettlementRefreshesPendingInventoryAndOrdersEdits(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	cfg.AssetsPath = acceptanceShopAssets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &shopTestBot{
		fakeBot:  &fakeBot{},
		requests: make(chan bankEnvelope, 32),
	}
	var inventoryMessage atomic.Int64
	var delay atomic.Bool
	var active atomic.Int32
	var overlap atomic.Bool
	delayed := make(chan core.View, 1)
	release := make(chan struct{})
	fake.afterEdit = func(ctx context.Context, group core.GroupID, message int, view core.View) error {
		if group != -1 || int64(message) != inventoryMessage.Load() {
			return nil
		}
		if active.Add(1) > 1 {
			overlap.Store(true)
		}
		defer active.Add(-1)
		if strings.Contains(textOf(view), "Bread") && delay.CompareAndSwap(true, false) {
			// Telegram has applied the edit; delay its acknowledgement while a
			// player operates the newly visible buttons.
			delayed <- view
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 2, "/shop", 0))
		shopID := len(fake.views)
		catalog := waitMenu(t, fake.fakeBot, -1, shopID, nil)
		router.Handle(ctx, callback(2, -1, 2, shopID, buttonData(t, catalog, "1")))
		card := changedMenu(t, fake.fakeBot, -1, shopID, catalog)
		router.Handle(ctx, callback(200, -1, 2, shopID, buttonData(t, card, "Buy")))
		review := changedMenu(t, fake.fakeBot, -1, shopID, card)
		confirm := buttonData(t, review, "Confirm")
		router.Handle(ctx, callback(3, -1, 2, shopID, confirm))
		router.Handle(ctx, callback(4, -1, 2, shopID, confirm))
		request := nextBankRequest(t, fake)
		if request.Method != bank.Invoice {
			t.Fatalf("expected invoice, got %s", request.Method)
		}
		router.Handle(ctx, bankReply(t, request.ID, map[string]any{
			"data":      map[string]any{"requestId": "invoice-one", "state": "pending"},
			"operation": map[string]any{"actionId": bank.Invoice, "operationId": "operation-one"},
		}))
		router.Handle(ctx, message(5, -1, 2, "/inventory", 0))
		inventoryID := len(fake.views)
		inventoryMessage.Store(int64(inventoryID))
		pending := waitMenu(t, fake.fakeBot, -1, inventoryID, nil)
		if !strings.Contains(textOf(pending), "empty") {
			t.Fatal("pending invoice granted items")
		}
		delay.Store(true)
		paid := bankReply(t, request.ID, paidInvoiceData())
		router.Handle(ctx, paid)
		router.Handle(ctx, paid)
		var refreshed core.View
		select {
		case refreshed = <-delayed:
		case <-time.After(5 * time.Second):
			t.Fatal("paid purchase did not refresh the existing inventory message")
		}
		router.Handle(ctx, callback(6, -1, 2, inventoryID, refreshed.Rows[0][0].Data))
		close(release)
		waitMenu(t, fake.fakeBot, -1, inventoryID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Bread") && len(view.Rows) == 1 && len(view.Rows[0]) == 1
		})
		waitMenu(t, fake.fakeBot, -1, shopID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Completed")
		})
		if overlap.Load() {
			t.Fatal("menu edits overlapped on the same message")
		}
		if len(fake.views) != 2 {
			t.Fatal("settlement posted replacement messages")
		}
		select {
		case extra := <-fake.requests:
			t.Fatalf("duplicate confirmation/settlement sent another request: %+v", extra)
		default:
		}
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Snapshot.CatalogID != "bread" || entries[0].Quantity != 1 {
		t.Fatalf("expected exactly one purchase grant: %+v %v", entries, err)
	}
}

type shopTestBot struct {
	*fakeBot
	requests  chan bankEnvelope
	afterEdit func(context.Context, core.GroupID, int, core.View) error
}

func (b *shopTestBot) SendBank(ctx context.Context, destination int64, text string) error {
	if destination != 900 {
		return fmt.Errorf("unexpected bank destination %d", destination)
	}

	var request bankEnvelope
	if err := json.Unmarshal([]byte(text), &request); err != nil {
		return err
	}
	select {
	case b.requests <- request:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *shopTestBot) Edit(ctx context.Context, group core.GroupID, message int, view core.View) error {
	if err := b.fakeBot.Edit(ctx, group, message, view); err != nil {
		return err
	}
	if b.afterEdit != nil {
		return b.afterEdit(ctx, group, message, view)
	}
	return nil
}

func nextBankRequest(t *testing.T, bot *shopTestBot) bankEnvelope {
	t.Helper()
	select {
	case request := <-bot.requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("durable bank work was not sent")
		return bankEnvelope{}
	}
}

func bankReply(t *testing.T, id string, result any) *models.Update {
	t.Helper()
	data, err := json.Marshal(struct {
		Version string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  any    `json:"result"`
	}{
		Version: "2.0",
		ID:      id,
		Result:  result,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &models.Update{
		ID: 9000,
		Message: &models.Message{
			ID:   9000,
			Date: 1,
			Chat: models.Chat{
				ID:   900,
				Type: models.ChatTypePrivate,
			},
			From: &models.User{
				ID:    900,
				IsBot: true,
			},
			Text: string(data),
		},
	}
}

func buttonData(t *testing.T, view core.View, caption string) string {
	t.Helper()
	for _, row := range view.Rows {
		for _, button := range row {
			if button.Caption == caption {
				return button.Data
			}
		}
	}
	t.Fatalf("button %q missing from %s", caption, textOf(view))
	return ""
}

func hasShopButton(view core.View, caption string) bool {
	for _, row := range view.Rows {
		for _, button := range row {
			if button.Caption == caption {
				return true
			}
		}
	}
	return false
}

func namedItemButton(t *testing.T, view core.View, name string) string {
	t.Helper()
	for _, line := range strings.Split(textOf(view), "\n") {
		prefix, item, found := strings.Cut(line, ". ")
		if found && strings.HasPrefix(item, name) {
			return buttonData(t, view, prefix)
		}
	}
	t.Fatalf("item %q missing from %s", name, textOf(view))
	return ""
}
