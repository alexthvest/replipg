package shop

import (
	"context"
	"errors"
	"hash/fnv"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

type Work struct {
	RequestID string
	OrderID   core.ID
	BankID    int64
	Body      string
	Fulfill   bool
	Group     core.GroupID
	Character core.ID
}

// DueWork claims a bounded batch durably before any transport call begins.
func (s *Service) DueWork(ctx context.Context, now time.Time, limit int) ([]Work, error) {
	if s.bankID == 0 {
		return nil, nil
	}
	if limit < 1 || limit > 32 {
		return nil, errors.New("shop: recovery batch must be 1..32")
	}

	var work []Work
	err := s.uow.WithinShop(ctx, func(tx Transaction) error {
		if err := tx.Requests.Identity(ctx, s.bankID, s.treasuryID); err != nil {
			return err
		}

		requests, err := tx.Requests.Due(ctx, now, limit)
		if err != nil {
			return err
		}

		for _, request := range requests {
			order, found, err := tx.Orders.Order(ctx, request.OrderID)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("shop: recovery order missing")
			}
			if order.State == domain.Paid || order.State == domain.PayoutConfirmed {
				request.NextAttempt = now.Add(backoff(request.Attempts))
				if err = tx.Requests.UpdateRequest(ctx, request); err != nil {
					return err
				}

				work = append(work, Work{
					OrderID:   order.ID,
					Fulfill:   true,
					Group:     order.Quote.Group,
					Character: order.Quote.Character,
				})
				continue
			}
			if request.PaymentID != "" && order.Quote.Direction == domain.Buy {
				request.NextAttempt = parked()
				if err = tx.Requests.UpdateRequest(ctx, request); err != nil {
					return err
				}

				id, err := s.ids()
				if err != nil {
					return err
				}

				fresh, err := s.protocol.Status(order, string(id), request.PaymentID)
				if err != nil {
					return err
				}
				fresh.FinancialKey = request.FinancialKey
				fresh.SenderAccountID = request.SenderAccountID
				fresh.NextAttempt = now
				if err = tx.Requests.CreateRequest(ctx, fresh); err != nil {
					return err
				}
				request = fresh
			}

			request.Attempts++
			request.Unknown = true
			request.NextAttempt = now.Add(backoff(request.Attempts) + jitter(request.ID, request.Attempts))
			if err = tx.Requests.UpdateRequest(ctx, request); err != nil {
				return err
			}
			if err = tx.Requests.RecordAttempt(ctx, Attempt{
				RequestID: request.ID,
				At:        now,
			}); err != nil {
				return err
			}

			next := domain.Requesting
			if order.Quote.Direction == domain.Sell {
				next = domain.PayoutRequesting
			}
			if order.State == domain.Created || order.State == domain.Reserved {
				if _, err = tx.Orders.Transition(ctx, order.ID, order.State, next, ""); err != nil {
					return err
				}
			}

			body, err := s.protocol.Encode(request)
			if err != nil {
				return err
			}
			work = append(work, Work{
				RequestID: request.ID,
				OrderID:   order.ID,
				BankID:    order.Quote.BankID,
				Body:      body,
				Group:     order.Quote.Group,
				Character: order.Quote.Character,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return work, nil
}

func (s *Service) ObserveSend(ctx context.Context, id string, sendErr error) error {
	return s.uow.WithinShop(ctx, func(tx Transaction) error {
		request, found, err := tx.Requests.Request(ctx, id)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("shop: send observation has no intent")
		}

		diagnostic := ""
		if sendErr != nil {
			diagnostic = "delivery unknown; recover with original financial key"
		}
		return tx.Requests.RecordAttempt(ctx, Attempt{
			RequestID:  request.ID,
			At:         s.now(),
			Sent:       sendErr == nil,
			Diagnostic: diagnostic,
		})
	})
}

func parked() time.Time {
	return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
}

func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 {
		attempts = 7
	}
	return time.Second * time.Duration(1<<attempts)
}

func jitter(id string, attempt int) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return time.Duration((hash.Sum32()+uint32(attempt)%251)%251) * time.Millisecond
}
