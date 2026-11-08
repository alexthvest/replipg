package app

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/yohamta/donburi"
)

// Balance reads are ephemeral and never participate in order settlement.
// Only the awaiting command consumes a reply; expired and duplicate replies are ignored.
type balanceReader struct {
	mu      sync.Mutex
	pending map[string]chan string
	log     *slog.Logger
}

func (r *balanceReader) reply(text string) bool {
	id, err := bank.ResponseID(text)
	if err != nil {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	response, ok := r.pending[id]
	if ok {
		select {
		case response <- text:
		default:
		}
	}
	return ok
}

func (r *balanceReader) read(ctx context.Context, sender appshop.Sender, bankID, accountID int64) (string, error) {
	id, err := core.NewID()
	if err != nil {
		return "", err
	}

	requestID := "balance-" + string(id)
	body, err := (bank.Protocol{}).BalanceRequest(requestID, accountID)
	if err != nil {
		return "", err
	}

	response := make(chan string, 1)
	r.mu.Lock()
	if len(r.pending) >= 16 {
		r.mu.Unlock()
		return "", errors.New("bank: too many balance reads")
	}
	if r.pending == nil {
		r.pending = make(map[string]chan string)
	}
	r.pending[requestID] = response
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
	}()

	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = sender.SendBank(readCtx, bankID, body); err != nil {
		return "", err
	}

	select {
	case text := <-response:
		return (bank.Protocol{}).DecodeBalance(requestID, accountID, text)
	case <-readCtx.Done():
		return "", readCtx.Err()
	}
}

type balanceInput struct {
	Args   string
	Amount string
	Failed bool
}

type balanceJob struct {
	scope core.Scope
	input telegram.Input
}

// queueBalance returns to the serial Telegram intake before waiting for a reply.
func queueBalance(
	engine *runtime.Engine,
	call func(context.Context, *runtime.Engine, core.Scope, balanceInput) (core.View, error),
	jobs chan<- balanceJob,
) telegram.Route {
	return func(ctx context.Context, scope core.Scope, input telegram.Input) (core.View, error) {
		request := balanceInput{Args: input.Args}
		view, err := call(ctx, engine, scope, request)
		if err != nil || len(view.Text) > 0 {
			return view, err
		}

		select {
		case <-ctx.Done():
			return core.View{}, ctx.Err()
		case jobs <- balanceJob{
			scope: scope,
			input: input,
		}:
			return core.View{}, nil
		default:
			request.Failed = true
			return call(ctx, engine, scope, request)
		}
	}
}

func runBalanceCommands(
	ctx context.Context,
	jobs <-chan balanceJob,
	command telegram.Route,
	transport telegram.Transport,
	log *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			if ctx.Err() != nil {
				return
			}
			view, err := command(ctx, job.scope, job.input)
			if ctx.Err() != nil {
				return
			}
			if err == nil && len(view.Text) > 0 {
				_, err = transport.Send(ctx, job.scope.Group, view)
			}
			if err != nil {
				log.Error("bank balance command failed", "group", job.scope.Group, "error", err)
			}
		}
	}
}

func balanceRoute(registry *runtime.Registry, m *modules) (
	func(context.Context, *runtime.Engine, core.Scope, balanceInput) (core.View, error), error,
) {
	return runtime.Bind(registry, "replipg", func(
		ctx context.Context,
		_ donburi.World,
		scope core.Scope,
		input balanceInput,
	) (core.View, error) {
		if !m.groups.IsOwner(scope.Actor) {
			return m.text(ctx, scope, "error.denied")
		}
		if input.Args != "balance" {
			return m.text(ctx, scope, "command.replipg.usage")
		}
		if input.Failed || m.bankID <= 0 {
			return m.text(ctx, scope, "command.replipg.unavailable")
		}
		if input.Amount == "" {
			return core.View{}, nil
		}

		language, err := m.localization.Language(ctx, scope.Group)
		if err != nil {
			return core.View{}, err
		}

		text, err := m.bundle.Locales().Render(language, "command.replipg.balance", map[string]string{
			"account": strconv.FormatInt(m.treasuryID, 10),
			"amount":  input.Amount,
		})
		return core.View{Text: text}, err
	})
}

func balanceCommand(
	engine *runtime.Engine,
	call func(context.Context, *runtime.Engine, core.Scope, balanceInput) (core.View, error),
	reader *balanceReader,
	sender appshop.Sender,
	bankID, accountID int64,
) telegram.Route {
	return func(ctx context.Context, scope core.Scope, input telegram.Input) (core.View, error) {
		request := balanceInput{Args: input.Args}
		view, err := call(ctx, engine, scope, request)
		if err != nil || len(view.Text) > 0 {
			return view, err
		}

		if sender != nil {
			request.Amount, err = reader.read(ctx, sender, bankID, accountID)
			if err != nil && reader.log != nil && ctx.Err() == nil {
				reader.log.Warn("bank balance read failed", "error", err)
			}
		}
		request.Failed = sender == nil || err != nil
		return call(ctx, engine, scope, request)
	}
}
