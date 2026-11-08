package grants

import (
	"context"
	"errors"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
)

type permissions struct {
	allowed bool
}

func (p permissions) Allowed(context.Context, core.Scope) (bool, error) {
	return p.allowed, nil
}

type operation struct {
	saved  Result
	exists bool
}

func (o operation) Operation(context.Context, Key) (Result, bool, error) {
	return o.saved, o.exists, nil
}

func (o operation) Record(context.Context, Key, Result) error {
	return errors.New("unexpected record")
}

type unit struct {
	tx        Transaction
	commitErr error
	called    bool
}

func (u *unit) WithinGrant(_ context.Context, fn func(Transaction) error) error {
	u.called = true
	if err := fn(u.tx); err != nil {
		return err
	}
	return u.commitErr
}

func TestReplayAndCommitBoundaries(t *testing.T) {
	saved := Result{
		Group:    -1,
		Quantity: 5,
	}
	uow := &unit{
		tx: Transaction{
			Operations: operation{
				saved:  saved,
				exists: true,
			},
		},
	}
	service, err := New(uow, permissions{false}, core.NewID)
	if err != nil {
		t.Fatal(err)
	}

	input := Request{
		Scope: core.Scope{
			Group:    -1,
			Actor:    1,
			BotID:    123,
			UpdateID: 1,
		},
	}
	result, err := service.Give(context.Background(), input, nil)
	if err != nil || result.Quantity != 5 {
		t.Fatalf("saved replay revalidated: %+v %v", result, err)
	}

	uow.commitErr = errors.New("commit failed")
	result, err = service.Give(context.Background(), input, nil)
	if err == nil || result.Quantity != 0 {
		t.Fatal("uncommitted success returned")
	}

	uow.commitErr = nil
	uow.tx.Operations = operation{}
	if _, err = service.Give(context.Background(), input, nil); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}

	input.Scope.Group = -2
	uow.tx.Operations = operation{
		saved:  saved,
		exists: true,
	}
	if _, err = service.Give(context.Background(), input, nil); err == nil {
		t.Fatal("foreign replay accepted")
	}
}
