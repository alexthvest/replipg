package config

import (
	"strings"
	"testing"
	"time"
)

func TestCraftingConfiguration(t *testing.T) {
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
	if err != nil || cfg.Crafting.Enabled || cfg.Crafting.Timeout != 2*time.Minute {
		t.Fatalf("disabled defaults: %+v %v", cfg.Crafting, err)
	}
	values["REPLIPG_CRAFTING_ENABLED"] = "true"
	if _, err = parse(); err == nil {
		t.Fatal("enabled discovery without local settings")
	}
	values["REPLIPG_CRAFTING_ENDPOINT"] = "http://127.0.0.1:11434"
	if _, err = parse(); err == nil {
		t.Fatal("enabled discovery without local model")
	}
	values["REPLIPG_CRAFTING_MODEL"] = "qwen3:4b"
	cfg, err = parse()
	if err != nil || !cfg.Crafting.Enabled || cfg.Crafting.Token != "" {
		t.Fatalf("local settings without hosted key: %v", err)
	}
	for key, invalid := range map[string][]string{
		"REPLIPG_CRAFTING_ENABLED":            {"yes", "1", "secret"},
		"REPLIPG_CRAFTING_ENDPOINT":           {"https://ollama.com", "http://user:secret@localhost:11434", "http://localhost:11434/?secret", "http://8.8.8.8", "file:///secret"},
		"REPLIPG_CRAFTING_MODEL":              {"", "\nsecret", " model"},
		"REPLIPG_CRAFTING_TIMEOUT":            {"0s", "-1s", "11m", "secret"},
		"REPLIPG_CRAFTING_MIN_INTERVAL":       {"0s", "2h", "secret"},
		"REPLIPG_CRAFTING_MAX_RESPONSE_BYTES": {"0", "1048577", "secret"},
		"REPLIPG_CRAFTING_MAX_TOKENS":         {"0", "8193", "secret"},
		"REPLIPG_CRAFTING_TOKEN":              {"secret\nheader"},
	} {
		old := values[key]
		for _, value := range invalid {
			values[key] = value
			_, err = parse()
			if err == nil || !strings.Contains(err.Error(), key) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("invalid setting %s accepted or leaked: %v", key, err)
			}
		}
		values[key] = old
	}
	values["REPLIPG_CRAFTING_TOKEN"] = "local-proxy-secret"
	cfg, err = parse()
	if err != nil || cfg.Crafting.Token != "local-proxy-secret" {
		t.Fatal("optional local authentication rejected")
	}
	for _, endpoint := range []string{"http://localhost:11434", "http://ollama:11434", "http://192.168.1.10:11434", "http://[::1]:11434"} {
		if !LocalEndpoint(endpoint) {
			t.Fatalf("local endpoint rejected: %s", endpoint)
		}
	}
}
