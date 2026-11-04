package character

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
)

func TestConfigDefaults(t *testing.T) {
	production, err := LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	development, err := LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}

	for _, cfg := range []Config{production, development} {
		if cfg.MaxHealth() != 100 || cfg.Attack() != 10 || cfg.Defense() != 5 || cfg.RecoveryPercent() != "25" {
			t.Fatalf("unexpected tuning: %+v", cfg)
		}
	}

	if production.IncapacitationDuration() != 5*time.Minute || development.IncapacitationDuration() != 5*time.Second {
		t.Fatal("incorrect mode duration")
	}

	again, err := LoadConfig(false)
	if err != nil || again != production {
		t.Fatalf("defaults changed: %v", err)
	}
}

func configFixture(t *testing.T, field string, value any) []byte {
	t.Helper()
	fields := map[string]any{"max_health": 100, "attack": 10, "defense": 5, "incapacitation_duration": "5m", "recovery_percent": "25"}
	fields[field] = value
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func TestConfigRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		field string
		value any
	}{
		{"max_health", 0}, {"max_health", -1}, {"max_health", 1.5}, {"max_health", "100"}, {"max_health", nil},
		{"attack", -1}, {"attack", 0.5}, {"attack", nil}, {"defense", -1}, {"defense", 0.5}, {"defense", nil},
		{"incapacitation_duration", "0s"}, {"incapacitation_duration", "-1s"}, {"incapacitation_duration", "999999999999999h"}, {"incapacitation_duration", "sensitive-fixture"}, {"incapacitation_duration", nil},
		{"recovery_percent", "0"}, {"recovery_percent", "100"}, {"recovery_percent", "100.000000000000000001"}, {"recovery_percent", "-1"},
		{"recovery_percent", "1/4"}, {"recovery_percent", "2.5e1"}, {"recovery_percent", " 25"}, {"recovery_percent", "NaN"}, {"recovery_percent", "Inf"}, {"recovery_percent", "sensitive-fixture"}, {"recovery_percent", 25}, {"recovery_percent", nil},
	}
	for _, tt := range tests {
		t.Run(tt.field+"/"+string(configFixture(t, tt.field, tt.value)), func(t *testing.T) {
			cfg, err := ParseConfig(configFixture(t, tt.field, tt.value))
			if err == nil {
				t.Fatalf("accepted invalid setting: %+v", cfg)
			}

			if !strings.Contains(err.Error(), tt.field) || strings.Contains(err.Error(), "sensitive-fixture") {
				t.Fatalf("unsafe or unspecific error: %v", err)
			}

			if cfg != (Config{}) {
				t.Fatal("returned partial configuration")
			}
		})
	}
}

func TestConfigExactDecimalAndIntegerBounds(t *testing.T) {
	for _, percent := range []string{"0.000000000000000000000001", "25.125000000000000000001", "99.999999999999999999999999"} {
		cfg, err := ParseConfig(configFixture(t, "recovery_percent", percent))
		if err != nil {
			t.Fatal(err)
		}

		if cfg.RecoveryPercent() != percent {
			t.Fatalf("lost decimal precision: %q", cfg.RecoveryPercent())
		}
	}

	for _, field := range []string{"max_health", "attack", "defense"} {
		cfg, err := ParseConfig(configFixture(t, field, int64(math.MaxInt64)))
		if err != nil {
			t.Fatalf("int64 maximum %s: %v", field, err)
		}

		if cfg.MaxHealth() <= 0 {
			t.Fatal("invalid maximum health")
		}

		if _, err := ParseConfig(configFixture(t, field, uint64(math.MaxInt64)+1)); err == nil {
			t.Fatalf("accepted overflowing %s", field)
		}
	}

	for _, field := range []string{"attack", "defense"} {
		if _, err := ParseConfig(configFixture(t, field, 0)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigStrictDocument(t *testing.T) {
	valid := string(configFixture(t, "max_health", 100))
	for _, input := range []string{"", "null", "[]", "{}", valid + valid, valid + " trailing", strings.Replace(valid, "\"max_health\":100", "\"max_health\":100,\"max_health\":101", 1), strings.Replace(valid, "\"max_health\":100", "\"unknown-sensitive-fixture\":100", 1)} {
		if _, err := ParseConfig([]byte(input)); err == nil {
			t.Fatalf("accepted invalid document %s", input)
		} else if strings.Contains(err.Error(), "sensitive-fixture") {
			t.Fatalf("exposed unknown input: %v", err)
		}
	}

	fields := []string{"max_health", "attack", "defense", "incapacitation_duration", "recovery_percent"}
	for _, field := range fields {
		var values map[string]json.RawMessage

		if err := json.Unmarshal([]byte(valid), &values); err != nil {
			t.Fatal(err)
		}

		delete(values, field)
		data, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ParseConfig(data); err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("missing %s: %v", field, err)
		}
	}
}

func TestConfigFailuresRetainInvalidConfigurationCategory(t *testing.T) {
	for _, data := range [][]byte{configFixture(t, "max_health", 0), configFixture(t, "recovery_percent", "sensitive-fixture"), []byte(`{"unknown-sensitive-fixture":1}`), []byte(`null`)} {
		_, err := ParseConfig(data)
		if !errors.Is(fmt.Errorf("startup tuning: %w", err), contract.ErrInvalidConfig) {
			t.Fatalf("configuration category lost: %v", err)
		}

		if strings.Contains(err.Error(), "sensitive-fixture") {
			t.Fatalf("configuration error exposed input: %v", err)
		}
	}
}
