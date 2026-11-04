package contract_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/character/contract"
)

func TestDomainErrors(t *testing.T) {
	tests := []struct {
		kind   contract.ErrorKind
		target error
	}{
		{contract.ErrorInvalidInput, contract.ErrInvalidInput},
		{contract.ErrorInvalidConfig, contract.ErrInvalidConfig},
		{contract.ErrorNotFound, contract.ErrNotFound},
		{contract.ErrorIncapacitated, contract.ErrIncapacitated},
		{contract.ErrorOperationConflict, contract.ErrOperationConflict},
		{contract.ErrorExhausted, contract.ErrExhausted},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			err := fmt.Errorf("character operation: %w", &contract.DomainError{Kind: tt.kind, Field: "secret SQL credential"})
			var domain *contract.DomainError

			if !errors.Is(err, tt.target) || !errors.As(err, &domain) || domain.Kind != tt.kind {
				t.Fatalf("uninspectable error %v", err)
			}

			if got := domain.Error(); got == "" || containsSensitive(got) {
				t.Fatalf("unsafe message %q", got)
			}
		})
	}

	unknown := &contract.DomainError{Kind: "secret SQL credential", Field: "secret SQL credential"}

	if containsSensitive(unknown.Error()) {
		t.Fatal("unrecognized kind exposed")
	}
}

func containsSensitive(s string) bool {
	for _, word := range []string{"secret", "SQL", "credential"} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

type driverFailure struct{}

func (*driverFailure) Error() string { return "secret SQL credential" }

func TestStorageErrorPreservesCauseWithSafeText(t *testing.T) {
	driver := &driverFailure{}
	for _, cause := range []error{driver, context.Canceled, context.DeadlineExceeded, errors.New("secret SQL credential")} {
		err := contract.NewStorageError(cause)
		var storage *contract.StorageError

		if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, cause) || !errors.As(err, &storage) {
			t.Fatalf("lost storage cause: %v", err)
		}

		if err.Error() != "character storage operation failed" || fmt.Sprint(err) != err.Error() {
			t.Fatalf("unsafe storage message %q", err.Error())
		}

		if cause == driver {
			var got *driverFailure

			if !errors.As(err, &got) || got != driver {
				t.Fatal("lost driver type")
			}
		}
	}
}
