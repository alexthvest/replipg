package config

import (
	"strings"
	"testing"
)

func TestBankConfiguration(t *testing.T) {
	values := map[string]string{
		"REPLIPG_BOT_TOKEN":    "123:test",
		"REPLIPG_BOT_OWNER_ID": "42",
		"REPLIPG_ASSETS_PATH":  t.TempDir(),
	}
	parse := func() (Config, error) {
		return Parse(func(key string) string {
			return values[key]
		})
	}
	cfg, err := parse()
	if err != nil || cfg.BankID != 0 || cfg.TreasuryID != 2 {
		t.Fatalf("bank defaults: %+v %v", cfg, err)
	}
	values["REPLIPG_BANK_BOT_ID"] = "99"
	values["REPLIPG_BANK_TREASURY_ID"] = "2147483647"
	cfg, err = parse()
	if err != nil || cfg.BankID != 99 || cfg.TreasuryID != 2147483647 {
		t.Fatalf("bank supplied config: %+v %v", cfg, err)
	}
	for _, key := range []string{"REPLIPG_BANK_BOT_ID", "REPLIPG_BANK_TREASURY_ID"} {
		old := values[key]
		for _, invalid := range []string{"0", "-1", "+1", "1.0", " 1", "secret", "9223372036854775808"} {
			values[key] = invalid
			if _, err = parse(); err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid bank config %s: %v", key, err)
			}
		}
		values[key] = old
	}
	values["REPLIPG_BANK_TREASURY_ID"] = "2147483648"
	if _, err = parse(); err == nil {
		t.Fatal("treasury exceeds bank account limit")
	}
}
