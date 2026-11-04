// Package app composes and owns the executable runtime lifecycle.
package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/health"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
	"github.com/alexthvest/replipg/internal/telegram"
)

// Registrations supplies feature registration capabilities before admission.
type Registrations struct {
	Registry  *interaction.Registry
	Outbox    *outbox.Store
	Scheduler *scheduler.Service
}

// Options supplies explicit composition boundaries. TelegramBaseURL and HTTPClient
// support local Bot API integration tests; production leaves both unset.
type Options struct {
	Commands        []runtimecontract.CommandDeclaration
	Diagnostics     observability.DiagnosticSink
	Register        func(Registrations) error
	TelegramBaseURL string
	HTTPClient      *http.Client
	Listen          func(string, string) (net.Listener, error)
	Open            func(context.Context, persistence.Options) (*persistence.DB, error)
}

// StartupError retains safe stage and correlation metadata, never raw causes.
type StartupError struct {
	Stage         string
	CorrelationID runtimecontract.CorrelationID
}

func (e *StartupError) Error() string {
	return "runtime: " + e.Stage + " failed (" + string(e.CorrelationID) + ")"
}

// Runtime is a single-use owner of all application resources and goroutines.
type Runtime struct {
	cfg              config.Config
	options          Options
	state            *health.State
	ready            chan struct{}
	mu               sync.Mutex
	used             bool
	criticalFailed   bool
	draining         bool
	correlation      runtimecontract.CorrelationID
	newShutdownTimer func(time.Duration) runtimeTimer
}

// New validates the complete configuration without filesystem or network I/O.
func New(cfg config.Config, options Options) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := runtimecontract.ValidateCommandDeclarations(options.Commands); err != nil {
		return nil, err
	}
	if options.Commands != nil {
		options.Commands = append([]runtimecontract.CommandDeclaration{}, options.Commands...)
	}

	if options.Diagnostics == nil {
		options.Diagnostics = observability.NewJSONSink(os.Stderr)
	}

	if options.Listen == nil {
		options.Listen = net.Listen
	}

	if options.Open == nil {
		options.Open = persistence.Open
	}

	correlation, err := observability.GenerateCorrelationID()
	if err != nil {
		return nil, &StartupError{Stage: "correlation"}
	}

	return &Runtime{cfg: cfg, options: options, state: health.NewState(), ready: make(chan struct{}), correlation: correlation, newShutdownTimer: func(d time.Duration) runtimeTimer {
		return systemTimer{time.NewTimer(d)}
	}}, nil
}

func (r *Runtime) State() *health.State { return r.state }

// Ready closes after every startup prerequisite and owned poll-loop handshake.
func (r *Runtime) Ready() <-chan struct{} { return r.ready }

func (r *Runtime) emit(ctx context.Context, category observability.EventCategory, operation, outcome string, state health.Lifecycle) {
	r.options.Diagnostics.Emit(ctx, observability.Diagnostic{Category: category, CorrelationID: r.correlation, Operation: operation, Outcome: outcome, Fields: observability.SafeFields{State: string(state)}})
}

func (r *Runtime) lifecycle(ctx context.Context, state health.Lifecycle) {
	_ = r.state.SetLifecycle(state)
	r.emit(ctx, observability.CategoryHealth, observability.OperationStateChange, observability.OutcomeOK, state)
}

func (r *Runtime) failure(ctx context.Context, stage string) error {
	r.emit(ctx, observability.CategoryStartup, observability.OperationPrepare, observability.OutcomeFailed, "")
	return &StartupError{Stage: stage, CorrelationID: r.correlation}
}

// Run performs ordered startup and owns resources until all loops finish.
func (r *Runtime) Run(ctx context.Context) (runErr error) {
	r.mu.Lock()

	if r.used {
		r.mu.Unlock()
		return r.failure(ctx, "already_started")
	}

	r.used = true
	r.mu.Unlock()
	r.lifecycle(ctx, health.Starting)
	defer r.lifecycle(context.WithoutCancel(ctx), health.Stopped)

	if ctx.Err() != nil {
		return nil
	}

	startupCtx, cancelStartup := context.WithCancel(ctx)
	defer cancelStartup()
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	admissionCtx, stopAdmission := context.WithCancel(workCtx)
	defer stopAdmission()
	db, err := r.options.Open(startupCtx, persistence.Options{StorageDir: r.cfg.StorageDir, StorageFilesystem: r.cfg.StorageFilesystem, BusyTimeout: r.cfg.BusyTimeout})
	if err != nil {
		return r.failure(ctx, "storage")
	}

	defer func() { _ = db.Close() }()
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		return r.failure(ctx, "migrations")
	}

	if err = db.Migrate(startupCtx, migrations); err != nil {
		return r.failure(ctx, "migrations")
	}

	r.emit(ctx, observability.CategoryStorage, observability.OperationMigrate, observability.OutcomeCompleted, "")
	_ = r.state.SetDependency(health.Storage, true)
	listener, err := r.options.Listen("tcp", r.cfg.HealthAddress)
	if err != nil {
		return r.failure(ctx, "health_listener")
	}

	server := &http.Server{Handler: health.NewHandler(r.state, health.NewWorkStatus(db)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}

	var owned, healthOwned sync.WaitGroup
	var poller *telegram.Poller
	results := make(chan error, 4)
	start := func(stage string, fn func() error) {
		r.startRequired(workCtx, &owned, results, stage, fn)
	}
	r.startRequired(workCtx, &healthOwned, results, "health_server", func() error { return server.Serve(listener) })
	defer func() {
		r.drain(workCtx, &owned, poller, stopAdmission, cancelWork)
		_ = server.Close()
		healthOwned.Wait()
		if runErr == nil {
			select {
			case runErr = <-results:
			default:
			}
		}
	}()
	r.lifecycle(ctx, health.Recovering)
	deliveries := outbox.NewStore(db, outbox.Options{Diagnostics: r.options.Diagnostics})
	jobs := scheduler.New(db, systemClock{})

	if err = deliveries.Recover(startupCtx); err != nil {
		return r.failure(ctx, "outbox_recovery")
	}

	if err = jobs.Recover(startupCtx); err != nil {
		return r.failure(ctx, "scheduler_recovery")
	}

	r.emit(ctx, observability.CategoryStartup, observability.OperationRecover, observability.OutcomeCompleted, "")
	routes := interaction.NewRegistry()

	if r.options.Register != nil {
		if err = r.options.Register(Registrations{routes, deliveries, jobs}); err != nil {
			return r.failure(ctx, "registrations")
		}
	}

	routes.Seal()
	deliveries.Seal()
	jobs.SealRegistrations()
	r.emit(ctx, observability.CategoryStartup, observability.OperationPrepare, observability.OutcomeCompleted, "")

	if err = deliveries.ReconcilePending(startupCtx, r.cfg.HandlerTimeout); err != nil {
		return r.failure(ctx, "outbox_reconciliation")
	}

	client, err := telegram.NewClient(startupCtx, telegram.ClientConfig{Token: r.cfg.BotToken(), BaseURL: r.options.TelegramBaseURL, HTTPClient: r.options.HTTPClient, RequestTimeout: r.cfg.PollTimeout + 5*time.Second})
	if err != nil {
		return r.failure(ctx, "telegram_identity")
	}

	if r.options.Commands != nil {
		if err := client.SetGroupCommands(startupCtx, r.options.Commands); err != nil {
			return r.failure(ctx, "telegram_commands")
		}
	}
	_ = r.state.SetDependency(health.Telegram, true)
	sender := telegram.NewDelivery(client, time.Now)
	router := interaction.NewRouter(routes, db, db, deliveries, jobs, interaction.RouterOptions{Diagnostics: r.options.Diagnostics})
	processor, err := telegram.NewProcessor(boundedRouter{UpdateRouter: router, timeout: r.cfg.HandlerTimeout}, sender, telegram.ProcessorOptions{Diagnostics: r.options.Diagnostics})
	if err != nil {
		return r.failure(ctx, "processor")
	}

	poller, err = telegram.NewPoller(client, db, telegram.PollerOptions{TimeoutSeconds: int(r.cfg.PollTimeout / time.Second), ProcessUpdate: func(ctx context.Context, update telegram.DecodedUpdate) error {
		if err := r.admit(ctx); err != nil {
			return err
		}
		return processor.ProcessUpdate(ctx, update)
	}})
	if err != nil {
		return r.failure(ctx, "poller")
	}

	if err = poller.Initialize(startupCtx); err != nil {
		return r.failure(ctx, "intake_initialization")
	}

	outbound, err := outbox.NewWorker(deliveries, outbox.WorkerOptions{Delivery: deliveryPort{sender}})
	if err != nil {
		return r.failure(ctx, "outbox_worker")
	}

	scheduled, err := scheduler.NewWorker(jobs, scheduler.WorkerOptions{Owner: string(r.correlation), Lease: r.cfg.ClaimLease, NewWorkScope: func(tx runtimecontract.DBTX) runtimecontract.WorkScope { return workScope{tx, deliveries, jobs} }})
	if err != nil {
		return r.failure(ctx, "scheduler_worker")
	}

	outboxStarted, schedulerStarted := make(chan struct{}), make(chan struct{})
	if ctx.Err() != nil {
		return nil
	}
	start("outbox_worker", func() error { close(outboxStarted); return r.runOutbox(workCtx, outbound, admissionCtx) })
	start("scheduler_worker", func() error { close(schedulerStarted); return r.runScheduler(workCtx, scheduled, admissionCtx) })
	start("intake", func() error { return poller.Run(workCtx) })

	for _, started := range []<-chan struct{}{outboxStarted, schedulerStarted, poller.Running()} {
		select {
		case <-started:
		case err := <-results:
			return err
		case <-ctx.Done():
			return nil
		}
	}

	select {
	case err := <-results:
		return err
	case <-ctx.Done():
		return nil
	default:
	}

	r.mu.Lock()
	if r.criticalFailed {
		r.mu.Unlock()
		return <-results
	}
	for _, dependency := range []health.Dependency{health.Intake, health.Outbox, health.Scheduler} {
		_ = r.state.SetDependency(dependency, true)
	}

	r.lifecycle(ctx, health.Ready)
	close(r.ready)
	r.mu.Unlock()

	select {
	case <-ctx.Done():
		return nil
	case err := <-results:
		return err
	}
}

func validateConfig(c config.Config) error {
	values := map[string]string{config.EnvDevSmoke: strconv.FormatBool(c.DevSmoke), config.EnvBotToken: c.BotToken(), config.EnvStorageDir: c.StorageDir, config.EnvStorageFilesystem: string(c.StorageFilesystem), config.EnvHealthAddress: c.HealthAddress, config.EnvPollTimeout: c.PollTimeout.String(), config.EnvBusyTimeout: c.BusyTimeout.String(), config.EnvHandlerTimeout: c.HandlerTimeout.String(), config.EnvRetryInitial: c.RetryInitial.String(), config.EnvRetryMaximum: c.RetryMaximum.String(), config.EnvRetryAttempts: strconv.Itoa(c.RetryAttempts), config.EnvClaimLease: c.ClaimLease.String(), config.EnvShutdownDeadline: c.ShutdownDeadline.String(), config.EnvDeploymentMode: string(c.DeploymentMode)}
	_, err := config.NewLoader(func(k string) (string, bool) { v, ok := values[k]; return v, ok }).Load()

	return err
}

func (r *Runtime) startRequired(ctx context.Context, owned *sync.WaitGroup, results chan<- error, stage string, fn func() error) {
	owned.Add(1)
	go func() {
		defer owned.Done()
		err := fn()
		r.mu.Lock()
		expectedExit := err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, telegram.ErrPollerState) || errors.Is(err, http.ErrServerClosed)
		if (r.draining || ctx.Err() != nil) && expectedExit {
			r.mu.Unlock()
			return
		}
		r.criticalFailed = true
		r.lifecycle(ctx, health.Draining)
		r.mu.Unlock()
		category, operation := observability.CategoryHealth, observability.OperationProbe
		switch stage {
		case "intake":
			category, operation = observability.CategoryIntake, observability.OperationPoll
		case "outbox_worker":
			category, operation = observability.CategoryDelivery, observability.OperationAttempt
		case "scheduler_worker":
			category, operation = observability.CategoryScheduler, observability.OperationExecute
		}
		r.emit(ctx, category, operation, observability.OutcomeFailed, "")
		results <- &StartupError{Stage: stage, CorrelationID: r.correlation}
	}()
}

type outboxRunner interface {
	RunOnce(context.Context) (bool, error)
	WaitNext(context.Context, time.Duration) error
}

type schedulerRunner interface {
	RunOnce(context.Context) (bool, error)
}

func (r *Runtime) runOutbox(ctx context.Context, w outboxRunner, admission ...context.Context) error {
	claimCtx := ctx
	if len(admission) != 0 {
		claimCtx = admission[0]
	}
	for {
		if err := r.admit(claimCtx); err != nil {
			return err
		}
		done, err := w.RunOnce(ctx)
		if done || err != nil {
			outcome := observability.OutcomeCompleted

			if err != nil {
				outcome = observability.OutcomeFailed
			}

			r.emit(ctx, observability.CategoryDelivery, observability.OperationAttempt, outcome, "")
		}

		if err != nil && !errors.Is(err, outbox.ErrDeliveryNotCompleted) {
			return err
		}

		if !done {
			if err = w.WaitNext(claimCtx, time.Second); err != nil {
				return err
			}
		}
	}
}

func (r *Runtime) runScheduler(ctx context.Context, w schedulerRunner, admission ...context.Context) error {
	claimCtx := ctx
	if len(admission) != 0 {
		claimCtx = admission[0]
	}
	for {
		if err := r.admit(claimCtx); err != nil {
			return err
		}
		done, err := w.RunOnce(ctx)
		if done || err != nil {
			outcome := observability.OutcomeCompleted

			if err != nil {
				outcome = observability.OutcomeFailed
			}

			r.emit(ctx, observability.CategoryScheduler, observability.OperationExecute, outcome, "")
		}

		if err != nil && !errors.Is(err, scheduler.ErrJobNotCompleted) {
			return err
		}

		if !done {
			timer := time.NewTimer(time.Second)

			select {
			case <-claimCtx.Done():
				timer.Stop()
				return claimCtx.Err()
			case <-timer.C:
			}
		}
	}
}
