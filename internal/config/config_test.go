package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:TOP_SECRET_bot-token"

func validEnvironment() map[string]string {
	return map[string]string{
		EnvBotToken:          testToken,
		EnvStorageDir:        "/var/lib/replipg",
		EnvStorageFilesystem: "local",
		EnvHealthAddress:     "127.0.0.1:8080",
		EnvPollTimeout:       "30s",
		EnvBusyTimeout:       "5s",
		EnvHandlerTimeout:    "30s",
		EnvRetryInitial:      "250ms",
		EnvRetryMaximum:      "30s",
		EnvRetryAttempts:     "5",
		EnvClaimLease:        "1m",
		EnvShutdownDeadline:  "20s",
		EnvDeploymentMode:    "single",
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoaderAcceptsValidConfiguration(t *testing.T) {
	cfg, err := NewLoader(mapLookup(validEnvironment())).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := cfg.BotToken(); got != testToken {
		t.Fatal("BotToken() did not return the configured credential")
	}

	if cfg.StorageDir != "/var/lib/replipg" || cfg.HealthAddress != "127.0.0.1:8080" {
		t.Fatalf("unexpected paths/addresses: %#v", cfg.Redacted())
	}

	if cfg.PollTimeout != 30*time.Second || cfg.BusyTimeout != 5*time.Second || cfg.HandlerTimeout != 30*time.Second {
		t.Fatalf("unexpected operation bounds: %#v", cfg.Redacted())
	}

	if cfg.RetryInitial != 250*time.Millisecond || cfg.RetryMaximum != 30*time.Second || cfg.RetryAttempts != 5 {
		t.Fatalf("unexpected retry bounds: %#v", cfg.Redacted())
	}

	if cfg.ClaimLease != time.Minute || cfg.ShutdownDeadline != 20*time.Second {
		t.Fatalf("unexpected lifecycle bounds: %#v", cfg.Redacted())
	}

	if cfg.DeploymentMode != DeploymentSingle || cfg.StorageFilesystem != FilesystemLocal {
		t.Fatalf("unexpected safety declarations: %#v", cfg.Redacted())
	}
}

func TestLoaderRejectsMissingSetting(t *testing.T) {
	for _, setting := range allSettings {
		t.Run(setting, func(t *testing.T) {
			env := validEnvironment()
			delete(env, setting)

			assertConfigError(t, NewLoader(mapLookup(env)), setting)
		})
	}
}

func TestLoaderRejectsInvalidSetting(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		value   string
	}{
		{name: "empty token", setting: EnvBotToken, value: ""},
		{name: "token whitespace", setting: EnvBotToken, value: "123456:secret value"},
		{name: "relative storage", setting: EnvStorageDir, value: "data"},
		{name: "unclean storage", setting: EnvStorageDir, value: "/var/lib/../replipg"},
		{name: "root storage", setting: EnvStorageDir, value: "/"},
		{name: "non-local storage", setting: EnvStorageFilesystem, value: "network"},
		{name: "invalid health address", setting: EnvHealthAddress, value: "8080"},
		{name: "zero health port", setting: EnvHealthAddress, value: "127.0.0.1:0"},
		{name: "poll malformed", setting: EnvPollTimeout, value: "soon"},
		{name: "poll zero", setting: EnvPollTimeout, value: "0s"},
		{name: "poll excessive", setting: EnvPollTimeout, value: "51s"},
		{name: "busy too small", setting: EnvBusyTimeout, value: "500us"},
		{name: "busy excessive", setting: EnvBusyTimeout, value: "31s"},
		{name: "handler too small", setting: EnvHandlerTimeout, value: "99ms"},
		{name: "handler excessive", setting: EnvHandlerTimeout, value: "5m1s"},
		{name: "retry initial zero", setting: EnvRetryInitial, value: "0s"},
		{name: "retry initial excessive", setting: EnvRetryInitial, value: "1m1s"},
		{name: "retry maximum zero", setting: EnvRetryMaximum, value: "0s"},
		{name: "retry maximum excessive", setting: EnvRetryMaximum, value: "1h1s"},
		{name: "retry attempts malformed", setting: EnvRetryAttempts, value: "many"},
		{name: "retry attempts zero", setting: EnvRetryAttempts, value: "0"},
		{name: "retry attempts excessive", setting: EnvRetryAttempts, value: "101"},
		{name: "lease too small", setting: EnvClaimLease, value: "999ms"},
		{name: "lease excessive", setting: EnvClaimLease, value: "30m1s"},
		{name: "shutdown too small", setting: EnvShutdownDeadline, value: "999ms"},
		{name: "shutdown excessive", setting: EnvShutdownDeadline, value: "5m1s"},
		{name: "multiple deployment", setting: EnvDeploymentMode, value: "multiple"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnvironment()
			env[tt.setting] = tt.value

			errText := assertConfigError(t, NewLoader(mapLookup(env)), tt.setting)
			if tt.value != "" && strings.Contains(errText, tt.value) {
				t.Fatalf("error exposed invalid input value: %s", errText)
			}
		})
	}
}

func TestLoaderRejectsUnsafeCrossSettingBounds(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		value   string
	}{
		{name: "retry maximum below initial", setting: EnvRetryMaximum, value: "100ms"},
		{name: "claim lease below handler bound", setting: EnvClaimLease, value: "20s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnvironment()
			env[tt.setting] = tt.value
			assertConfigError(t, NewLoader(mapLookup(env)), tt.setting)
		})
	}
}

func TestConfigurationFormattingAndRedactedViewHideToken(t *testing.T) {
	cfg, err := NewLoader(mapLookup(validEnvironment())).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	redacted := cfg.Redacted()
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	outputs := []string{
		fmt.Sprint(cfg),
		fmt.Sprintf("%v", cfg),
		fmt.Sprintf("%+v", cfg),
		fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%s", cfg),
		fmt.Sprint(redacted),
		string(encoded),
	}
	encodedConfig, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal(Config) error = %v", err)
	}

	outputs = append(outputs, string(encodedConfig))

	for _, output := range outputs {
		if strings.Contains(output, testToken) || strings.Contains(output, "TOP_SECRET") {
			t.Fatalf("formatted configuration exposed token in %q", output)
		}
	}

	if !redacted.BotTokenConfigured {
		t.Fatal("redacted view should report that a credential is configured")
	}
}

func TestConfigErrorsNeverExposeInputValues(t *testing.T) {
	env := validEnvironment()
	env[EnvBotToken] = "sensitive malformed credential"

	_, err := NewLoader(mapLookup(env)).Load()
	if err == nil {
		t.Fatal("Load() error = nil, want ConfigError")
	}

	if strings.Contains(err.Error(), env[EnvBotToken]) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("error exposed sensitive input: %v", err)
	}

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error type = %T, want *ConfigError", err)
	}

	if configErr.Setting != EnvBotToken || configErr.Reason == "" {
		t.Fatalf("ConfigError = %#v", configErr)
	}
}

func TestLoaderUsesOnlySuppliedReadOnlyLookup(t *testing.T) {
	env := validEnvironment()
	seen := make(map[string]int)
	lookup := func(key string) (string, bool) {
		seen[key]++
		value, ok := env[key]

		return value, ok
	}

	if _, err := NewLoader(lookup).Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(seen) != len(allSettings)+1 {
		t.Fatalf("looked up %d settings, want %d", len(seen), len(allSettings)+1)
	}

	for _, setting := range append(append([]string(nil), allSettings...), EnvDevSmoke) {
		if seen[setting] != 1 {
			t.Errorf("lookup count for %s = %d, want 1", setting, seen[setting])
		}
	}
}

func assertConfigError(t *testing.T, loader Loader, setting string) string {
	t.Helper()

	_, err := loader.Load()
	if err == nil {
		t.Fatalf("Load() error = nil, want error for %s", setting)
	}

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("Load() error type = %T, want *ConfigError", err)
	}

	if configErr.Setting != setting {
		t.Fatalf("ConfigError.Setting = %q, want %q (error: %v)", configErr.Setting, setting, err)
	}

	if configErr.Reason == "" {
		t.Fatal("ConfigError.Reason is empty")
	}

	return err.Error()
}

func TestOptionalDevSmokeConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, value               string
		present, enabled, invalid bool
	}{
		{name: "absent"},
		{name: "disabled", value: "false", present: true},
		{name: "enabled", value: "true", present: true, enabled: true},
		{name: "invalid", value: "PRIVATE invalid bool", present: true, invalid: true},
		{name: "empty", present: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnvironment()
			if tc.present {
				env[EnvDevSmoke] = tc.value
			}
			cfg, err := NewLoader(mapLookup(env)).Load()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), EnvDevSmoke) || strings.Contains(err.Error(), tc.value) && tc.value != "" {
					t.Fatal("unsafe or missing optional setting error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DevSmoke != tc.enabled || cfg.Redacted().DevSmoke != tc.enabled {
				t.Fatal("unexpected smoke enablement")
			}
		})
	}
}
