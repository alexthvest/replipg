package character

import (
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/presentation"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func renderLocalizedSummary(c *localization.Catalog, l localization.Locale, s contract.Snapshot, created bool) (runtimecontract.MessageContent, error) {
	state, err := c.Text(l, localization.Key("character.state."+string(s.State)), nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	summary, err := presentation.Localized(c, l, "character.summary", localization.Args{"health": localization.Integer(s.Health), "max": localization.Integer(s.MaxHealth), "attack": localization.Integer(s.Attack), "defense": localization.Integer(s.Defense), "state": localization.String(state)})
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	var text presentation.Builder

	if created {
		notice, e := c.Text(l, "character.created", nil)
		if e != nil {
			return runtimecontract.MessageContent{}, e
		}

		text.Write(notice)
	}
	text.Append(summary)
	if s.State == contract.StateIncapacitated {
		notice, e := c.Text(l, "character.recovery", localization.Args{"deadline": localization.String(s.RecoverAt.UTC().Format(time.RFC3339Nano))})
		if e != nil {
			return runtimecontract.MessageContent{}, e
		}

		text.Write(notice)
	}
	return text.Content(), nil
}

func renderLocalizedDomainError(c *localization.Catalog, l localization.Locale, err error) (string, error) {
	var key localization.Key

	switch {
	case errors.Is(err, contract.ErrIncapacitated):
		key = "character.error.incapacitated"
	case errors.Is(err, contract.ErrInvalidInput):
		key = "character.error.invalid"
	case errors.Is(err, contract.ErrNotFound):
		key = "character.error.not_found"
	case errors.Is(err, contract.ErrOperationConflict):
		key = "character.error.conflict"
	case errors.Is(err, contract.ErrExhausted):
		key = "character.error.exhausted"
	default:
		return "", nil
	}
	return c.Text(l, key, nil)
}
