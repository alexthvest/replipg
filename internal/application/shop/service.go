package shop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

var ErrUnavailable = errors.New("bank settlement unavailable")
var ErrQuoteChanged = errors.New("quote changed; review current terms")
var ErrProofConflict = errors.New("bank transaction proof conflicts with another order")

type Catalog interface {
	Offer(string, items.Mode) (domain.Offer, bool)
	Capture(string) (items.Snapshot, error)
}

type Result struct {
	Order   domain.Order
	Entries []inventory.Entry
	Changed bool
}

type Service struct {
	uow        UnitOfWork
	protocol   Protocol
	ids        func() (core.ID, error)
	now        func() time.Time
	bankID     int64
	treasuryID int64
}

func New(
	uow UnitOfWork,
	protocol Protocol,
	ids func() (core.ID, error),
	now func() time.Time,
	bankID, treasuryID int64,
) (*Service, error) {
	if uow == nil || protocol == nil || ids == nil || now == nil || bankID < 0 || treasuryID <= 0 || treasuryID > 2147483647 {
		return nil, errors.New("shop: valid dependencies and bank configuration required")
	}
	return &Service{
		uow:        uow,
		protocol:   protocol,
		ids:        ids,
		now:        now,
		bankID:     bankID,
		treasuryID: treasuryID,
	}, nil
}

func (s *Service) CheckIdentity(ctx context.Context) error {
	return s.uow.WithinShop(ctx, func(tx Transaction) error {
		return tx.Requests.Identity(ctx, s.bankID, s.treasuryID)
	})
}

func (s *Service) Confirm(
	ctx context.Context,
	scope core.Scope,
	quote domain.Quote,
	catalog Catalog,
) (domain.Order, error) {
	if err := scope.Validate(); err != nil {
		return domain.Order{}, err
	}
	if quote.Group != scope.Group || quote.Player != scope.Actor || scope.BotID <= 0 || scope.UpdateID <= 0 || catalog == nil {
		return domain.Order{}, errors.New("shop: invalid confirmation scope")
	}

	var result domain.Order
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		previous, found, err := tx.Orders.ByConfirmation(ctx, quote.ID)
		if err != nil {
			return err
		}
		if found {
			if previous.Quote.Group != scope.Group || previous.Quote.Player != scope.Actor {
				return errors.New("shop: foreign confirmation")
			}

			result = previous
			return nil
		}
		if s.bankID == 0 {
			return ErrUnavailable
		}
		if err = quote.Validate(); err != nil {
			return err
		}
		if quote.BankID != s.bankID || quote.TreasuryID != s.treasuryID {
			return ErrQuoteChanged
		}
		if err = tx.Requests.Identity(ctx, s.bankID, s.treasuryID); err != nil {
			return err
		}

		offer, found := catalog.Offer(quote.Snapshot.CatalogID, quote.Snapshot.Mode)
		if !found {
			return ErrQuoteChanged
		}
		if quote.Direction == domain.Buy {
			snapshot, err := catalog.Capture(quote.Snapshot.CatalogID)
			if err != nil || snapshot != quote.Snapshot || offer.BuyPrice != quote.Unit {
				return ErrQuoteChanged
			}
		} else {
			entry, found, err := tx.Reservations.Available(ctx, quote.Group, quote.Character, quote.Entry)
			if err != nil {
				return err
			}
			if !found || entry.Snapshot != quote.Snapshot || entry.Available() < quote.Quantity || offer.SellPrice != quote.Unit {
				return ErrQuoteChanged
			}
		}

		id, err := s.ids()
		if err != nil {
			return err
		}
		requestID, err := s.ids()
		if err != nil {
			return err
		}
		result = domain.Order{
			ID:           id,
			Confirmation: quote.ID,
			Quote:        quote,
			State:        domain.Created,
		}
		if quote.Direction == domain.Sell {
			result.State = domain.Reserved
		}

		request, err := s.protocol.Financial(
			result,
			string(requestID),
			"replipg:order:"+string(id)+":"+string(quote.Direction),
		)
		if err != nil {
			return err
		}
		request.NextAttempt = s.now()
		if _, err = s.protocol.Encode(request); err != nil {
			return err
		}
		if err = tx.Orders.Create(ctx, result, scope); err != nil {
			return err
		}
		if quote.Direction == domain.Sell {
			if err = tx.Reservations.Reserve(ctx, result); err != nil {
				return err
			}
		}
		return tx.Requests.CreateRequest(ctx, request)
	})
	if err != nil {
		return domain.Order{}, fmt.Errorf("confirm shop order: %w", err)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, scope core.Scope) ([]domain.Order, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}

	var orders []domain.Order
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		var err error
		orders, err = tx.Orders.List(ctx, scope.Group, scope.Actor)
		return err
	})
	return orders, err
}

func (s *Service) Get(ctx context.Context, scope core.Scope, id core.ID) (domain.Order, error) {
	if err := scope.Validate(); err != nil {
		return domain.Order{}, err
	}

	var order domain.Order
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		saved, found, err := tx.Orders.Order(ctx, id)
		if err != nil {
			return err
		}
		if !found || saved.Quote.Group != scope.Group || saved.Quote.Player != scope.Actor {
			return errors.New("shop: order unavailable in this scope")
		}
		order = saved
		return nil
	})
	return order, err
}

func (s *Service) FindConfirmation(
	ctx context.Context,
	scope core.Scope,
	confirmation core.ID,
) (domain.Order, bool, error) {
	if err := scope.Validate(); err != nil {
		return domain.Order{}, false, err
	}

	var order domain.Order
	var found bool
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		var err error
		order, found, err = tx.Orders.ByConfirmation(ctx, confirmation)
		if found && (order.Quote.Group != scope.Group || order.Quote.Player != scope.Actor) {
			return errors.New("shop: foreign confirmation")
		}
		return err
	})
	return order, found, err
}

func (s *Service) Reconcile(ctx context.Context, id core.ID) (Result, error) {
	var result Result
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		order, found, err := tx.Orders.Order(ctx, id)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("shop: order not found")
		}

		result.Order = order
		if order.State != domain.Paid && order.State != domain.PayoutConfirmed {
			return nil
		}
		if order.State == domain.Paid {
			result.Entries, err = inventory.Grant(
				ctx,
				tx.Inventory,
				s.ids,
				order.Quote.Group,
				order.Quote.Character,
				order.Quote.Snapshot,
				order.Quote.Quantity,
			)
		} else {
			result.Entries, err = tx.Reservations.Consume(ctx, order)
		}
		if err != nil {
			return fmt.Errorf("order %s awaiting local fulfillment: %w", order.ID, err)
		}

		changed, err := tx.Orders.Transition(ctx, order.ID, order.State, domain.Completed, "")
		if err != nil {
			return err
		}
		if !changed {
			return errors.New("shop: settlement state changed")
		}

		result.Order.State = domain.Completed
		result.Changed = true
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// HandleReply records financial proof in its own commit before ownership changes.
func (s *Service) HandleReply(ctx context.Context, requestID, text string) (Result, error) {
	var result Result
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		request, found, err := tx.Requests.Request(ctx, requestID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("shop: unsolicited bank response")
		}

		order, found, err := tx.Orders.Order(ctx, request.OrderID)
		if err != nil || !found {
			return errors.New("shop: response order unavailable")
		}
		result.Order = order
		if order.Quote.BankID != s.bankID || order.Quote.TreasuryID != s.treasuryID {
			return errors.New("shop: response bank identity changed")
		}
		binding, bound, err := tx.Requests.AccountBinding(ctx, order.Quote.BankID, order.Quote.Player)
		if err != nil {
			return err
		}
		if bound {
			request.SenderAccountID = binding
		}

		evidence, decodeErr := s.protocol.Decode(order, request, text)
		outcome := evidence.State
		if decodeErr != nil {
			outcome = "invalid-proof"
		} else if evidence.ErrorCode != 0 {
			outcome = evidence.Diagnostic
		}
		if err = tx.Requests.RecordResponse(ctx, request.ID, []byte(text), outcome); err != nil {
			return err
		}
		if order.State == domain.Completed || order.State == domain.Failed || order.State == domain.Rejected {
			return nil
		}
		if order.State == domain.Paid || order.State == domain.PayoutConfirmed {
			return nil
		}

		var next domain.State
		diagnostic := ""
		if decodeErr != nil {
			next = domain.ReconciliationRequired
			diagnostic = "invalid bank proof: " + decodeErr.Error()
			request.NextAttempt = parked()
		} else if evidence.ErrorCode != 0 {
			diagnostic = evidence.Diagnostic
			if evidence.ErrorCode == -32001 || evidence.ErrorCode == -32004 {
				next = domain.ReconciliationRequired
				request.NextAttempt = parked()
			} else if (evidence.ErrorCode == -32601 || evidence.ErrorCode == -32602) &&
				request.Attempts <= 1 && request.PaymentID == "" && order.State != domain.ReconciliationRequired {
				next = domain.Failed
				request.NextAttempt = parked()
			} else {
				next = domain.ReconciliationRequired
				request.NextAttempt = s.now().Add(backoff(request.Attempts))
			}
		} else {
			if request.PaymentID != "" && request.PaymentID != evidence.PaymentID {
				return errors.New("shop: bank payment identity changed")
			}
			request.PaymentID = evidence.PaymentID
			switch evidence.State {
			case "pending":
				next = domain.AwaitingPayment
				request.NextAttempt = s.now().Add(5 * time.Second)
			case "rejected":
				next = domain.Rejected
				request.NextAttempt = parked()
			case "failed":
				next = domain.Failed
				diagnostic = evidence.Diagnostic
				request.NextAttempt = parked()
			case "paid":
				proof := Proof{
					BankID:        order.Quote.BankID,
					TransactionID: evidence.TransactionID,
					OrderID:       order.ID,
					RequestID:     request.ID,
					Response:      evidence.Response,
				}
				if err = tx.Requests.SaveProof(ctx, proof); err != nil {
					if !errors.Is(err, ErrProofConflict) {
						return err
					}

					next = domain.ReconciliationRequired
					diagnostic = fmt.Sprintf(
						"conflicting proof for order %s request %s: %v", order.ID, request.ID, err,
					)
					request.NextAttempt = parked()
					break
				}
				if err = tx.Requests.BindAccount(ctx, order.Quote.BankID, order.Quote.Player, evidence.CounterpartyAccountID); err != nil {
					if !errors.Is(err, ErrProofConflict) {
						return err
					}

					next = domain.ReconciliationRequired
					diagnostic = fmt.Sprintf("bank account binding conflict for order %s request %s", order.ID, request.ID)
					request.NextAttempt = parked()
					break
				}

				request.SenderAccountID = evidence.CounterpartyAccountID
				next = domain.Paid
				if order.Quote.Direction == domain.Sell {
					next = domain.PayoutConfirmed
				}
				request.NextAttempt = s.now()
			default:
				return errors.New("shop: unrecognized bank evidence")
			}
		}

		if next == domain.Failed || next == domain.Rejected {
			if err = tx.Reservations.Release(ctx, order.ID); err != nil {
				return err
			}
		}
		if err = tx.Requests.UpdateRequest(ctx, request); err != nil {
			return err
		}
		if next != order.State {
			changed, err := tx.Orders.Transition(ctx, order.ID, order.State, next, diagnostic)
			if err != nil {
				return err
			}
			if !changed {
				return errors.New("shop: concurrent bank state change")
			}
		}

		result.Order.State = next
		result.Order.Diagnostic = diagnostic
		result.Changed = true
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if result.Order.State == domain.Paid || result.Order.State == domain.PayoutConfirmed {
		fulfilled, err := s.Reconcile(ctx, result.Order.ID)
		if err != nil {
			return result, err
		}
		return fulfilled, nil
	}
	return result, nil
}
