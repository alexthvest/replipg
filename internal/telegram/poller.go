package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// PollingAPI is the completion-controlled inbound Bot API boundary.
type PollingAPI interface {
	DeleteWebhook(context.Context, bool) error
	GetUpdates(context.Context, GetUpdatesRequest) ([]json.RawMessage, error)
}

// CompletedCursor reads only durable terminal update completion.
type CompletedCursor interface {
	CompletedUpdate(context.Context) (runtimecontract.UpdateID, error)
}

// PollerOptions supplies the explicit Telegram long-poll timeout in seconds.
type PollerOptions struct {
	TimeoutSeconds int
	// ProcessUpdate returns after routing and any post-commit delivery.
	// Durable cursor evidence, rather than a nil error, permits acknowledgement.
	ProcessUpdate func(context.Context, DecodedUpdate) error
	// Sleep overrides the cancellation-aware retry wait for deterministic tests.
	Sleep func(context.Context, time.Duration) error
}

var (
	ErrPollerState   = errors.New("telegram: polling session is not accepting work")
	ErrInvalidPoller = errors.New("telegram: invalid polling configuration")
	ErrInvalidCursor = errors.New("telegram: invalid completed cursor")
)

type pollerState uint8

const (
	pollerNew pollerState = iota
	pollerInitializing
	pollerInitialized
	pollerStopped
)

// Poller owns one polling session. Initialize starts no background goroutines;
// the caller owns its context and the subsequent polling lifecycle.
type Poller struct {
	api        PollingAPI
	cursor     CompletedCursor
	options    PollerOptions
	mu         sync.Mutex
	state      pollerState
	request    GetUpdatesRequest
	started    chan struct{}
	running    chan struct{}
	runOwned   bool
	pollCancel context.CancelFunc
}

// NewPoller validates dependencies before any startup side effects.
func NewPoller(api PollingAPI, cursor CompletedCursor, options PollerOptions) (*Poller, error) {
	if nilPollingDependency(api) || nilPollingDependency(cursor) || options.TimeoutSeconds < 0 {
		return nil, ErrInvalidPoller
	}
	return &Poller{api: api, cursor: cursor, options: options, started: make(chan struct{}), running: make(chan struct{})}, nil
}

// Started closes only after this session owns initialization and all startup
// prerequisites succeed. It does not imply an update has arrived or completed.
func (p *Poller) Started() <-chan struct{} { return p.started }

// Initialize disables webhook intake while preserving backlog, then prepares
// the exact durable successor offset. Failed or stopped sessions cannot restart.
func (p *Poller) Initialize(ctx context.Context) (err error) {
	p.mu.Lock()
	if p.state != pollerNew {
		p.mu.Unlock()
		return ErrPollerState
	}
	p.state = pollerInitializing
	p.mu.Unlock()
	defer func() {
		if err != nil {
			p.mu.Lock()
			p.state = pollerStopped
			p.mu.Unlock()
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = p.api.DeleteWebhook(ctx, false); err != nil {
		return fmt.Errorf("telegram: initialize webhook: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	stopped := p.state != pollerInitializing
	p.mu.Unlock()
	if stopped {
		return ErrPollerState
	}
	completed, err := p.cursor.CompletedUpdate(ctx)
	if err != nil {
		return fmt.Errorf("telegram: initialize cursor: %w", err)
	}
	if completed < 0 || completed == runtimecontract.UpdateID(math.MaxInt64) {
		return ErrInvalidCursor
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	if p.state != pollerInitializing {
		return ErrPollerState
	}
	p.request = GetUpdatesRequest{Offset: int64(completed) + 1, Limit: 1, Timeout: p.options.TimeoutSeconds, AllowedUpdates: []string{"message", "callback_query"}}
	p.state = pollerInitialized
	close(p.started)
	return nil
}

// NextRequest returns a detached snapshot only while the session accepts work.
// Reading a request never acknowledges or advances an update.
func (p *Poller) NextRequest() (GetUpdatesRequest, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != pollerInitialized {
		return GetUpdatesRequest{}, ErrPollerState
	}
	request := p.request
	request.AllowedUpdates = append([]string(nil), request.AllowedUpdates...)
	return request, nil
}

// StopAccepting closes admission and interrupts polling and retry waits.
// Already admitted processing retains the lifecycle owner's work context.
func (p *Poller) StopAccepting() {
	p.mu.Lock()
	p.state = pollerStopped
	cancel := p.pollCancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func nilPollingDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}
