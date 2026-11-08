package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
)

func TestOwnerBalanceCommand(t *testing.T) {
	for _, language := range []string{"en", "ru"} {
		t.Run(language, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.BankID = 900
			cfg.TreasuryID = 2
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fake := &shopTestBot{
				fakeBot:  &fakeBot{},
				requests: make(chan bankEnvelope, 16),
			}
			fake.script = func(ctx context.Context, router *telegram.Router) {
				defer cancel()
				router.Handle(ctx, message(1, -1, 1, "/admin add", 2))
				for _, actor := range []int64{2, 3} {
					router.Handle(ctx, message(2, -1, actor, "/replipg balance", 0))
					if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "permission") {
						t.Fatal("non-owner balance request not denied")
					}
				}
				for _, command := range []string{"/replipg", "/replipg balance extra", "/replipg unknown"} {
					router.Handle(ctx, message(3, -1, 1, command, 0))
					if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "Use /replipg balance") {
						t.Fatal("invalid command did not return usage")
					}
				}
				if len(fake.requests) != 0 {
					t.Fatal("denied or malformed command sent bank work")
				}

				if language == "ru" {
					router.Handle(ctx, message(4, -1, 1, "/settings", 0))
					menuID := len(fake.views)
					menu := waitMenu(t, fake.fakeBot, -1, menuID, nil)
					router.Handle(ctx, callback(5, -1, 1, menuID, menu.Rows[1][0].Data))
				}

				// Match production's one synchronous Telegram update handler:
				// the command must return before the bank reply can be dispatched.
				router.Handle(ctx, message(6, -1, 1, "/replipg@replipg_bot balance", 0))
				request := nextBankRequest(t, fake)
				if request.Method != "bank.balance.public.v1" || string(request.Params) != `{"accountId":2}` {
					t.Fatalf("unexpected balance request: %+v", request)
				}

				result := map[string]any{"data": map[string]any{"accountId": 2, "balance": strings.Repeat("9", 100)}}
				forged := bankReply(t, request.ID, result)
				forged.Message.From.ID = 901
				router.Handle(ctx, forged)
				router.Handle(ctx, bankReply(t, "unrelated", result))
				fake.mu.Lock()
				for _, view := range fake.views {
					if strings.Contains(textOf(view), strings.Repeat("9", 100)) {
						t.Error("untrusted reply completed balance command")
					}
				}
				fake.mu.Unlock()

				// A waiting read must leave the runtime available for gameplay.
				router.Handle(ctx, message(7, -2, 3, "/ping", 0))
				router.Handle(ctx, bankReply(t, request.ID, result))
				view := waitBalanceResult(t, fake.fakeBot, strings.Repeat("9", 100))
				want := "Bot bank balance"
				if language == "ru" {
					want = "Баланс бота"
				}
				if text := textOf(view); !strings.Contains(text, want) || !strings.Contains(text, strings.Repeat("9", 100)) {
					t.Fatalf("incorrect localized balance: %s", text)
				}
				before := len(fake.views)
				router.Handle(ctx, bankReply(t, request.ID, result))
				if len(fake.views) != before {
					t.Fatal("duplicate reply delivered another result")
				}
			}

			deps := defaults()
			deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
				return fake, nil
			}
			application, err := NewWith(cfg, logger(), deps)
			if err != nil {
				t.Fatal(err)
			}
			if err = application.Run(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func waitBalanceResult(t *testing.T, fake *fakeBot, match string) core.View {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		fake.mu.Lock()
		for _, view := range fake.views {
			if strings.Contains(textOf(view), match) {
				fake.mu.Unlock()
				return view
			}
		}
		fake.mu.Unlock()
		select {
		case <-timeout.C:
			t.Fatal("balance result was not delivered")
			return core.View{}
		case <-ticker.C:
		}
	}
}

type balanceSender func(context.Context, int64, string) error

func (f balanceSender) SendBank(ctx context.Context, id int64, body string) error {
	return f(ctx, id, body)
}

func TestBalanceReadFailuresAndCleanup(t *testing.T) {
	for _, mode := range []string{"cancel", "send-error", "wrong-account", "rpc-error", "zero"} {
		t.Run(mode, func(t *testing.T) {
			reader := &balanceReader{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sender := balanceSender(func(_ context.Context, _ int64, body string) error {
				var request bankEnvelope
				if err := json.Unmarshal([]byte(body), &request); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "cancel":
					cancel()
				case "send-error":
					return errors.New("offline")
				case "rpc-error":
					reader.reply(`{"jsonrpc":"2.0","id":"` + request.ID + `","error":{"code":-32601,"message":"Method unavailable"}}`)
				default:
					account := 2
					if mode == "wrong-account" {
						account = 3
					}
					result := map[string]any{"data": map[string]any{"accountId": account, "balance": "0"}}
					reader.reply(bankReply(t, request.ID, result).Message.Text)
				}
				return nil
			})
			amount, err := reader.read(ctx, sender, 900, 2)
			if mode == "zero" {
				if err != nil || amount != "0" {
					t.Fatalf("zero balance: %s %v", amount, err)
				}
			} else if err == nil {
				t.Fatal("expected failed read")
			}
			if len(reader.pending) != 0 {
				t.Fatal("completed read retained correlation")
			}
		})
	}
}

func TestBalanceDisabled(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/replipg balance", 0))
		if len(fake.views) != 1 || !strings.Contains(textOf(fake.views[0]), "unavailable") {
			t.Fatal("disabled bank did not return unavailable")
		}
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestBalanceReadTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader := &balanceReader{}
		sender := balanceSender(func(context.Context, int64, string) error {
			return nil
		})
		started := time.Now()
		_, err := reader.read(context.Background(), sender, 900, 2)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 10*time.Second {
			t.Fatalf("expected ten-second timeout, got %v after %s", err, time.Since(started))
		}
		if len(reader.pending) != 0 {
			t.Fatal("timed-out read retained correlation")
		}
	})
}

func TestBalanceWorkerShutdownWithPendingReads(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &shopTestBot{
		fakeBot:  &fakeBot{},
		requests: make(chan bankEnvelope, 16),
	}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/replipg balance", 0))
		nextBankRequest(t, fake)
		// Leave the read unanswered and enqueue another read behind it.
		router.Handle(ctx, message(2, -1, 1, "/replipg balance", 0))
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= 3*time.Second {
		t.Fatal("shutdown waited for the bank timeout")
	}
	if len(fake.requests) != 0 || len(fake.views) != 0 {
		t.Fatal("shutdown sent a queued read or delivered a cancelled result")
	}
}
