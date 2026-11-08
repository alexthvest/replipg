package app

import (
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/modules/shop"
)

func TestShopMaximumHonorsOwnershipAndBankAmountLimit(t *testing.T) {
	for _, tc := range []struct {
		unit      shop.Amount
		available int64
		want      int64
	}{
		{
			unit:      "10",
			available: 1000,
			want:      1000,
		},
		{
			unit:      "5",
			available: 3,
			want:      3,
		},
		{
			unit:      shop.Amount("1" + strings.Repeat("0", 99)),
			available: 1000,
			want:      9,
		},
		{
			unit:      shop.Amount(strings.Repeat("9", 100)),
			available: 1000,
			want:      1,
		},
	} {
		if got := shopQuantityLimit(tc.unit, tc.available); got != tc.want {
			t.Errorf("unit=%s available=%d: got %d, want %d", tc.unit, tc.available, got, tc.want)
		}
	}
}
