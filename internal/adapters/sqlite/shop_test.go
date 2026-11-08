package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/alexthvest/replipg/internal/application/grants"
	application "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

func testOrder(
	snapshot items.Snapshot,
	character core.ID,
	id core.ID,
	direction domain.Direction,
) domain.Order {
	state := domain.Created
	entry := core.ID("")
	if direction == domain.Sell {
		state = domain.Reserved
		entry = "entry"
	}

	return domain.Order{
		ID:           id,
		Confirmation: "confirmation-" + id,
		Quote: domain.Quote{
			ID:           "quote-" + id,
			Group:        -1,
			Player:       2,
			Character:    character,
			Entry:        entry,
			Direction:    direction,
			Snapshot:     snapshot,
			Quantity:     1,
			Unit:         "10",
			Total:        "10",
			BankID:       99,
			TreasuryID:   2,
			DeliveryChat: -1,
		},
		State: state,
	}
}

func createOrder(
	ctx context.Context,
	tx application.Transaction,
	order domain.Order,
	update int64,
) error {
	return tx.Orders.Create(ctx, order, core.Scope{
		Group:    order.Quote.Group,
		Actor:    order.Quote.Player,
		BotID:    123,
		UpdateID: update,
	})
}

func testRequest(order domain.Order) application.Request {
	return application.Request{
		ID:           "request-" + string(order.ID),
		OrderID:      order.ID,
		FinancialKey: "key-" + string(order.ID),
		Kind:         "invoice",
		Method:       "paymentRequest.public.v1",
		Params:       json.RawMessage(`{"amount":"10"}`),
		NextAttempt:  time.Unix(100, 0),
	}
}

func TestShopMigrationPreservesVersionThreeData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	raw, err := openDriver(path)
	if err != nil {
		t.Fatal(err)
	}

	old := &Database{db: raw}
	source := fstest.MapFS{}
	names, err := fs.Glob(migrations, "migrations/00[123]_*.sql")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range names {
		data, err := fs.ReadFile(migrations, name)
		if err != nil {
			t.Fatal(err)
		}
		source[name] = &fstest.MapFile{Data: data}
	}

	if err = old.migrate(ctx, source); err != nil {
		t.Fatal(err)
	}

	_, _, coin, _ := grantSetup(t)
	snapshotData, err := coin.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	replay := grants.Result{
		Group:    -1,
		Snapshot: coin,
		Quantity: 5,
	}
	replayData, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}

	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{sql: "INSERT INTO groups VALUES(-1,'ru')"},
		{sql: "INSERT INTO characters VALUES('character',-1,2,'Owner')"},
		{
			sql:  "INSERT INTO item_snapshots VALUES('snapshot','coin',?,'digest')",
			args: []any{snapshotData},
		},
		{sql: "INSERT INTO inventory_entries VALUES('entry',-1,'character','snapshot','stackable',5)"},
		{
			sql:  "INSERT INTO grant_operations VALUES(123,1,-1,?)",
			args: []any{replayData},
		},
	} {
		if _, err = raw.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	group, err := db.Group(ctx, -1)
	if err != nil || group.Language != "ru" {
		t.Fatalf("group after upgrade: %+v %v", group, err)
	}

	entries, err := db.Entries(ctx, -1, "character")
	if err != nil || len(entries) != 1 || entries[0].ID != "entry" ||
		entries[0].Quantity != 5 || entries[0].Snapshot != coin || entries[0].Reserved != 0 {
		t.Fatalf("ownership after upgrade: %+v %v", entries, err)
	}

	if err = db.WithinGrant(ctx, func(tx grants.Transaction) error {
		result, found, err := tx.Operations.Operation(ctx, grants.Key{
			BotID:    123,
			UpdateID: 1,
		})
		if err != nil || !found || !reflect.DeepEqual(result, replay) {
			t.Fatalf("grant replay after upgrade: %+v %v %v", result, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err = db.migrate(ctx, source); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("unsupported rollback: %v", err)
	}
}

func TestShopFrozenIntentReplayAndRecoveryPersistence(t *testing.T) {
	db, grantsService, coin, _ := grantSetup(t)
	ctx := context.Background()
	result, err := grantsService.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	order := testOrder(coin, result.Character.ID, "purchase", domain.Buy)
	financial := testRequest(order)
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := createOrder(ctx, tx, order, 10); err != nil {
			return err
		}
		return tx.Requests.CreateRequest(ctx, financial)
	}); err != nil {
		t.Fatal(err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		saved, found, err := tx.Orders.ByConfirmation(ctx, order.Confirmation)
		if err != nil || !found || saved != order {
			t.Fatalf("confirmation replay: %+v %v %v", saved, found, err)
		}

		duplicate := order
		duplicate.ID = "duplicate"
		if err := createOrder(ctx, tx, duplicate, 11); err == nil {
			t.Fatal("duplicate confirmation accepted")
		}

		changed := financial
		changed.Params = json.RawMessage(`{"amount":"11"}`)
		if err := tx.Requests.UpdateRequest(ctx, changed); err == nil {
			t.Fatal("financial payload mutation accepted")
		}
		if err := tx.Requests.Identity(ctx, 98, 2); err == nil {
			t.Fatal("changed bank allowed outstanding intent")
		}
		if err := tx.Requests.Identity(ctx, 99, 3); err == nil {
			t.Fatal("changed treasury allowed outstanding intent")
		}
		if err := tx.Requests.RecordAttempt(ctx, application.Attempt{
			RequestID: financial.ID,
			At:        time.Unix(100, 0),
			Sent:      true,
		}); err != nil {
			return err
		}
		financial.Attempts = 1
		financial.Unknown = true
		return tx.Requests.UpdateRequest(ctx, financial)
	}); err != nil {
		t.Fatal(err)
	}

	var path string
	if err = db.db.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	if err = reopened.WithinShop(ctx, func(tx application.Transaction) error {
		due, err := tx.Requests.Due(ctx, time.Unix(101, 0), 10)
		if err != nil || len(due) != 1 || !due[0].Unknown ||
			due[0].Attempts != 1 || !reflect.DeepEqual(due[0].Params, financial.Params) {
			t.Fatalf("durable due work: %+v %v", due, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestShopTransactionFailureCannotLoseIntent(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	granted, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	order := testOrder(coin, granted.Character.ID, "failure", domain.Buy)
	injected := errors.New("injected failure after durable insert")
	err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := createOrder(ctx, tx, order, 11); err != nil {
			return err
		}
		if err := tx.Requests.CreateRequest(ctx, testRequest(order)); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		_, found, err := tx.Orders.Order(ctx, order.ID)
		if err != nil || found {
			t.Fatalf("rolled back order exists: %v %v", found, err)
		}
		_, found, err = tx.Requests.Request(ctx, testRequest(order).ID)
		if err != nil || found {
			t.Fatalf("rolled back request exists: %v %v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestShopProofUniquenessAndMonotonicTransitions(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	granted, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	first := testOrder(coin, granted.Character.ID, "first", domain.Buy)
	second := testOrder(coin, granted.Character.ID, "second", domain.Buy)
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		for i, order := range []domain.Order{first, second} {
			if err := createOrder(ctx, tx, order, int64(10+i)); err != nil {
				return err
			}
			if err := tx.Requests.CreateRequest(ctx, testRequest(order)); err != nil {
				return err
			}
		}

		proof := application.Proof{
			BankID:        99,
			TransactionID: "txn",
			OrderID:       first.ID,
			RequestID:     testRequest(first).ID,
			Response:      json.RawMessage(`{"state":"paid"}`),
		}
		if err := tx.Requests.SaveProof(ctx, proof); err != nil {
			return err
		}
		if err := tx.Requests.SaveProof(ctx, proof); err != nil {
			return err
		}
		proof.OrderID = second.ID
		proof.RequestID = testRequest(second).ID
		if err := tx.Requests.SaveProof(ctx, proof); err == nil {
			t.Fatal("one transaction used for two orders")
		}
		if changed, err := tx.Orders.Transition(ctx, first.ID, domain.Created, domain.Paid, ""); err != nil || !changed {
			t.Fatalf("paid transition: %v %v", changed, err)
		}
		if changed, err := tx.Orders.Transition(ctx, first.ID, domain.Created, domain.AwaitingPayment, ""); err != nil || changed {
			t.Fatalf("stale transition: %v %v", changed, err)
		}
		if _, err := tx.Orders.Transition(ctx, first.ID, domain.Paid, domain.Failed, ""); err == nil {
			t.Fatal("paid proof regressed to failure")
		}
		if changed, err := tx.Orders.Transition(ctx, first.ID, domain.Paid, domain.Completed, ""); err != nil || !changed {
			t.Fatalf("completion transition: %v %v", changed, err)
		}
		if _, err := tx.Orders.Transition(ctx, first.ID, domain.Completed, domain.Paid, ""); err == nil {
			t.Fatal("completed order regressed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSaleReservationsCompeteAndSettleAtomically(t *testing.T) {
	for _, individual := range []bool{false, true} {
		t.Run(map[bool]string{false: "stack", true: "instance"}[individual], func(t *testing.T) {
			db, service, coin, stick := grantSetup(t)
			snapshot := coin
			owned := int64(10)
			quantity := int64(6)
			if individual {
				snapshot = stick
				owned = 1
				quantity = 1
			}

			ctx := context.Background()
			granted, err := service.Give(ctx, request(1, owned), snapshotCatalog{snapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}

			entry := granted.Entries[0]
			var mu sync.Mutex
			var winner domain.Order
			var wg sync.WaitGroup
			successes := 0
			for _, id := range []core.ID{"sale-a", "sale-b"} {
				wg.Add(1)
				go func(id core.ID) {
					defer wg.Done()
					order := testOrder(snapshot, entry.Character, id, domain.Sell)
					order.Quote.Entry = entry.ID
					order.Quote.Quantity = quantity
					order.Quote.Total, _ = order.Quote.Unit.Total(quantity)
					err := db.WithinShop(ctx, func(tx application.Transaction) error {
						update := int64(10)
						if id == "sale-b" {
							update = 11
						}
						if err := createOrder(ctx, tx, order, update); err != nil {
							return err
						}
						return tx.Reservations.Reserve(ctx, order)
					})
					if err == nil {
						mu.Lock()
						successes++
						winner = order
						mu.Unlock()
					}
				}(id)
			}

			wg.Wait()
			if successes != 1 {
				t.Fatalf("competing reservations succeeded %d times", successes)
			}

			entries, err := db.Entries(ctx, -1, entry.Character)
			if err != nil || len(entries) != 1 || entries[0].Quantity != owned ||
				entries[0].Reserved != quantity || entries[0].Available() != owned-quantity ||
				entries[0].Snapshot != snapshot {
				t.Fatalf("reserved ownership: %+v %v", entries, err)
			}

			if err = db.WithinShop(ctx, func(tx application.Transaction) error {
				financial := testRequest(winner)
				if err := tx.Requests.CreateRequest(ctx, financial); err != nil {
					return err
				}
				if err := tx.Requests.SaveProof(ctx, application.Proof{
					BankID:        99,
					TransactionID: "payout",
					OrderID:       winner.ID,
					RequestID:     financial.ID,
					Response:      json.RawMessage(`{"state":"paid"}`),
				}); err != nil {
					return err
				}

				changed, err := tx.Orders.Transition(ctx, winner.ID, domain.Reserved, domain.PayoutConfirmed, "")
				if err != nil || !changed {
					t.Fatalf("confirm payout: %v %v", changed, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			winner.State = domain.PayoutConfirmed
			injected := errors.New("ownership commit failed")
			if err = db.WithinShop(ctx, func(tx application.Transaction) error {
				if _, err := tx.Reservations.Consume(ctx, winner); err != nil {
					return err
				}
				if _, err := tx.Orders.Transition(ctx, winner.ID, domain.PayoutConfirmed, domain.Completed, ""); err != nil {
					return err
				}
				return injected
			}); !errors.Is(err, injected) {
				t.Fatal(err)
			}
			entries, err = db.Entries(ctx, -1, entry.Character)
			if err != nil || len(entries) != 1 || entries[0].Quantity != owned || entries[0].Reserved != quantity {
				t.Fatalf("failed consume changed ownership: %+v %v", entries, err)
			}

			if err = db.WithinShop(ctx, func(tx application.Transaction) error {
				if _, err := tx.Reservations.Consume(ctx, winner); err != nil {
					return err
				}

				_, err := tx.Orders.Transition(ctx, winner.ID, domain.PayoutConfirmed, domain.Completed, "")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			entries, err = db.Entries(ctx, -1, entry.Character)
			if err != nil || (individual && len(entries) != 0) ||
				(!individual && (len(entries) != 1 || entries[0].Quantity != 4 || entries[0].Reserved != 0)) {
				t.Fatalf("settled ownership: %+v %v", entries, err)
			}
		})
	}
}

func TestShopOwnershipAndImmutableDatabaseConstraints(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	granted, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	order := testOrder(coin, granted.Character.ID, "owned", domain.Buy)
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		foreign := order
		foreign.Quote.Player = 999
		if err := createOrder(ctx, tx, foreign, 10); err == nil {
			t.Fatal("order references another player's character")
		}
		foreign = order
		foreign.Quote.Group = -2
		foreign.Quote.DeliveryChat = -2
		if err := createOrder(ctx, tx, foreign, 11); err == nil {
			t.Fatal("order references another group's character")
		}
		if err := createOrder(ctx, tx, order, 12); err != nil {
			return err
		}
		return tx.Requests.CreateRequest(ctx, testRequest(order))
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = db.db.ExecContext(ctx, "UPDATE shop_orders SET treasury_id=3 WHERE id=?", order.ID); err == nil {
		t.Fatal("database allowed frozen treasury mutation")
	}

	if _, err = db.db.ExecContext(ctx, "UPDATE financial_requests SET params=? WHERE order_id=?", []byte(`{"amount":"99"}`), order.ID); err == nil {
		t.Fatal("database allowed frozen request mutation")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	candidate := testOrder(coin, granted.Character.ID, "canceled", domain.Buy)
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := createOrder(ctx, tx, candidate, 13); err != nil {
			return err
		}
		if err := tx.Requests.CreateRequest(ctx, testRequest(candidate)); err != nil {
			return err
		}
		cancel()
		return nil
	}); err == nil {
		t.Fatal("canceled commit acknowledged as successful")
	}

	if err = db.WithinShop(context.Background(), func(tx application.Transaction) error {
		_, found, err := tx.Orders.Order(context.Background(), candidate.ID)
		if err != nil || found {
			t.Fatalf("canceled commit retained intent: %v %v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReservationRestartReleaseAndLastStackUnit(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	granted, err := service.Give(ctx, request(1, 3), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	entry := granted.Entries[0]
	sale := testOrder(coin, entry.Character, "last-stack", domain.Sell)
	sale.Quote.Entry = entry.ID
	sale.Quote.Quantity = 3
	sale.Quote.Total = "30"
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := createOrder(ctx, tx, sale, 10); err != nil {
			return err
		}
		return tx.Reservations.Reserve(ctx, sale)
	}); err != nil {
		t.Fatal(err)
	}

	var path string
	if err = db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.Entries(ctx, -1, entry.Character)
	if err != nil || len(entries) != 1 || entries[0].ID != entry.ID ||
		entries[0].Snapshot != entry.Snapshot || entries[0].Quantity != 3 || entries[0].Reserved != 3 {
		t.Fatalf("restarted reservation changed identity: %+v %v", entries, err)
	}

	injected := errors.New("release rollback")
	err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := tx.Reservations.Release(ctx, sale.ID); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	entries, err = db.Entries(ctx, -1, entry.Character)
	if err != nil || entries[0].Reserved != 3 || entries[0].Quantity != 3 {
		t.Fatalf("failed release lost reservation: %+v %v", entries, err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := tx.Reservations.Release(ctx, sale.ID); err != nil {
			return err
		}

		available, found, err := tx.Reservations.Available(ctx, -1, entry.Character, entry.ID)
		if err != nil || !found || available.Available() != 3 || available.Quantity != 3 {
			t.Fatalf("release consumed ownership: %+v %v %v", available, found, err)
		}
		return tx.Reservations.Reserve(ctx, sale)
	}); err != nil {
		t.Fatal(err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		financial := testRequest(sale)
		if err := tx.Requests.CreateRequest(ctx, financial); err != nil {
			return err
		}
		if err := tx.Requests.SaveProof(ctx, application.Proof{
			BankID:        99,
			TransactionID: "last-stack-payout",
			OrderID:       sale.ID,
			RequestID:     financial.ID,
			Response:      json.RawMessage(`{"state":"paid"}`),
		}); err != nil {
			return err
		}
		if _, err := tx.Orders.Transition(ctx, sale.ID, domain.Reserved, domain.PayoutConfirmed, ""); err != nil {
			return err
		}
		sale.State = domain.PayoutConfirmed
		remaining, err := tx.Reservations.Consume(ctx, sale)
		if err != nil || len(remaining) != 0 {
			t.Fatalf("last stack consumption: %+v %v", remaining, err)
		}
		_, err = tx.Orders.Transition(ctx, sale.ID, domain.PayoutConfirmed, domain.Completed, "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entries, err = db.Entries(ctx, -1, entry.Character)
	if err != nil || len(entries) != 0 {
		t.Fatalf("last stack entry retained: %+v %v", entries, err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		saved, found, err := tx.Orders.Order(ctx, sale.ID)
		if err != nil || !found || saved.State != domain.Completed || saved.Quote.Snapshot != coin {
			t.Fatalf("historical snapshot lost: %+v %v %v", saved, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProofWriteFailureRetainsOriginalRecoveryAndReservation(t *testing.T) {
	db, service, coin, _ := grantSetup(t)
	ctx := context.Background()
	granted, err := service.Give(ctx, request(1, 2), snapshotCatalog{snapshot: coin})
	if err != nil {
		t.Fatal(err)
	}

	order := testOrder(coin, granted.Character.ID, "proof-failure", domain.Sell)
	order.Quote.Entry = granted.Entries[0].ID
	financial := testRequest(order)
	financial.Attempts = 1
	financial.Unknown = true
	proof := application.Proof{
		BankID:        99,
		TransactionID: "verified-payout",
		OrderID:       order.ID,
		RequestID:     financial.ID,
		Response:      json.RawMessage(`{"jsonrpc":"2.0","id":"known","result":{"data":{"transactionId":"verified-payout"}}}`),
	}
	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := createOrder(ctx, tx, order, 10); err != nil {
			return err
		}
		if err := tx.Reservations.Reserve(ctx, order); err != nil {
			return err
		}
		return tx.Requests.CreateRequest(ctx, financial)
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = db.db.ExecContext(ctx, `CREATE TRIGGER injected_proof_failure BEFORE INSERT ON financial_proofs BEGIN SELECT RAISE(ABORT,'proof storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	err = db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := tx.Requests.RecordResponse(ctx, financial.ID, proof.Response, "paid"); err != nil {
			return err
		}
		if err := tx.Requests.SaveProof(ctx, proof); err != nil {
			return err
		}

		_, err := tx.Orders.Transition(ctx, order.ID, domain.Reserved, domain.PayoutConfirmed, "")
		return err
	})
	if err == nil || errors.Is(err, application.ErrProofConflict) {
		t.Fatalf("transient proof failure must remain retryable: %v", err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		due, err := tx.Requests.Due(ctx, time.Unix(101, 0), 10)
		if err != nil || len(due) != 1 || due[0].ID != financial.ID ||
			due[0].FinancialKey != financial.FinancialKey || !due[0].Unknown {
			t.Fatalf("proof failure lost original financial intent: %+v %v", due, err)
		}

		entry, found, err := tx.Reservations.Available(ctx, order.Quote.Group, order.Quote.Character, order.Quote.Entry)
		if err != nil || !found || entry.Quantity != 2 || entry.Reserved != 1 {
			t.Fatalf("proof failure released or consumed items: %+v %v %v", entry, found, err)
		}

		saved, found, err := tx.Orders.Order(ctx, order.ID)
		if err != nil || !found || saved.State != domain.Reserved {
			t.Fatalf("proof failure changed financial state: %+v %v %v", saved, found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err = db.db.ExecContext(ctx, "DROP TRIGGER injected_proof_failure"); err != nil {
		t.Fatal(err)
	}

	if err = db.WithinShop(ctx, func(tx application.Transaction) error {
		return tx.Requests.SaveProof(ctx, proof)
	}); err != nil {
		t.Fatalf("original proof cannot recover: %v", err)
	}
}

func TestVerifiedBankAccountBindingsAreDurableAndCannotChange(t *testing.T) {
	db, _, _, _ := grantSetup(t)
	ctx := context.Background()
	if err := db.WithinShop(ctx, func(tx application.Transaction) error {
		if err := tx.Requests.BindAccount(ctx, 99, 2, 456); err != nil {
			return err
		}
		if err := tx.Requests.BindAccount(ctx, 99, 2, 456); err != nil {
			return err
		}
		if err := tx.Requests.BindAccount(ctx, 99, 2, 457); !errors.Is(err, application.ErrProofConflict) {
			t.Fatalf("changed account binding accepted: %v", err)
		}
		if err := tx.Requests.BindAccount(ctx, 99, 3, 456); !errors.Is(err, application.ErrProofConflict) {
			t.Fatalf("shared player account accepted: %v", err)
		}
		return tx.Requests.BindAccount(ctx, 100, 2, 457)
	}); err != nil {
		t.Fatal(err)
	}

	var path string
	if err := db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = reopened.Close()
	}()
	if err = reopened.WithinShop(ctx, func(tx application.Transaction) error {
		account, found, err := tx.Requests.AccountBinding(ctx, 99, 2)
		if err != nil || !found || account != 456 {
			t.Fatalf("binding changed across restart: %d %v %v", account, found, err)
		}
		_, found, err = tx.Requests.AccountBinding(ctx, 99, 3)
		if err != nil || found {
			t.Fatalf("failed conflicting binding persisted: %v %v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestComponentShopRecoveryAndLegacyQuoteBytes(t *testing.T) {
	for _, classified := range []bool{false, true} {
		t.Run(fmt.Sprint(classified), func(t *testing.T) {
			db, service, _, _ := grantSetup(t)
			ctx := context.Background()
			var snapshot items.Snapshot
			if err := json.Unmarshal([]byte(preComponentSnapshot), &snapshot); err != nil {
				t.Fatal(err)
			}

			if classified {
				snapshot.Components = items.Components{
					Material: true,
					Weapon:   true,
				}
			}

			granted, err := service.Give(ctx, request(1, 5), snapshotCatalog{snapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}

			sale := testOrder(snapshot, granted.Character.ID, "component-sale", domain.Sell)
			sale.Quote.Entry = granted.Entries[0].ID
			financial := testRequest(sale)
			financial.Unknown = true
			financial.Attempts = 1
			if classified {
				if err := db.WithinShop(ctx, func(tx application.Transaction) error {
					if err := createOrder(ctx, tx, sale, 10); err != nil {
						return err
					}
					return tx.Reservations.Reserve(ctx, sale)
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				// Literal pre-component quote layout with dynamic durable identities only.
				quote := `{"ID":"quote-component-sale","Group":-1,"Player":2,"Character":"` + string(granted.Character.ID) + `","Entry":"` + string(granted.Entries[0].ID) + `","Direction":"sell","Snapshot":` + preComponentSnapshot + `,"Quantity":1,"Unit":"10","Total":"10","BankID":99,"TreasuryID":2,"DeliveryChat":-1}`
				if _, err := db.db.Exec(`INSERT INTO shop_orders VALUES('component-sale','confirmation-component-sale',-1,?,2,'sell',?,'reserved','',99,2,123,10,0)`, granted.Character.ID, []byte(quote)); err != nil {
					t.Fatal(err)
				}

				if err := db.WithinShop(ctx, func(tx application.Transaction) error {
					return tx.Reservations.Reserve(ctx, sale)
				}); err != nil {
					t.Fatal(err)
				}
			}

			if err := db.WithinShop(ctx, func(tx application.Transaction) error {
				return tx.Requests.CreateRequest(ctx, financial)
			}); err != nil {
				t.Fatal(err)
			}

			var original []byte
			if err := db.db.QueryRow("SELECT quote_json FROM shop_orders WHERE id=?", sale.ID).Scan(&original); err != nil {
				t.Fatal(err)
			}

			db, _ = reopenComponentDatabase(t, db)
			for range 2 {
				if err := db.WithinShop(ctx, func(tx application.Transaction) error {
					recovered, found, err := tx.Orders.ByConfirmation(ctx, sale.Confirmation)
					if err != nil || !found || recovered != sale {
						t.Fatalf("saved order changed: %+v %v %v", recovered, found, err)
					}

					due, err := tx.Requests.Due(ctx, time.Unix(101, 0), 10)
					if err != nil || len(due) != 1 || due[0].ID != financial.ID || !due[0].Unknown || due[0].Attempts != 1 {
						t.Fatalf("recovery changed financial intent: %+v %v", due, err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}

			entries, err := db.Entries(ctx, -1, granted.Character.ID)
			if err != nil || len(entries) != 1 || entries[0].Snapshot != snapshot || entries[0].Quantity != 5 || entries[0].Reserved != 1 || entries[0].ID != granted.Entries[0].ID {
				t.Fatalf("recovery changed reserved ownership: %+v %v", entries, err)
			}

			var after []byte
			if err := db.db.QueryRow("SELECT quote_json FROM shop_orders WHERE id=?", sale.ID).Scan(&after); err != nil || !bytes.Equal(original, after) {
				t.Fatalf("historical quote rewritten: %s %v", after, err)
			}
		})
	}
}
