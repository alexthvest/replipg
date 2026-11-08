package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	assets := t.TempDir()
	base := map[string]string{
		"REPLIPG_BOT_TOKEN":    "123:secret",
		"REPLIPG_BOT_OWNER_ID": "42",
		"REPLIPG_ASSETS_PATH":  assets,
	}
	get := func(k string) string {
		return base[k]
	}
	c, err := Parse(get)
	if err != nil || c.OwnerID != 42 || c.MenuTTL != 15*time.Minute || !filepath.IsAbs(c.DatabasePath) {
		t.Fatalf("defaults: %v", err)
	}

	for _, tc := range []struct {
		k string
		v string
	}{
		{
			"REPLIPG_BOT_TOKEN",
			"",
		},
		{
			"REPLIPG_BOT_TOKEN",
			"secret",
		},
		{
			"REPLIPG_BOT_TOKEN",
			"12:secret\n",
		},
		{
			"REPLIPG_BOT_OWNER_ID",
			"0",
		},
		{
			"REPLIPG_BOT_OWNER_ID",
			"secret",
		},
		{
			"REPLIPG_ASSET_DEBOUNCE",
			"0s",
		},
		{
			"REPLIPG_MENU_TTL",
			"-1m",
		},
		{
			"REPLIPG_SHUTDOWN_TIMEOUT",
			"secret",
		},
		{
			"REPLIPG_DATABASE_PATH",
			"\x00secret",
		},
		{
			"REPLIPG_DATABASE_PATH",
			assets,
		},
		{
			"REPLIPG_ASSETS_PATH",
			filepath.Join(assets, "missing"),
		},
	} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			old := base[tc.k]
			base[tc.k] = tc.v
			defer func() {
				base[tc.k] = old
			}()

			_, err := Parse(get)
			if err == nil {
				t.Fatal("accepted invalid config")
			}

			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}

	if _, err := Parse(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestOnlyAppEnvironmentNames(t *testing.T) {
	values := map[string]string{
		"TELEGRAM_BOT_TOKEN":       "999:legacy",
		"BOT_OWNER_ID":             "999",
		"REPLIPG_BOT_TOKEN":        "123:configured",
		"REPLIPG_BOT_OWNER_ID":     "42",
		"REPLIPG_ASSETS_PATH":      t.TempDir(),
		"REPLIPG_DATABASE_PATH":    filepath.Join(t.TempDir(), "game.db"),
		"REPLIPG_ASSET_DEBOUNCE":   "500ms",
		"REPLIPG_MENU_TTL":         "20m",
		"REPLIPG_SHUTDOWN_TIMEOUT": "15s",
	}
	cfg, err := Parse(func(key string) string {
		return values[key]
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Token != "123:configured" ||
		cfg.OwnerID != 42 ||
		cfg.AssetDebounce != 500*time.Millisecond ||
		cfg.MenuTTL != 20*time.Minute ||
		cfg.ShutdownTimeout != 15*time.Second {
		t.Fatal("prefixed configuration was not used")
	}

	delete(values, "REPLIPG_BOT_TOKEN")
	if _, err = Parse(func(key string) string {
		return values[key]
	}); err == nil {
		t.Fatal("legacy token accepted")
	}
}
