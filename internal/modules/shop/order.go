package shop

import (
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type Direction string

const (
	Buy  Direction = "buy"
	Sell Direction = "sell"
)

type State string

const (
	Created                State = "created"
	Requesting             State = "requesting"
	AwaitingPayment        State = "awaiting-payment"
	Paid                   State = "paid"
	Reserved               State = "reserved"
	PayoutRequesting       State = "payout-requesting"
	PayoutConfirmed        State = "payout-confirmed"
	Completed              State = "completed"
	Rejected               State = "rejected"
	Failed                 State = "failed"
	ReconciliationRequired State = "reconciliation-required"
)

type Quote struct {
	ID           core.ID
	Group        core.GroupID
	Player       core.UserID
	Character    core.ID
	Entry        core.ID
	Direction    Direction
	Snapshot     items.Snapshot
	Quantity     int64
	Unit         Amount
	Total        Amount
	BankID       int64
	TreasuryID   int64
	DeliveryChat int64
}

func (q Quote) Validate() error {
	if q.ID == "" || q.Group >= 0 || q.Player <= 0 || q.Character == "" || q.BankID <= 0 || q.TreasuryID <= 0 {
		return errors.New("invalid quote identity")
	}

	if q.Direction != Buy && q.Direction != Sell {
		return errors.New("invalid quote direction")
	}

	if q.Direction == Sell && (q.Entry == "" || (q.Snapshot.Mode == items.Individual && q.Quantity != 1)) {
		return errors.New("sale requires an entry and valid instance quantity")
	}

	if q.Direction == Buy && q.DeliveryChat != int64(q.Group) {
		return errors.New("purchase must deliver to its originating group")
	}

	if err := q.Snapshot.Validate(); err != nil {
		return err
	}

	total, err := q.Unit.Total(q.Quantity)
	if err != nil {
		return err
	}
	if total != q.Total {
		return errors.New("quote total does not match unit price and quantity")
	}
	return nil
}

type Order struct {
	ID           core.ID
	Confirmation core.ID
	Quote        Quote
	State        State
	Diagnostic   string
}
