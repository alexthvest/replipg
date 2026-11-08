// Package app is the explicit composition root for features and adapters.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/yohamta/donburi"
)

type Poller interface {
	telegram.Transport
	Identity() (int64, string)
	Start(context.Context, context.Context, *telegram.Router)
	Close() error
}

type Dependencies struct {
	Open    func(context.Context, string) (*sqlite.Database, error)
	Load    func(string) (*content.Bundle, error)
	Connect func(context.Context, string, *slog.Logger) (Poller, error)
}

func defaults() Dependencies {
	return Dependencies{
		Open: sqlite.Open,
		Load: cueassets.Load,
		Connect: func(ctx context.Context, token string, log *slog.Logger) (Poller, error) {
			return telegram.New(ctx, token, log)
		},
	}
}

type App struct {
	cfg  config.Config
	log  *slog.Logger
	deps Dependencies
}

func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	return NewWith(cfg, logger, defaults())
}

func NewWith(cfg config.Config, logger *slog.Logger, deps Dependencies) (*App, error) {
	if logger == nil || deps.Open == nil || deps.Load == nil || deps.Connect == nil {
		return nil, errors.New("app: logger and startup dependencies required")
	}
	if cfg.OwnerID <= 0 ||
		cfg.Token == "" ||
		cfg.DatabasePath == "" ||
		cfg.AssetsPath == "" ||
		cfg.ShutdownTimeout <= 0 ||
		cfg.MenuTTL <= 0 ||
		cfg.AssetDebounce <= 0 {
		return nil, errors.New("app: validated configuration required")
	}
	return &App{
		cfg:  cfg,
		log:  logger,
		deps: deps,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	db, err := a.deps.Open(ctx, a.cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("storage readiness: %w", err)
	}

	defer func() {
		_ = db.Close()
	}()

	bundle, err := a.deps.Load(a.cfg.AssetsPath)
	if err != nil {
		return fmt.Errorf("asset readiness: %w", err)
	}

	service, err := groups.New(db, core.UserID(a.cfg.OwnerID))
	if err != nil {
		return err
	}

	registry := runtime.NewRegistry()
	characterService, err := characters.New(db, core.NewID)
	if err != nil {
		return err
	}

	modules, err := newModules(service, characterService, bundle, a.cfg.MenuTTL)
	if err != nil {
		return err
	}

	if err = registry.Hook(runtime.Hook{
		Name: "groups",
		Run: func(ctx context.Context, world donburi.World) error {
			states, err := service.Load(ctx)
			if err != nil {
				return err
			}

			for _, state := range states {
				if err = modules.groupProjection.Apply(world, state); err != nil {
					return err
				}
			}
			return nil
		},
	}); err != nil {
		return err
	}

	routes, err := groupRoutes(registry, modules)
	if err != nil {
		return err
	}
	if err = itemRoutes(registry, modules, routes); err != nil {
		return err
	}

	if err = registry.Hook(runtime.Hook{
		Name:     "characters",
		Requires: []string{"groups"},
		Run: func(ctx context.Context, world donburi.World) error {
			all, err := characterService.Load(ctx)
			if err != nil {
				return err
			}

			for _, c := range all {
				if err = modules.characterProjection.Apply(world, c); err != nil {
					return err
				}
			}
			return nil
		},
	}); err != nil {
		return err
	}

	if err = characterRoutes(registry, modules, routes); err != nil {
		return err
	}

	if err = configureInventory(registry, modules, db, routes); err != nil {
		return err
	}
	if err = configureShop(registry, modules, db, routes, a.cfg.BankID, a.cfg.TreasuryID); err != nil {
		return err
	}
	balanceCall, err := balanceRoute(registry, modules)
	if err != nil {
		return err
	}
	bankCall, err := bankOperations(registry, modules)
	if err != nil {
		return err
	}
	menuCall, err := menuOperations(registry, modules)
	if err != nil {
		return err
	}

	publish, err := runtime.Bind(registry, "content", func(
		_ context.Context,
		_ donburi.World,
		_ core.Scope,
		candidate *content.Bundle,
	) (core.View, error) {
		if err := candidate.Compatible(modules.bundle); err != nil {
			return core.View{}, err
		}

		modules.bundle = candidate
		modules.sessions.Invalidate(menus.Dependency{Kind: "content"})
		return core.View{}, nil
	})
	if err != nil {
		return err
	}

	engine, err := runtime.New(registry, 128, operationTimeout)
	if err != nil {
		return err
	}

	if err = engine.Start(ctx); err != nil {
		return err
	}

	defer func() {
		drain, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
		defer cancel()

		_ = engine.Stop(drain)
	}()

	transport, err := a.deps.Connect(ctx, a.cfg.Token, a.log)
	if err != nil {
		return err
	}

	defer func() {
		_ = transport.Close()
	}()

	id, username := transport.Identity()
	router, err := telegram.NewRouter(id, username, transport, a.log)
	if err != nil {
		return err
	}
	router.OrderedMenus()
	bankContext, stopBank := context.WithCancel(context.Background())
	defer stopBank()
	bankDone := make(chan struct{})
	bankRuntime := bankOwner{
		engine: engine,
		scope: core.Scope{
			Group: -1,
			Actor: core.UserID(a.cfg.OwnerID),
		},
		call: bankCall,
	}
	balanceReads := &balanceReader{log: a.log}
	if err = router.OnBankReply(telegram.TrustedBankHandler(a.cfg.BankID, func(ctx context.Context, text string) error {
		if balanceReads.reply(text) {
			return nil
		}
		return bankRuntime.reply(ctx, text)
	})); err != nil {
		return err
	}
	go func() {
		defer close(bankDone)
		if sender, ok := transport.(appshop.Sender); ok {
			runBankWorker(bankContext, bankRuntime, sender, a.log)
		} else {
			<-bankContext.Done()
		}
	}()
	defer func() {
		stopBank()
		<-bankDone
	}()
	menuContext, stopMenus := context.WithCancel(context.Background())
	menusDone := make(chan struct{})
	go func() {
		defer close(menusDone)
		menus.Deliver(menuContext, menuOwner{
			engine: engine,
			scope: core.Scope{
				Group: -1,
				Actor: core.UserID(a.cfg.OwnerID),
			},
			call: menuCall,
		}, transport)
	}()
	defer func() {
		stopMenus()
		<-menusDone
	}()

	sender, _ := transport.(appshop.Sender)
	balanceContext, stopBalance := context.WithCancel(context.Background())
	balanceDone := make(chan struct{})
	balanceJobs := make(chan balanceJob, 16)
	go func() {
		defer close(balanceDone)
		runBalanceCommands(balanceContext, balanceJobs,
			balanceCommand(engine, balanceCall, balanceReads, sender, a.cfg.BankID, a.cfg.TreasuryID),
			transport, a.log)
	}()
	defer func() {
		stopBalance()
		<-balanceDone
	}()
	if err = router.Command("replipg", queueBalance(engine, balanceCall, balanceJobs)); err != nil {
		return err
	}

	for name, route := range routes {
		if name == "bind-menu" || name == "menu" {
			continue
		}

		if err = router.Command(name, func(ctx context.Context, scope core.Scope, input telegram.Input) (core.View, error) {
			return route(ctx, engine, scope, input)
		}); err != nil {
			return err
		}
	}

	if err = router.OnMenuSent(func(ctx context.Context, scope core.Scope, input telegram.Input) (core.View, error) {
		return routes["bind-menu"](ctx, engine, scope, input)
	}); err != nil {
		return err
	}

	if err = router.Callback("m", func(ctx context.Context, scope core.Scope, input telegram.Input) (core.View, error) {
		return routes["menu"](ctx, engine, scope, input)
	}); err != nil {
		return err
	}

	watcher, err := cueassets.Watch(context.Background(), a.cfg.AssetsPath, a.cfg.AssetDebounce, func(
		ctx context.Context,
		candidate *content.Bundle,
	) error {
		_, err := publish(ctx, engine, core.Scope{
			Group: -1,
			Actor: core.UserID(a.cfg.OwnerID),
		}, candidate)
		if err == nil {
			a.log.Info("assets published")
		}
		return err
	}, func(err error) {
		a.log.Error("asset reload rejected", "error", err)
	})
	if err != nil {
		return fmt.Errorf("asset watcher: %w", err)
	}

	defer watcher.Stop()

	delivery, cancelDelivery := context.WithCancel(context.Background())
	defer cancelDelivery()

	intake, cancelIntake := context.WithCancel(ctx)
	defer cancelIntake()

	joined := make(chan struct{})
	go func() {
		defer close(joined)

		transport.Start(intake, delivery, router)
	}()

	select {
	case <-ctx.Done():
	case <-joined:
		return errors.New("telegram intake stopped unexpectedly")
	case <-watcher.Done():
		cancelIntake()
		cancelDelivery()
		<-joined
		return watcher.Err()
	case <-engine.Done():
		cancelIntake()
		watcher.Stop()
		cancelDelivery()
		<-joined
		return errors.New("game owner stopped; reconstruct committed state before serving gameplay")
	}

	cancelIntake()
	watcher.Stop()
	stopBalance()
	<-balanceDone
	stopBank()
	<-bankDone
	stopMenus()
	<-menusDone
	drain, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()

	deadline := context.AfterFunc(drain, cancelDelivery)
	defer deadline()

	if err = engine.Stop(drain); err != nil {
		cancelDelivery()
		<-joined
		return err
	}

	select {
	case <-joined:
		return nil
	case <-drain.Done():
		cancelDelivery()
		<-joined
		return drain.Err()
	}
}

// operationTimeout is bounded separately from signal cancellation.
const operationTimeout = 10 * time.Second
