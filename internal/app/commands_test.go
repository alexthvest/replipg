package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestCommandsValidatedAndCopied(t *testing.T) {
	if _, err := New(testConfig(t), Options{Commands: []runtimecontract.CommandDeclaration{{Name: "/invalid"}}}); err == nil {
		t.Fatal("invalid menu accepted")
	}

	declarations := []runtimecontract.CommandDeclaration{{Name: "character", Description: "Inspect", Ephemeral: true}}
	runtime, err := New(testConfig(t), Options{Commands: declarations})
	if err != nil {
		t.Fatal(err)
	}

	declarations[0].Name = "changed"

	if runtime.options.Commands[0].Name != "character" {
		t.Fatal("menu aliased")
	}

	empty, err := New(testConfig(t), Options{Commands: []runtimecontract.CommandDeclaration{}})
	if err != nil || empty.options.Commands == nil {
		t.Fatal("empty menu lost management intent")
	}
}

func TestMenuPublicationBeforeReadinessAndAdmission(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			menu := `{"ok":true,"result":true}`
			if !success {
				menu = `{"ok":false,"error_code":403,"description":"private"}`
			}
			fake := testsupport.NewFakeTelegram(testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":{"id":123,"is_bot":true}}`)}, testsupport.TelegramResponse{Body: []byte(menu)}, testsupport.TelegramResponse{Body: []byte(`{"ok":true,"result":true}`)}, testsupport.TelegramResponse{WaitForCancellation: true})
			defer fake.Close()

			runtime, err := New(testConfig(t), Options{Commands: []runtimecontract.CommandDeclaration{{Name: "character", Description: "Inspect", Ephemeral: true}}, TelegramBaseURL: fake.URL(), HTTPClient: fake.Client()})
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()

			if success {
				select {
				case <-runtime.Ready():
				case <-ctx.Done():
					t.Fatal("never ready")
				}

				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			} else {
				err := <-done
				var startup *StartupError

				if !errors.As(err, &startup) || startup.Stage != "telegram_commands" {
					t.Fatalf("err=%v", err)
				}

				select {
				case <-runtime.Ready():
					t.Fatal("failure ready")
				default:
				}
			}
			requests := fake.Requests()

			if len(requests) < 2 || !strings.HasSuffix(requests[0].Path, "/getMe") || !strings.HasSuffix(requests[1].Path, "/setMyCommands") {
				t.Fatal("wrong startup ordering")
			}

			if !success && len(requests) != 2 {
				t.Fatal("polling after menu failure")
			}
		})
	}
}
