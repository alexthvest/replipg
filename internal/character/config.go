package character

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math/big"

	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
)

// Config holds validated character tuning. Its value and accessors contain no
// mutable references, so a caller cannot change another service's tuning.
type Config struct {
	maxHealth              int64
	attack                 int64
	defense                int64
	incapacitationDuration time.Duration
	recoveryPercent        string
}

// MaxHealth is the starting maximum health copied into a new character.
func (c Config) MaxHealth() int64 { return c.maxHealth }

// Attack is the starting base attack copied into a new character.
func (c Config) Attack() int64 { return c.attack }

// Defense is the starting base defense copied into a new character.
func (c Config) Defense() int64 { return c.defense }

// IncapacitationDuration is the delay before an incapacitated character recovers.
func (c Config) IncapacitationDuration() time.Duration { return c.incapacitationDuration }

// RecoveryPercent returns the exact decimal percentage without floating-point conversion.
func (c Config) RecoveryPercent() string { return c.recoveryPercent }

//go:embed defaults.json
var productionDefaults string

//go:embed defaults_development.json
var developmentDefaults string

// LoadConfig loads production tuning, or the shorter development recovery delay.
// It performs no storage or network operations.
func LoadConfig(development bool) (Config, error) {
	defaults := productionDefaults
	if development {
		defaults = developmentDefaults
	}
	return ParseConfig([]byte(defaults))
}

// ParseConfig validates a complete JSON configuration. It rejects unknown and
// duplicate fields, missing values, and trailing documents. Errors identify only
// known settings and never include supplied values or raw JSON parser errors.
func ParseConfig(data []byte) (Config, error) {
	fields, err := configFields(data)
	if err != nil {
		return Config{}, err
	}

	var cfg Config

	for _, field := range []struct {
		name    string
		target  *int64
		minimum int64
	}{
		{"max_health", &cfg.maxHealth, 1}, {"attack", &cfg.attack, 0}, {"defense", &cfg.defense, 0},
	} {
		raw := fields[field.name]
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, field.target) != nil || *field.target < field.minimum {
			return Config{}, configError(field.name, "must be a bounded integer in its allowed range")
		}
	}

	var duration string

	if json.Unmarshal(fields["incapacitation_duration"], &duration) != nil {
		return Config{}, configError("incapacitation_duration", "must be a positive duration")
	}

	cfg.incapacitationDuration, err = time.ParseDuration(duration)
	if err != nil || cfg.incapacitationDuration <= 0 {
		return Config{}, configError("incapacitation_duration", "must be a positive duration")
	}

	if json.Unmarshal(fields["recovery_percent"], &cfg.recoveryPercent) != nil || !decimalPercent(cfg.recoveryPercent) {
		return Config{}, configError("recovery_percent", "must be a decimal string strictly between zero and 100")
	}

	return cfg, nil
}

func configFields(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, configError("document", "must be one JSON object")
	}

	fields := make(map[string]json.RawMessage, 5)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, configError("document", "must be valid JSON")
		}

		name, ok := token.(string)
		if !ok {
			return nil, configError("document", "must be valid JSON")
		}

		switch name {
		case "max_health", "attack", "defense", "incapacitation_duration", "recovery_percent":
		default:
			return nil, configError("document", "contains an unknown field")
		}

		if _, exists := fields[name]; exists {
			return nil, configError(name, "must appear once")
		}

		var raw json.RawMessage

		if err := decoder.Decode(&raw); err != nil {
			return nil, configError(name, "must be valid JSON")
		}

		fields[name] = raw
	}

	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, configError("document", "must be valid JSON")
	}

	if _, err := decoder.Token(); err != io.EOF {
		return nil, configError("document", "must contain no trailing data")
	}

	return fields, nil
}

func configError(field, message string) error {
	return fmt.Errorf("character config %s: %s: %w", field, message, contract.ErrInvalidConfig)
}

func decimalPercent(value string) bool {
	// Accept only unsigned decimal notation with digits on both sides of any dot.
	// big.Rat alone would also accept fractions and scientific notation.
	if value == "" {
		return false
	}

	dot := -1
	for i, c := range value {
		if c == '.' && dot < 0 {
			dot = i
			continue
		}

		if c < '0' || c > '9' {
			return false
		}
	}

	if dot == 0 || dot == len(value)-1 {
		return false
	}

	percent, ok := new(big.Rat).SetString(value)
	return ok && percent.Sign() > 0 && percent.Cmp(big.NewRat(100, 1)) < 0
}
