package telegram

import (
	"strconv"
	"strings"

	"github.com/alexthvest/replipg/internal/modules/inventory"
)

func ParseGive(args string) (string, int64, error) {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		return "", 0, inventory.ErrQuantity
	}
	if fields[1] == "" {
		return "", 0, inventory.ErrQuantity
	}

	for _, r := range fields[1] {
		if r < '0' || r > '9' {
			return "", 0, inventory.ErrQuantity
		}
	}

	quantity, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", 0, inventory.ErrQuantity
	}

	if err = inventory.ValidateQuantity(quantity); err != nil {
		return "", 0, err
	}
	return fields[0], quantity, nil
}
