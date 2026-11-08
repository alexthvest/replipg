package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	application "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

type shopTransaction struct {
	*grantTransaction
}

func (d *Database) WithinShop(
	ctx context.Context,
	fn func(application.Transaction) error,
) error {
	if fn == nil {
		return errors.New("shop transaction callback required")
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin shop transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	ports := &shopTransaction{
		grantTransaction: &grantTransaction{
			tx:     tx,
			digest: items.Digest,
		},
	}
	if err = fn(application.Transaction{
		Orders:       ports,
		Requests:     ports,
		Reservations: ports,
		Inventory:    ports,
		Characters:   ports,
	}); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit shop transaction: %w", err)
	}
	return nil
}

const orderQuery = `SELECT id,confirmation,quote_json,state,diagnostic FROM shop_orders`

func scanOrder(row scanner) (domain.Order, error) {
	var order domain.Order
	var data []byte
	if err := row.Scan(&order.ID, &order.Confirmation, &data, &order.State, &order.Diagnostic); err != nil {
		return order, err
	}

	if err := json.Unmarshal(data, &order.Quote); err != nil {
		return order, fmt.Errorf("decode order quote: %w", err)
	}
	return order, order.Quote.Validate()
}

func (t *shopTransaction) Order(ctx context.Context, id core.ID) (domain.Order, bool, error) {
	order, err := scanOrder(t.tx.QueryRowContext(ctx, orderQuery+" WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return order, false, nil
	}
	return order, err == nil, err
}

func (t *shopTransaction) ByConfirmation(
	ctx context.Context,
	confirmation core.ID,
) (domain.Order, bool, error) {
	order, err := scanOrder(t.tx.QueryRowContext(ctx, orderQuery+" WHERE confirmation=?", confirmation))
	if errors.Is(err, sql.ErrNoRows) {
		return order, false, nil
	}
	return order, err == nil, err
}

func (t *shopTransaction) Create(ctx context.Context, order domain.Order, scope core.Scope) error {
	if err := order.Quote.Validate(); err != nil {
		return err
	}
	if order.ID == "" || order.Confirmation == "" || scope.Group != order.Quote.Group || scope.Actor != order.Quote.Player {
		return errors.New("invalid shop order identity")
	}

	if (order.Quote.Direction == domain.Buy && order.State != domain.Created) || (order.Quote.Direction == domain.Sell && order.State != domain.Reserved) {
		return errors.New("invalid initial shop state")
	}

	data, err := json.Marshal(order.Quote)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(
		ctx,
		`INSERT INTO shop_orders
  (id,confirmation,group_id,character_id,user_id,direction,quote_json,state,diagnostic,bank_id,treasury_id,bot_id,update_id,next_attempt)
  VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		order.ID,
		order.Confirmation,
		order.Quote.Group,
		order.Quote.Character,
		order.Quote.Player,
		order.Quote.Direction,
		data,
		order.State,
		order.Diagnostic,
		order.Quote.BankID,
		order.Quote.TreasuryID,
		scope.BotID,
		scope.UpdateID,
		0,
	)
	if err != nil {
		return fmt.Errorf("create shop order: %w", err)
	}
	return nil
}

func validTransition(from, to domain.State) bool {
	if from == to {
		return true
	}

	switch from {
	case domain.Completed, domain.Failed, domain.Rejected:
		return false
	case domain.Paid, domain.PayoutConfirmed:
		return to == domain.Completed
	}

	switch to {
	case domain.ReconciliationRequired, domain.Rejected, domain.Failed:
		return true
	case domain.Requesting:
		return from == domain.Created
	case domain.AwaitingPayment:
		return from == domain.Created || from == domain.Requesting || from == domain.AwaitingPayment || from == domain.ReconciliationRequired
	case domain.Paid:
		return from == domain.Created || from == domain.Requesting || from == domain.AwaitingPayment || from == domain.ReconciliationRequired
	case domain.PayoutRequesting:
		return from == domain.Reserved
	case domain.PayoutConfirmed:
		return from == domain.Reserved || from == domain.PayoutRequesting || from == domain.ReconciliationRequired
	}
	return false
}

func (t *shopTransaction) Transition(
	ctx context.Context,
	id core.ID,
	from, to domain.State,
	diagnostic string,
) (bool, error) {
	if !validTransition(from, to) {
		return false, errors.New("invalid shop order transition")
	}

	result, err := t.tx.ExecContext(
		ctx,
		`UPDATE shop_orders SET state=?,diagnostic=? WHERE id=? AND state=?
 AND (state NOT IN ('paid','payout-confirmed') OR ?='completed')
 AND (?!='paid' OR direction='buy') AND (?!='payout-confirmed' OR direction='sell')`,
		to,
		diagnostic,
		id,
		from,
		to,
		to,
		to,
	)
	if err != nil {
		return false, fmt.Errorf("transition shop order: %w", err)
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (t *shopTransaction) List(
	ctx context.Context,
	group core.GroupID,
	player core.UserID,
) ([]domain.Order, error) {
	rows, err := t.tx.QueryContext(ctx, orderQuery+" WHERE group_id=? AND user_id=? ORDER BY rowid DESC", group, player)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var orders []domain.Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

const requestQuery = `SELECT transport_id,order_id,financial_key,kind,method,params,next_attempt,attempts,unknown,payment_id,sender_account_id FROM financial_requests`

func scanRequest(row scanner) (application.Request, error) {
	var request application.Request
	var next int64
	err := row.Scan(
		&request.ID,
		&request.OrderID,
		&request.FinancialKey,
		&request.Kind,
		&request.Method,
		&request.Params,
		&next,
		&request.Attempts,
		&request.Unknown,
		&request.PaymentID,
		&request.SenderAccountID,
	)
	request.NextAttempt = time.UnixMilli(next)
	return request, err
}

func (t *shopTransaction) Request(ctx context.Context, id string) (application.Request, bool, error) {
	request, err := scanRequest(t.tx.QueryRowContext(ctx, requestQuery+" WHERE transport_id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return request, false, nil
	}
	return request, err == nil, err
}

func (t *shopTransaction) CreateRequest(ctx context.Context, request application.Request) error {
	if request.ID == "" || request.OrderID == "" || request.FinancialKey == "" || request.Method == "" || request.Kind == "" || !json.Valid(request.Params) {
		return errors.New("invalid bank request intent")
	}

	_, err := t.tx.ExecContext(
		ctx,
		`INSERT INTO financial_requests
  (transport_id,order_id,financial_key,kind,method,params,next_attempt,attempts,unknown,payment_id,sender_account_id)
  VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		request.ID,
		request.OrderID,
		request.FinancialKey,
		request.Kind,
		request.Method,
		[]byte(request.Params),
		request.NextAttempt.UnixMilli(),
		request.Attempts,
		request.Unknown,
		request.PaymentID,
		request.SenderAccountID,
	)
	if err != nil {
		return fmt.Errorf("create bank request: %w", err)
	}
	return nil
}

func (t *shopTransaction) UpdateRequest(ctx context.Context, request application.Request) error {
	saved, exists, err := t.Request(ctx, request.ID)
	if err != nil {
		return err
	}
	if !exists || saved.OrderID != request.OrderID || saved.FinancialKey != request.FinancialKey || saved.Kind != request.Kind || saved.Method != request.Method || !bytes.Equal(saved.Params, request.Params) {
		return errors.New("bank request intent is immutable")
	}
	if request.Attempts < saved.Attempts || (saved.PaymentID != "" && request.PaymentID != saved.PaymentID) || (saved.SenderAccountID != 0 && request.SenderAccountID != saved.SenderAccountID) {
		return errors.New("bank request evidence cannot regress")
	}

	_, err = t.tx.ExecContext(
		ctx,
		`UPDATE financial_requests SET next_attempt=?,attempts=?,unknown=?,payment_id=?,sender_account_id=? WHERE transport_id=?`,
		request.NextAttempt.UnixMilli(),
		request.Attempts,
		request.Unknown,
		request.PaymentID,
		request.SenderAccountID,
		request.ID,
	)
	return err
}

func (t *shopTransaction) Due(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]application.Request, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid bank due work limit")
	}

	rows, err := t.tx.QueryContext(
		ctx,
		requestQuery+` WHERE next_attempt<=? AND order_id IN
  (SELECT id FROM shop_orders WHERE state NOT IN ('completed','failed','rejected')) ORDER BY next_attempt,transport_id LIMIT ?`,
		now.UnixMilli(),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var requests []application.Request
	for rows.Next() {
		request, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (t *shopTransaction) RecordAttempt(ctx context.Context, attempt application.Attempt) error {
	outcome := "unknown"
	if attempt.Sent {
		outcome = "sent"
	}

	_, err := t.tx.ExecContext(
		ctx,
		`INSERT INTO financial_attempts(transport_id,attempted_at,outcome,diagnostic) VALUES(?,?,?,?)`,
		attempt.RequestID,
		attempt.At.UnixMilli(),
		outcome,
		attempt.Diagnostic,
	)
	return err
}

func (t *shopTransaction) RecordResponse(
	ctx context.Context,
	id string,
	response json.RawMessage,
	outcome string,
) error {
	if !json.Valid(response) {
		return errors.New("invalid bank response JSON")
	}

	result, err := t.tx.ExecContext(
		ctx,
		"UPDATE financial_requests SET response=?,outcome=? WHERE transport_id=?",
		[]byte(response),
		outcome,
		id,
	)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("unsolicited bank response")
	}
	return nil
}

func (t *shopTransaction) SaveProof(ctx context.Context, proof application.Proof) error {
	if proof.BankID <= 0 || proof.TransactionID == "" || proof.OrderID == "" || proof.RequestID == "" || !json.Valid(proof.Response) {
		return errors.New("invalid bank proof")
	}

	order, exists, err := t.Order(ctx, proof.OrderID)
	if err != nil {
		return err
	}
	if !exists || order.Quote.BankID != proof.BankID {
		return errors.New("foreign bank proof")
	}

	request, exists, err := t.Request(ctx, proof.RequestID)
	if err != nil {
		return err
	}
	if !exists || request.OrderID != proof.OrderID {
		return errors.New("foreign request proof")
	}

	var transactionID string
	var bankID int64
	var data []byte
	err = t.tx.QueryRowContext(
		ctx,
		"SELECT bank_id,transaction_id,response FROM financial_proofs WHERE order_id=?",
		proof.OrderID,
	).Scan(&bankID, &transactionID, &data)
	if err == nil {
		if bankID != proof.BankID || transactionID != proof.TransactionID {
			return fmt.Errorf("%w: order already has another transaction", application.ErrProofConflict)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var provenOrder core.ID
	err = t.tx.QueryRowContext(
		ctx,
		"SELECT order_id FROM financial_proofs WHERE bank_id=? AND transaction_id=?",
		proof.BankID,
		proof.TransactionID,
	).Scan(&provenOrder)
	if err == nil {
		return fmt.Errorf("%w: transaction belongs to order %s", application.ErrProofConflict, provenOrder)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = t.tx.ExecContext(
		ctx,
		`INSERT INTO financial_proofs(bank_id,transaction_id,order_id,transport_id,response) VALUES(?,?,?,?,?)`,
		proof.BankID,
		proof.TransactionID,
		proof.OrderID,
		proof.RequestID,
		[]byte(proof.Response),
	)
	if err != nil {
		return fmt.Errorf("record unique bank proof: %w", err)
	}
	return nil
}

func (t *shopTransaction) Identity(ctx context.Context, bank, treasury int64) error {
	if bank == 0 {
		return nil
	}

	var count int
	err := t.tx.QueryRowContext(
		ctx,
		`SELECT count(*) FROM shop_orders WHERE state NOT IN ('completed','rejected','failed') AND (bank_id!=? OR treasury_id!=?)`,
		bank,
		treasury,
	).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.New("outstanding shop orders require their original bank and treasury configuration")
	}
	return nil
}

func (t *shopTransaction) AccountBinding(
	ctx context.Context,
	bank int64,
	player core.UserID,
) (int64, bool, error) {
	var account int64
	err := t.tx.QueryRowContext(
		ctx,
		"SELECT account_id FROM bank_account_bindings WHERE bank_id=? AND user_id=?",
		bank,
		player,
	).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return account, err == nil, err
}

func (t *shopTransaction) BindAccount(ctx context.Context, bank int64, player core.UserID, account int64) error {
	if bank <= 0 || player <= 0 || account <= 0 || account > 2147483647 {
		return errors.New("invalid bank account binding")
	}

	bound, found, err := t.AccountBinding(ctx, bank, player)
	if err != nil {
		return err
	}
	if found {
		if bound != account {
			return fmt.Errorf("%w: player bank account binding changed", application.ErrProofConflict)
		}
		return nil
	}

	var boundPlayer core.UserID
	err = t.tx.QueryRowContext(
		ctx,
		"SELECT user_id FROM bank_account_bindings WHERE bank_id=? AND account_id=?",
		bank,
		account,
	).Scan(&boundPlayer)
	if err == nil {
		return fmt.Errorf("%w: bank account belongs to another player", application.ErrProofConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = t.tx.ExecContext(
		ctx,
		"INSERT INTO bank_account_bindings(bank_id,user_id,account_id) VALUES(?,?,?)",
		bank,
		player,
		account,
	)
	return err
}
