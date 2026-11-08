// Package shop owns fixed offers, immutable quotes, and order rules.
package shop

import (
	"errors"
	"math/big"

	"github.com/alexthvest/replipg/internal/modules/inventory"
)

// Amount is a canonical positive integer, never a floating point value.
type Amount string

func (a Amount) Validate() error {
	if len(a) == 0 || len(a) > 100 || a[0] < '1' || a[0] > '9' {
		return errors.New("amount must be a canonical positive integer of at most 100 digits")
	}

	for _, digit := range a {
		if digit < '0' || digit > '9' {
			return errors.New("amount contains a nondecimal digit")
		}
	}
	return nil
}

func (a Amount) Total(quantity int64) (Amount, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}

	if err := inventory.ValidateQuantity(quantity); err != nil {
		return "", err
	}

	value, _ := new(big.Int).SetString(string(a), 10)
	total := Amount(value.Mul(value, big.NewInt(quantity)).String())
	return total, total.Validate()
}

func (a Amount) less(other Amount) bool {
	if len(a) != len(other) {
		return len(a) < len(other)
	}
	return a < other
}
