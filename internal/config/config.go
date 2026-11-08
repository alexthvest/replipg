// Package config parses environment configuration without echoing secret values.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Token           string
	OwnerID         int64
	LogLevel        slog.Level
	DatabasePath    string
	AssetsPath      string
	AssetDebounce   time.Duration
	MenuTTL         time.Duration
	ShutdownTimeout time.Duration
	BankID          int64
	TreasuryID      int64
}

func FromEnv() (Config, error) {
	return Parse(os.Getenv)
}

func Parse(get func(string) string) (Config, error) {
	if get == nil {
		return Config{}, errors.New("configuration: environment reader required")
	}

	c := Config{Token: get("REPLIPG_BOT_TOKEN")}
	if strings.TrimSpace(c.Token) != c.Token || strings.ContainsAny(c.Token, "\r\n\t ") {
		return Config{}, errors.New("REPLIPG_BOT_TOKEN: invalid token")
	}

	parts := strings.Split(c.Token, ":")
	if len(parts) != 2 || parts[1] == "" {
		return Config{}, errors.New("REPLIPG_BOT_TOKEN: required bot token")
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return Config{}, errors.New("REPLIPG_BOT_TOKEN: invalid bot identity")
	}

	c.OwnerID, err = strconv.ParseInt(get("REPLIPG_BOT_OWNER_ID"), 10, 64)
	if err != nil || c.OwnerID <= 0 {
		return Config{}, errors.New("REPLIPG_BOT_OWNER_ID: positive integer required")
	}

	c.TreasuryID = 2
	for _, setting := range []struct {
		key     string
		dst     *int64
		maximum int64
	}{
		{
			key:     "REPLIPG_BANK_BOT_ID",
			dst:     &c.BankID,
			maximum: 9223372036854775807,
		},
		{
			key:     "REPLIPG_BANK_TREASURY_ID",
			dst:     &c.TreasuryID,
			maximum: 2147483647,
		},
	} {
		if value := get(setting.key); value != "" {
			*setting.dst, err = strconv.ParseInt(value, 10, 64)
			if err != nil || *setting.dst <= 0 || *setting.dst > setting.maximum || strings.TrimSpace(value) != value || strings.HasPrefix(value, "+") {
				return Config{}, fmt.Errorf("%s: positive numeric identity required", setting.key)
			}
		}
	}

	switch strings.ToLower(get("REPLIPG_LOG_LEVEL")) {
	case "", "info":
		c.LogLevel = slog.LevelInfo
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "warn":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return Config{}, errors.New("REPLIPG_LOG_LEVEL: expected debug, info, warn, or error")
	}

	for _, p := range []struct {
		key      string
		fallback string
		dst      *string
	}{
		{
			"REPLIPG_DATABASE_PATH",
			"./data/replipg.db",
			&c.DatabasePath,
		},
		{
			"REPLIPG_ASSETS_PATH",
			"./assets",
			&c.AssetsPath,
		},
	} {
		value := get(p.key)
		if value == "" {
			value = p.fallback
		}

		if strings.TrimSpace(value) == "" ||
			strings.ContainsRune(value, 0) ||
			strings.HasPrefix(value, "file:") ||
			value == ":memory:" {
			return Config{}, fmt.Errorf("%s: filesystem path required", p.key)
		}

		path, err := filepath.Abs(value)
		if err != nil {
			return Config{}, fmt.Errorf("%s: cannot resolve path", p.key)
		}

		*p.dst = path
	}

	for _, d := range []struct {
		key      string
		fallback time.Duration
		dst      *time.Duration
	}{
		{
			"REPLIPG_ASSET_DEBOUNCE",
			300 * time.Millisecond,
			&c.AssetDebounce,
		},
		{
			"REPLIPG_MENU_TTL",
			15 * time.Minute,
			&c.MenuTTL,
		},
		{
			"REPLIPG_SHUTDOWN_TIMEOUT",
			10 * time.Second,
			&c.ShutdownTimeout,
		},
	} {
		*d.dst = d.fallback
		if raw := get(d.key); raw != "" {
			*d.dst, err = time.ParseDuration(raw)
			if err != nil || *d.dst <= 0 {
				return Config{}, fmt.Errorf("%s: positive duration required", d.key)
			}
		}
	}

	if info, err := os.Stat(c.AssetsPath); err != nil || !info.IsDir() {
		return Config{}, errors.New("REPLIPG_ASSETS_PATH: existing directory required")
	}

	if info, err := os.Stat(c.DatabasePath); err == nil && info.IsDir() {
		return Config{}, errors.New("REPLIPG_DATABASE_PATH: file required")
	} else if err != nil && !os.IsNotExist(err) {
		return Config{}, errors.New("REPLIPG_DATABASE_PATH: cannot access path")
	}
	return c, nil
}
