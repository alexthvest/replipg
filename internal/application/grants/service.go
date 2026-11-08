// Package grants coordinates one atomic cross-feature grant transaction.
package grants

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type Key struct {
	BotID    int64
	UpdateID int64
}

type Result struct {
	Group     core.GroupID
	Character characters.Character
	Snapshot  items.Snapshot
	Quantity  int64
	Entries   []inventory.Entry
}

type Operations interface {
	Operation(context.Context, Key) (Result, bool, error)
	Record(context.Context, Key, Result) error
}

type Transaction struct {
	Characters characters.Creator
	Inventory  inventory.Writer
	Operations Operations
}

type UnitOfWork interface {
	WithinGrant(context.Context, func(Transaction) error) error
}

type Permissions interface {
	Allowed(context.Context, core.Scope) (bool, error)
}

type Catalog interface {
	Capture(string) (items.Snapshot, error)
}

type Service struct {
	uow         UnitOfWork
	permissions Permissions
	ids         func() (core.ID, error)
}

func New(uow UnitOfWork, permissions Permissions, ids func() (core.ID, error)) (*Service, error) {
	if uow == nil || permissions == nil || ids == nil {
		return nil, errors.New("grants: unit of work, permissions and IDs required")
	}
	return &Service{
		uow:         uow,
		permissions: permissions,
		ids:         ids,
	}, nil
}

var ErrDenied = errors.New("grant permission denied")

type Request struct {
	Scope     core.Scope
	Recipient *core.User
	ItemID    string
	Quantity  int64
}

func (s *Service) Give(ctx context.Context, input Request, catalog Catalog) (Result, error) {
	if err := input.Scope.Validate(); err != nil {
		return Result{}, err
	}
	if input.Scope.BotID <= 0 || input.Scope.UpdateID <= 0 {
		return Result{}, errors.New("grant operation identity required")
	}

	key := Key{
		BotID:    input.Scope.BotID,
		UpdateID: input.Scope.UpdateID,
	}
	var result Result
	// Permission reads occur before acquiring the one-connection transaction. Replay
	// ignores the result, preserving the originally committed operation outcome.
	allowed, permissionErr := s.permissions.Allowed(ctx, input.Scope)
	err := s.uow.WithinGrant(ctx, func(tx Transaction) error {
		saved, exists, err := tx.Operations.Operation(ctx, key)
		if err != nil {
			return err
		}
		if exists {
			if saved.Group != input.Scope.Group {
				return errors.New("foreign grant replay")
			}

			result = saved
			return nil
		}
		if permissionErr != nil {
			return permissionErr
		}
		if !allowed {
			return ErrDenied
		}
		if input.Recipient == nil || input.Recipient.ID <= 0 {
			return errors.New("reply recipient required")
		}

		if err = inventory.ValidateQuantity(input.Quantity); err != nil {
			return err
		}

		snapshot, err := catalog.Capture(input.ItemID)
		if err != nil {
			return err
		}

		character, err := characters.Ensure(ctx, tx.Characters, s.ids, input.Scope.Group, *input.Recipient)
		if err != nil {
			return err
		}

		entries, err := inventory.Grant(ctx, tx.Inventory, s.ids, input.Scope.Group, character.ID, snapshot, input.Quantity)
		if err != nil {
			return err
		}

		result = Result{
			Group:     input.Scope.Group,
			Character: character,
			Snapshot:  snapshot,
			Quantity:  input.Quantity,
			Entries:   entries,
		}
		return tx.Operations.Record(ctx, key, result)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
