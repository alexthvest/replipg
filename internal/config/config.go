// Package config loads and validates operator-supplied runtime configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	EnvDevSmoke          = "REPLIPG_DEV_SMOKE"
	EnvBotToken          = "REPLIPG_BOT_TOKEN"
	EnvStorageDir        = "REPLIPG_STORAGE_DIR"
	EnvStorageFilesystem = "REPLIPG_STORAGE_FILESYSTEM"
	EnvHealthAddress     = "REPLIPG_HEALTH_ADDRESS"
	EnvPollTimeout       = "REPLIPG_POLL_TIMEOUT"
	EnvBusyTimeout       = "REPLIPG_BUSY_TIMEOUT"
	EnvHandlerTimeout    = "REPLIPG_HANDLER_TIMEOUT"
	EnvRetryInitial      = "REPLIPG_RETRY_INITIAL"
	EnvRetryMaximum      = "REPLIPG_RETRY_MAXIMUM"
	EnvRetryAttempts     = "REPLIPG_RETRY_ATTEMPTS"
	EnvClaimLease        = "REPLIPG_CLAIM_LEASE"
	EnvShutdownDeadline  = "REPLIPG_SHUTDOWN_DEADLINE"
	EnvDeploymentMode    = "REPLIPG_DEPLOYMENT_MODE"
)

const (
	minPollTimeout = time.Second
	maxPollTimeout = 50 * time.Second

	minBusyTimeout = time.Millisecond
	maxBusyTimeout = 30 * time.Second

	minHandlerTimeout = 100 * time.Millisecond
	maxHandlerTimeout = 5 * time.Minute

	minRetryInitial  = time.Millisecond
	maxRetryInitial  = time.Minute
	minRetryMaximum  = time.Millisecond
	maxRetryMaximum  = time.Hour
	minRetryAttempts = 1
	maxRetryAttempts = 100

	minClaimLease = time.Second
	maxClaimLease = 30 * time.Minute

	minShutdownDeadline = time.Second
	maxShutdownDeadline = 5 * time.Minute
)

var allSettings = []string{
	EnvBotToken,
	EnvStorageDir,
	EnvStorageFilesystem,
	EnvHealthAddress,
	EnvPollTimeout,
	EnvBusyTimeout,
	EnvHandlerTimeout,
	EnvRetryInitial,
	EnvRetryMaximum,
	EnvRetryAttempts,
	EnvClaimLease,
	EnvShutdownDeadline,
	EnvDeploymentMode,
}

// FilesystemKind is the operator assertion for the storage filesystem type.
type FilesystemKind string

// FilesystemLocal is the only filesystem kind supported by SQLite WAL mode.
const FilesystemLocal FilesystemKind = "local"

// DeploymentMode declares the number of runtime processes in a deployment.
type DeploymentMode string

// DeploymentSingle is the only supported deployment mode.
const DeploymentSingle DeploymentMode = "single"

// LookupEnv is the read-only environment capability used by Loader.
type LookupEnv func(string) (string, bool)

// Loader produces a fully validated runtime configuration.
type Loader interface {
	Load() (Config, error)
}

// EnvLoader loads runtime configuration from a read-only environment lookup.
type EnvLoader struct {
	lookup LookupEnv
}

// NewLoader constructs a Loader using lookup. Loader never mutates the
// supplied environment or performs network or filesystem operations.
func NewLoader(lookup LookupEnv) Loader {
	return EnvLoader{lookup: lookup}
}

// Config is validated runtime configuration. Its credential is deliberately
// private so reflection-free serialization cannot expose it accidentally.
type Config struct {
	botToken string
	DevSmoke bool

	StorageDir        string
	StorageFilesystem FilesystemKind
	HealthAddress     string
	PollTimeout       time.Duration
	BusyTimeout       time.Duration
	HandlerTimeout    time.Duration
	RetryInitial      time.Duration
	RetryMaximum      time.Duration
	RetryAttempts     int
	ClaimLease        time.Duration
	ShutdownDeadline  time.Duration
	DeploymentMode    DeploymentMode
}

// BotToken deliberately returns the credential for construction of the
// Telegram adapter. Operator-facing code should use Redacted instead.
func (c Config) BotToken() string {
	return c.botToken
}

// RedactedConfig is the operator-safe view of Config.
type RedactedConfig struct {
	DevSmoke           bool           `json:"dev_smoke"`
	BotTokenConfigured bool           `json:"bot_token_configured"`
	StorageDir         string         `json:"storage_dir"`
	StorageFilesystem  FilesystemKind `json:"storage_filesystem"`
	HealthAddress      string         `json:"health_address"`
	PollTimeout        time.Duration  `json:"poll_timeout"`
	BusyTimeout        time.Duration  `json:"busy_timeout"`
	HandlerTimeout     time.Duration  `json:"handler_timeout"`
	RetryInitial       time.Duration  `json:"retry_initial"`
	RetryMaximum       time.Duration  `json:"retry_maximum"`
	RetryAttempts      int            `json:"retry_attempts"`
	ClaimLease         time.Duration  `json:"claim_lease"`
	ShutdownDeadline   time.Duration  `json:"shutdown_deadline"`
	DeploymentMode     DeploymentMode `json:"deployment_mode"`
}

// Redacted returns an operator-safe view of the configuration.
func (c Config) Redacted() RedactedConfig {
	return RedactedConfig{
		DevSmoke:           c.DevSmoke,
		BotTokenConfigured: c.botToken != "",
		StorageDir:         c.StorageDir,
		StorageFilesystem:  c.StorageFilesystem,
		HealthAddress:      c.HealthAddress,
		PollTimeout:        c.PollTimeout,
		BusyTimeout:        c.BusyTimeout,
		HandlerTimeout:     c.HandlerTimeout,
		RetryInitial:       c.RetryInitial,
		RetryMaximum:       c.RetryMaximum,
		RetryAttempts:      c.RetryAttempts,
		ClaimLease:         c.ClaimLease,
		ShutdownDeadline:   c.ShutdownDeadline,
		DeploymentMode:     c.DeploymentMode,
	}
}

// Format prevents every fmt formatting verb from traversing Config and
// exposing its private credential.
func (c Config) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.Redacted())
}

// ConfigError identifies an invalid setting without retaining its value or an
// underlying parsing error that could include that value.
type ConfigError struct {
	Setting string
	Reason  string
}

// Error implements error.
func (e *ConfigError) Error() string {
	return "invalid configuration setting " + e.Setting + ": " + e.Reason
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return NewLoader(os.LookupEnv).Load()
}

// Load loads and validates every runtime setting before returning a Config.
func (l EnvLoader) Load() (Config, error) {
	if l.lookup == nil {
		return Config{}, invalid("environment", "lookup is required")
	}

	values := make(map[string]string, len(allSettings))

	for _, setting := range allSettings {
		value, ok := l.lookup(setting)
		if !ok || value == "" {
			return Config{}, invalid(setting, "is required")
		}

		values[setting] = value
	}

	if !validBotToken(values[EnvBotToken]) {
		return Config{}, invalid(EnvBotToken, "has invalid format")
	}

	if !validStorageDir(values[EnvStorageDir]) {
		return Config{}, invalid(EnvStorageDir, "must be an absolute clean non-root path")
	}

	if FilesystemKind(values[EnvStorageFilesystem]) != FilesystemLocal {
		return Config{}, invalid(EnvStorageFilesystem, "must assert local filesystem storage")
	}

	if !validHealthAddress(values[EnvHealthAddress]) {
		return Config{}, invalid(EnvHealthAddress, "must be a host and non-zero TCP port")
	}

	pollTimeout, ok := boundedDuration(values[EnvPollTimeout], minPollTimeout, maxPollTimeout)
	if !ok {
		return Config{}, invalid(EnvPollTimeout, "must be a duration within supported bounds")
	}

	busyTimeout, ok := boundedDuration(values[EnvBusyTimeout], minBusyTimeout, maxBusyTimeout)
	if !ok {
		return Config{}, invalid(EnvBusyTimeout, "must be a duration within supported bounds")
	}

	handlerTimeout, ok := boundedDuration(values[EnvHandlerTimeout], minHandlerTimeout, maxHandlerTimeout)
	if !ok {
		return Config{}, invalid(EnvHandlerTimeout, "must be a duration within supported bounds")
	}

	retryInitial, ok := boundedDuration(values[EnvRetryInitial], minRetryInitial, maxRetryInitial)
	if !ok {
		return Config{}, invalid(EnvRetryInitial, "must be a duration within supported bounds")
	}

	retryMaximum, ok := boundedDuration(values[EnvRetryMaximum], minRetryMaximum, maxRetryMaximum)
	if !ok || retryMaximum < retryInitial {
		return Config{}, invalid(EnvRetryMaximum, "must be a bounded duration not below retry initial")
	}

	retryAttempts, err := strconv.Atoi(values[EnvRetryAttempts])
	if err != nil || retryAttempts < minRetryAttempts || retryAttempts > maxRetryAttempts {
		return Config{}, invalid(EnvRetryAttempts, "must be an integer within supported bounds")
	}

	claimLease, ok := boundedDuration(values[EnvClaimLease], minClaimLease, maxClaimLease)
	if !ok || claimLease < handlerTimeout {
		return Config{}, invalid(EnvClaimLease, "must be bounded and not below handler timeout")
	}

	shutdownDeadline, ok := boundedDuration(values[EnvShutdownDeadline], minShutdownDeadline, maxShutdownDeadline)
	if !ok {
		return Config{}, invalid(EnvShutdownDeadline, "must be a duration within supported bounds")
	}

	if DeploymentMode(values[EnvDeploymentMode]) != DeploymentSingle {
		return Config{}, invalid(EnvDeploymentMode, "must select single-instance operation")
	}

	devSmoke := false
	if value, present := l.lookup(EnvDevSmoke); present {
		var err error
		devSmoke, err = strconv.ParseBool(value)
		if err != nil {
			return Config{}, invalid(EnvDevSmoke, "must be a boolean")
		}
	}

	return Config{
		DevSmoke:          devSmoke,
		botToken:          values[EnvBotToken],
		StorageDir:        values[EnvStorageDir],
		StorageFilesystem: FilesystemLocal,
		HealthAddress:     values[EnvHealthAddress],
		PollTimeout:       pollTimeout,
		BusyTimeout:       busyTimeout,
		HandlerTimeout:    handlerTimeout,
		RetryInitial:      retryInitial,
		RetryMaximum:      retryMaximum,
		RetryAttempts:     retryAttempts,
		ClaimLease:        claimLease,
		ShutdownDeadline:  shutdownDeadline,
		DeploymentMode:    DeploymentSingle,
	}, nil
}

func invalid(setting, reason string) *ConfigError {
	return &ConfigError{Setting: setting, Reason: reason}
}

func boundedDuration(value string, minimum, maximum time.Duration) (time.Duration, bool) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration < minimum || duration > maximum {
		return 0, false
	}

	return duration, true
}

func validStorageDir(value string) bool {
	if strings.IndexByte(value, 0) >= 0 || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}

	return value != string(filepath.Separator)
}

func validHealthAddress(value string) bool {
	host, portText, err := net.SplitHostPort(value)
	if err != nil || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
		return false
	}

	port, err := strconv.Atoi(portText)

	return err == nil && port >= 1 && port <= 65535
}

func validBotToken(value string) bool {
	id, secret, found := strings.Cut(value, ":")
	if !found || id == "" || secret == "" || strings.Contains(secret, ":") {
		return false
	}

	for _, char := range id {
		if char < '0' || char > '9' {
			return false
		}
	}

	for _, char := range secret {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_':
		default:
			return false
		}
	}

	return true
}
