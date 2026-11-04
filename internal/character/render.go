package character

import (
	"errors"
	"fmt"
	"github.com/alexthvest/replipg/internal/character/contract"
	"time"
)

func renderSummary(snapshot contract.Snapshot, created bool) string {
	notice := ""
	if created {
		notice = "Character created.\n"
	}
	text := fmt.Sprintf("%sHealth: %d/%d\nAttack: %d\nDefense: %d\nState: %s", notice, snapshot.Health, snapshot.MaxHealth, snapshot.Attack, snapshot.Defense, snapshot.State)
	if snapshot.State == contract.StateIncapacitated {
		text += "\nRecovery (UTC): " + snapshot.RecoverAt.UTC().Format(time.RFC3339Nano)
	}
	return text
}
func renderDomainError(err error) string {
	switch {
	case errors.Is(err, contract.ErrIncapacitated):
		return "Your character is incapacitated until recovery."
	case errors.Is(err, contract.ErrInvalidInput):
		return "Invalid character action."
	case errors.Is(err, contract.ErrNotFound):
		return "Character not found."
	case errors.Is(err, contract.ErrOperationConflict):
		return "This character action conflicts with an earlier action."
	case errors.Is(err, contract.ErrExhausted):
		return "This character action cannot be applied."
	default:
		return ""
	}
}
