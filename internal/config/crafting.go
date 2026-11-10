package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Crafting bounds separately operated local inference. Saved outcomes do not
// require Enabled or a reachable inference service.
type Crafting struct {
	Enabled          bool
	Endpoint         string
	Model            string
	Token            string
	Timeout          time.Duration
	MinInterval      time.Duration
	MaxResponseBytes int64
	MaxTokens        int
}

// LocalEndpoint accepts loopback/private addresses and local service names.
// The adapter also checks resolved addresses before opening a connection.
func LocalEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return !strings.Contains(host, ".") && !strings.ContainsAny(host, " :\\/\t\r\n")
}

func parseCrafting(get func(string) string) (Crafting, error) {
	c := Crafting{
		Endpoint:         get("REPLIPG_CRAFTING_ENDPOINT"),
		Model:            get("REPLIPG_CRAFTING_MODEL"),
		Token:            get("REPLIPG_CRAFTING_TOKEN"),
		Timeout:          2 * time.Minute,
		MinInterval:      time.Second,
		MaxResponseBytes: 65536,
		MaxTokens:        2048,
	}
	if raw := get("REPLIPG_CRAFTING_ENABLED"); raw != "" {
		if raw != "true" && raw != "false" {
			return Crafting{}, errors.New("REPLIPG_CRAFTING_ENABLED: expected true or false")
		}
		c.Enabled = raw == "true"
	}
	for _, setting := range []struct {
		key string
		dst *time.Duration
		max time.Duration
	}{
		{key: "REPLIPG_CRAFTING_TIMEOUT", dst: &c.Timeout, max: 10 * time.Minute},
		{key: "REPLIPG_CRAFTING_MIN_INTERVAL", dst: &c.MinInterval, max: time.Hour},
	} {
		if raw := get(setting.key); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 || value > setting.max {
				return Crafting{}, fmt.Errorf("%s: positive bounded duration required", setting.key)
			}
			*setting.dst = value
		}
	}
	for _, setting := range []struct {
		key string
		dst *int64
		min int64
		max int64
	}{
		{key: "REPLIPG_CRAFTING_MAX_RESPONSE_BYTES", dst: &c.MaxResponseBytes, min: 1024, max: 1048576},
	} {
		if raw := get(setting.key); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < setting.min || value > setting.max {
				return Crafting{}, fmt.Errorf("%s: bounded positive integer required", setting.key)
			}
			*setting.dst = value
		}
	}
	if raw := get("REPLIPG_CRAFTING_MAX_TOKENS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 128 || value > 8192 {
			return Crafting{}, errors.New("REPLIPG_CRAFTING_MAX_TOKENS: expected integer from 128 to 8192")
		}
		c.MaxTokens = value
	}
	if (c.Endpoint != "" || c.Enabled) && !LocalEndpoint(c.Endpoint) {
		return Crafting{}, errors.New("REPLIPG_CRAFTING_ENDPOINT: local HTTP service URL required")
	}
	if (c.Enabled && strings.TrimSpace(c.Model) == "") || strings.TrimSpace(c.Model) != c.Model || len(c.Model) > 128 || strings.ContainsAny(c.Model, "\r\n\t ") {
		return Crafting{}, errors.New("REPLIPG_CRAFTING_MODEL: local model name required")
	}
	if strings.ContainsAny(c.Token, "\r\n") {
		return Crafting{}, errors.New("REPLIPG_CRAFTING_TOKEN: invalid authentication token")
	}
	return c, nil
}
