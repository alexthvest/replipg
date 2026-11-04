package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func commandEnvironment() map[string]string {
	return map[string]string{
		config.EnvBotToken: "123456:COMMAND_SECRET", config.EnvStorageDir: "/var/lib/replipg",
		config.EnvStorageFilesystem: "local", config.EnvHealthAddress: "127.0.0.1:8080",
		config.EnvPollTimeout: "30s", config.EnvBusyTimeout: "5s", config.EnvHandlerTimeout: "30s",
		config.EnvRetryInitial: "250ms", config.EnvRetryMaximum: "30s", config.EnvRetryAttempts: "5",
		config.EnvClaimLease: "1m", config.EnvShutdownDeadline: "20s", config.EnvDeploymentMode: "single",
	}
}

func commandLookup(env map[string]string) config.LookupEnv {
	return func(key string) (string, bool) { value, ok := env[key]; return value, ok }
}

type fakeRuntime struct {
	run func(context.Context) error
}

func (r fakeRuntime) Run(ctx context.Context) error { return r.run(ctx) }

func TestExecuteRejectsConfigurationBeforeConstruction(t *testing.T) {
	for _, tc := range []struct{ name, setting, value string }{
		{"missing token", config.EnvBotToken, ""},
		{"invalid token", config.EnvBotToken, "SECRET invalid token"},
		{"invalid storage", config.EnvStorageDir, "SECRET-relative-path"},
		{"invalid timeout", config.EnvPollTimeout, "SECRET-invalid-duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := commandEnvironment()
			env[tc.setting] = tc.value

			var output bytes.Buffer
			constructed := false
			code := execute(context.Background(), commandLookup(env), func(config.Config) (runtimeRunner, error) {
				constructed = true
				return fakeRuntime{}, nil
			}, &output)
			if code == 0 || constructed {
				t.Fatalf("invalid configuration: exit = %d, constructed = %v", code, constructed)
			}

			if !strings.Contains(output.String(), tc.setting) || strings.Contains(output.String(), "SECRET") {
				t.Fatalf("diagnostic must name only the invalid setting and safe reason: %q", output.String())
			}
		})
	}
}

func TestExecuteMapsRuntimeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		constructErr, runErr error
		wantCode, wantRuns   int
	}{
		{name: "graceful", wantRuns: 1},
		{name: "construction failure", constructErr: errors.New("SECRET constructor payload"), wantCode: 1},
		{name: "runtime failure", runErr: errors.New("SECRET runtime payload"), wantCode: 1, wantRuns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := commandEnvironment()
			ctx := context.WithValue(context.Background(), commandContextKey{}, "preserved")

			var output bytes.Buffer
			constructions, runs := 0, 0
			code := execute(ctx, commandLookup(env), func(cfg config.Config) (runtimeRunner, error) {
				constructions++

				if cfg.BotToken() != env[config.EnvBotToken] || cfg.StorageDir != env[config.EnvStorageDir] || cfg.ShutdownDeadline != 20*time.Second {
					t.Fatal("factory did not receive validated configuration")
				}

				return fakeRuntime{run: func(got context.Context) error {
					runs++

					if got != ctx {
						t.Fatal("runtime did not receive command context")
					}

					return tc.runErr
				}}, tc.constructErr
			}, &output)
			if code != tc.wantCode || constructions != 1 || runs != tc.wantRuns {
				t.Fatalf("exit = %d, constructions = %d, runs = %d", code, constructions, runs)
			}

			if strings.Contains(output.String(), "SECRET") || strings.Contains(output.String(), env[config.EnvStorageDir]) {
				t.Fatalf("diagnostic exposed private data: %q", output.String())
			}

			if (output.Len() > 0) != (tc.wantCode != 0) {
				t.Fatalf("unexpected diagnostic: %q", output.String())
			}
		})
	}
}

type commandContextKey struct{}

func TestExecutePassesCancellationToRuntimeAndWaitsForReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	returned := false

	var output bytes.Buffer
	code := execute(ctx, commandLookup(commandEnvironment()), func(config.Config) (runtimeRunner, error) {
		return fakeRuntime{run: func(ctx context.Context) error {
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("runtime context was not cancelled")
			}

			returned = true

			return nil
		}}, nil
	}, &output)
	if code != 0 || !returned || output.Len() != 0 {
		t.Fatalf("graceful cancelled runtime: exit = %d, returned = %v, diagnostic = %q", code, returned, output.String())
	}
}

func TestExecutePreservesSafeStartupErrorMetadata(t *testing.T) {
	for _, construction := range []bool{true, false} {
		t.Run(fmt.Sprintf("construction=%v", construction), func(t *testing.T) {
			safeErr := &app.StartupError{Stage: "identity", CorrelationID: "command-test-correlation"}
			wrappedErr := fmt.Errorf("SECRET raw wrapper: %w", safeErr)
			var output bytes.Buffer
			code := execute(context.Background(), commandLookup(commandEnvironment()), func(config.Config) (runtimeRunner, error) {
				if construction {
					return nil, wrappedErr
				}
				return fakeRuntime{run: func(context.Context) error { return wrappedErr }}, nil
			}, &output)
			if code == 0 || !strings.Contains(output.String(), safeErr.Stage) || !strings.Contains(output.String(), string(safeErr.CorrelationID)) {
				t.Fatalf("startup error lost safe diagnostic metadata: exit = %d, diagnostic = %q", code, output.String())
			}
			if strings.Contains(output.String(), "SECRET") {
				t.Fatalf("startup error exposed raw wrapper: %q", output.String())
			}
		})
	}
}

func TestProductionSmokeRequiresExplicitEnablement(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		options := productionOptions(config.Config{DevSmoke: enabled})
		if !enabled {
			if options.Register != nil {
				t.Fatal("default command registers development behavior")
			}
			continue
		}
		if options.Register == nil {
			t.Fatal("explicit smoke enablement ignored")
		}
		registry := interaction.NewRegistry()
		if err := options.Register(app.Registrations{Registry: registry}); err != nil {
			t.Fatal(err)
		}
		registry.Seal()
		identity := interaction.Identity{Update: 1, Chat: -100, User: 8, ChatKind: runtimecontract.ChatKindSupergroup}
		for _, event := range []interaction.Event{interaction.Command{Identity: identity, Name: "dev_smoke"}, interaction.Command{Identity: identity, Name: "dev_smoke_ephemeral"}, interaction.Callback{Identity: identity, Data: "dev_smoke:reply"}, interaction.GroupMessage{Identity: identity}} {
			if _, ok := registry.Resolve(event); !ok {
				t.Fatal("enabled command omitted development route")
			}
		}
	}
}
