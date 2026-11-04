package persistence

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"modernc.org/sqlite"
)

func TestIsStorageFailure(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false}, {"domain", errors.New("domain failure"), false},
		{"not found", sql.ErrNoRows, false}, {"validation", &UpdateOutcomeValidationError{Field: "status", Reason: "unsupported"}, false},
		{"sqlite", new(sqlite.Error), true}, {"wrapped sqlite", fmt.Errorf("context: %w", new(sqlite.Error)), true},
		{"transaction done", sql.ErrTxDone, true}, {"connection done", sql.ErrConnDone, true},
		{"storage", ErrStorage, true}, {"transaction", ErrTransaction, true}, {"contention", ErrContention, true}, {"closed", ErrPersistenceClosed, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStorageFailure(tt.err); got != tt.want {
				t.Fatalf("IsStorageFailure()=%v want %v", got, tt.want)
			}
		})
	}
}
