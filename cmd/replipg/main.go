package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/devsmoke"
)

type runtimeRunner interface {
	Run(context.Context) error
}

type runtimeFactory func(config.Config) (runtimeRunner, error)

func execute(ctx context.Context, lookup config.LookupEnv, factory runtimeFactory, stderr io.Writer) int {
	cfg, err := config.NewLoader(lookup).Load()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	runtime, err := factory(cfg)
	if err != nil {
		writeRuntimeError(stderr, "runtime construction failed", err)
		return 1
	}

	if err := runtime.Run(ctx); err != nil {
		writeRuntimeError(stderr, "runtime failed", err)
		return 1
	}

	return 0
}

func writeRuntimeError(stderr io.Writer, fallback string, err error) {
	var startupErr *app.StartupError
	if errors.As(err, &startupErr) && startupErr != nil {
		_, _ = fmt.Fprintln(stderr, startupErr)
		return
	}
	_, _ = fmt.Fprintln(stderr, fallback)
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return execute(ctx, os.LookupEnv, func(cfg config.Config) (runtimeRunner, error) {
		return app.New(cfg, productionOptions(cfg))
	}, os.Stderr)
}

func productionOptions(cfg config.Config) app.Options {
	options := app.Options{}
	if cfg.DevSmoke {
		options.Register = func(registrations app.Registrations) error {
			return devsmoke.Register(registrations.Registry, nil)
		}
	}
	return options
}

func main() {
	os.Exit(run())
}
