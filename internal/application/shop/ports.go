// Package shop coordinates durable shop intent and external settlement.
package shop

import (
	"context"
	"encoding/json"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

type Request struct {
	ID              string
	OrderID         core.ID
	FinancialKey    string
	Kind            string
	Method          string
	Params          json.RawMessage
	NextAttempt     time.Time
	Attempts        int
	Unknown         bool
	PaymentID       string
	SenderAccountID int64
}

type Attempt struct {
	RequestID  string
	At         time.Time
	Sent       bool
	Diagnostic string
}

type Proof struct {
	BankID        int64
	TransactionID string
	OrderID       core.ID
	RequestID     string
	Response      json.RawMessage
}

type Evidence struct {
	State                 string
	PaymentID             string
	TransactionID         string
	SenderAccountID       int64
	CounterpartyAccountID int64
	ErrorCode             int
	Diagnostic            string
	Response              json.RawMessage
}

type Orders interface {
	Order(context.Context, core.ID) (domain.Order, bool, error)
	ByConfirmation(context.Context, core.ID) (domain.Order, bool, error)
	Create(context.Context, domain.Order, core.Scope) error
	Transition(context.Context, core.ID, domain.State, domain.State, string) (bool, error)
	List(context.Context, core.GroupID, core.UserID) ([]domain.Order, error)
}

type Requests interface {
	Request(context.Context, string) (Request, bool, error)
	CreateRequest(context.Context, Request) error
	UpdateRequest(context.Context, Request) error
	Due(context.Context, time.Time, int) ([]Request, error)
	RecordAttempt(context.Context, Attempt) error
	RecordResponse(context.Context, string, json.RawMessage, string) error
	SaveProof(context.Context, Proof) error
	Identity(context.Context, int64, int64) error
	AccountBinding(context.Context, int64, core.UserID) (int64, bool, error)
	BindAccount(context.Context, int64, core.UserID, int64) error
}

type Reservations interface {
	Reserve(context.Context, domain.Order) error
	Release(context.Context, core.ID) error
	Consume(context.Context, domain.Order) ([]inventory.Entry, error)
	Available(context.Context, core.GroupID, core.ID, core.ID) (inventory.Entry, bool, error)
}

type Transaction struct {
	Orders       Orders
	Requests     Requests
	Reservations Reservations
	Inventory    inventory.Writer
	Characters   characters.Creator
}

type UnitOfWork interface {
	WithinShop(context.Context, func(Transaction) error) error
}

// Protocol is separate from delivery: no network call occurs in a transaction.
type Protocol interface {
	Financial(domain.Order, string, string) (Request, error)
	Status(domain.Order, string, string) (Request, error)
	Encode(Request) (string, error)
	Decode(domain.Order, Request, string) (Evidence, error)
}

type Sender interface {
	SendBank(context.Context, int64, string) error
}
