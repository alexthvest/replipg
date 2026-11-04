package contract_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestDomainErrorsSafeAndInspectable(t *testing.T) {
	for _, tt := range []struct {
		kind     contract.ErrorKind
		sentinel error
	}{
		{contract.ErrorInvalidInput, contract.ErrInvalidInput},
		{contract.ErrorInvalidCatalog, contract.ErrInvalidCatalog},
		{contract.ErrorNotFound, contract.ErrNotFound},
		{contract.ErrorNotOwned, contract.ErrNotOwned},
		{contract.ErrorIncompatibleSlot, contract.ErrIncompatibleSlot},
		{contract.ErrorStaleControl, contract.ErrStaleControl},
		{contract.ErrorExpiredControl, contract.ErrExpiredControl},
		{contract.ErrorNotUsable, contract.ErrNotUsable},
		{contract.ErrorFullHealth, contract.ErrFullHealth},
		{contract.ErrorIncapacitated, contract.ErrIncapacitated},
		{contract.ErrorOperationConflict, contract.ErrOperationConflict},
		{contract.ErrorExhausted, contract.ErrExhausted},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			domain := &contract.DomainError{Kind: tt.kind, Field: "private command token"}
			wrapped := fmt.Errorf("operation: %w", domain)
			var got *contract.DomainError

			if !errors.Is(wrapped, tt.sentinel) || !errors.As(wrapped, &got) || got != domain {
				t.Fatal("lost domain classification")
			}

			if domain.Error() != tt.sentinel.Error() || strings.Contains(domain.Error(), domain.Field) {
				t.Fatal("unsafe domain text")
			}
		})
	}

	unknown := &contract.DomainError{Kind: "secret token", Field: "private content"}

	if unknown.Error() != "inventory operation failed" || errors.Is(unknown, contract.ErrInvalidInput) {
		t.Fatal("unknown category not sanitized")
	}
}

type driverFailure struct{}

func (*driverFailure) Error() string { return "secret SQL token and personal content" }

func TestStorageFailurePreservesCauseAndSafeText(t *testing.T) {
	driver := &driverFailure{}
	for _, cause := range []error{driver, context.Canceled, context.DeadlineExceeded, errors.New("private SQL and content")} {
		err := contract.NewStorageError(cause)
		var storage *contract.StorageError
		var marker runtimecontract.StorageFailure

		if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, cause) || !errors.As(err, &storage) || !errors.As(err, &marker) {
			t.Fatal("lost cause or runtime classification")
		}

		if err.Error() != "inventory storage operation failed" || fmt.Sprint(err) != err.Error() {
			t.Fatal("unsafe error text")
		}

		if cause == driver {
			var got *driverFailure

			if !errors.As(err, &got) || got != driver {
				t.Fatal("lost driver type")
			}
		}
	}
}
