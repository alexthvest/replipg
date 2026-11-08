// Package inventory owns group-scoped ownership rules and inventory read models.
package inventory

import (
	"context"
	"errors"
	"math"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type Entry struct {
	ID         core.ID
	Group      core.GroupID
	Character  core.ID
	SnapshotID core.ID
	Snapshot   items.Snapshot
	Quantity   int64
}

type Reader interface {
	Entries(context.Context, core.GroupID, core.ID) ([]Entry, error)
	Entry(context.Context, core.GroupID, core.ID, core.ID) (Entry, bool, error)
}

type Writer interface {
	ReuseSnapshot(context.Context, items.Snapshot) (core.ID, error)
	Stack(context.Context, core.GroupID, core.ID, core.ID) (Entry, bool, error)
	PutEntry(context.Context, Entry) error
}

var ErrQuantity = errors.New("quantity must be an integer from 1 to 1000")

var ErrOverflow = errors.New("stack quantity overflow")

func ValidateQuantity(quantity int64) error {
	if quantity < 1 || quantity > 1000 {
		return ErrQuantity
	}
	return nil
}

// Grant uses only transaction-bound writes. It never opens or commits a transaction.
func Grant(
	ctx context.Context,
	repo Writer,
	ids func() (core.ID, error),
	group core.GroupID,
	character core.ID,
	snapshot items.Snapshot,
	quantity int64,
) ([]Entry, error) {
	if group >= 0 || character == "" {
		return nil, errors.New("invalid inventory identity")
	}

	if err := ValidateQuantity(quantity); err != nil {
		return nil, err
	}

	if err := snapshot.Validate(); err != nil {
		return nil, err
	}

	snapshotID, err := repo.ReuseSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if snapshot.Mode == items.Stackable {
		entry, found, err := repo.Stack(ctx, group, character, snapshotID)
		if err != nil {
			return nil, err
		}

		if !found {
			id, err := ids()
			if err != nil {
				return nil, err
			}

			entry = Entry{
				ID:         id,
				Group:      group,
				Character:  character,
				SnapshotID: snapshotID,
				Snapshot:   snapshot,
			}
		}

		if entry.Quantity > math.MaxInt64-quantity {
			return nil, ErrOverflow
		}

		entry.Quantity += quantity
		if err = repo.PutEntry(ctx, entry); err != nil {
			return nil, err
		}
		return []Entry{entry}, nil
	}

	entries := make([]Entry, 0, int(quantity))
	for range quantity {
		id, err := ids()
		if err != nil {
			return nil, err
		}

		entry := Entry{
			ID:         id,
			Group:      group,
			Character:  character,
			SnapshotID: snapshotID,
			Snapshot:   snapshot,
			Quantity:   1,
		}
		if err = repo.PutEntry(ctx, entry); err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}
	return entries, nil
}

type Service struct {
	repo Reader
}

func New(repo Reader) (*Service, error) {
	if repo == nil {
		return nil, errors.New("inventory: repository required")
	}
	return &Service{repo: repo}, nil
}

func (s *Service) List(ctx context.Context, group core.GroupID, character core.ID) ([]Entry, error) {
	if group >= 0 || character == "" {
		return nil, errors.New("invalid inventory scope")
	}
	return s.repo.Entries(ctx, group, character)
}

func (s *Service) Inspect(ctx context.Context, group core.GroupID, character, id core.ID) (Entry, error) {
	entry, ok, err := s.repo.Entry(ctx, group, character, id)
	if err != nil {
		return entry, err
	}
	if !ok {
		return entry, errors.New("entry not found in this inventory")
	}
	return entry, nil
}
