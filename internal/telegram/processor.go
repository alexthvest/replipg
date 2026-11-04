package telegram

import (
	"context"
	"errors"
	"os"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// UpdateRouter durably completes mapped and known-identity transport outcomes.
type UpdateRouter interface {
	Dispatch(context.Context, interaction.Event) (interaction.DispatchOutcome, error)
	RecordIgnored(context.Context, runtimecontract.UpdateID) (interaction.DispatchOutcome, error)
	RecordDecodeFailure(context.Context, runtimecontract.UpdateID) (interaction.DispatchOutcome, error)
}

// ProcessorOptions configures privacy-safe delivery diagnostics.
type ProcessorOptions struct{ Diagnostics observability.DiagnosticSink }

var (
	ErrInvalidProcessor = errors.New("telegram: invalid update processor configuration")
	ErrUpdateProcessing = errors.New("telegram: update processing did not durably complete")
)

// Processor connects intake with durable dispatch and immediate delivery.
// It owns no goroutines; ProcessUpdate runs within its caller's work lifetime.
type Processor struct {
	router      UpdateRouter
	sender      runtimecontract.Sender
	diagnostics observability.DiagnosticSink
}

// NewProcessor validates required dependencies without database or network I/O.
func NewProcessor(router UpdateRouter, sender runtimecontract.Sender, options ProcessorOptions) (*Processor, error) {
	if nilPollingDependency(router) || nilPollingDependency(sender) {
		return nil, ErrInvalidProcessor
	}
	if nilPollingDependency(options.Diagnostics) {
		options.Diagnostics = observability.NewJSONSink(os.Stderr)
	}
	return &Processor{router: router, sender: sender, diagnostics: options.Diagnostics}, nil
}

// ProcessUpdate is suitable for PollerOptions.ProcessUpdate. Callback
// acknowledgement precedes dispatch; its failure never prevents dispatch.
// A durable terminal outcome takes precedence over a handler validation error.
func (p *Processor) ProcessUpdate(ctx context.Context, update DecodedUpdate) error {
	if ctx.Err() != nil || update.UpdateID <= 0 {
		return ErrUpdateProcessing
	}
	switch update.Status {
	case DecodeMapped:
		if nilPollingDependency(update.Event) || update.Event.UpdateID() != update.UpdateID {
			return ErrUpdateProcessing
		}
	case DecodeIgnored, DecodeFailed:
	default:
		return ErrUpdateProcessing
	}
	var ackErr error
	if update.CallbackQueryID != "" {
		ackErr = p.sender.AcknowledgeCallback(ctx, runtimecontract.CallbackAcknowledgement{CallbackQueryID: string(update.CallbackQueryID)})
	}
	var outcome interaction.DispatchOutcome
	var err error
	switch update.Status {
	case DecodeMapped:
		outcome, err = p.router.Dispatch(ctx, update.Event)
	case DecodeIgnored:
		outcome, err = p.router.RecordIgnored(ctx, update.UpdateID)
	case DecodeFailed:
		outcome, err = p.router.RecordDecodeFailure(ctx, update.UpdateID)
	}
	if outcome.Status == "" {
		return ErrUpdateProcessing
	}
	// Router errors can accompany a durable handler_failed outcome. No raw
	// handler, database, or transport errors cross this boundary.
	if err != nil && outcome.Status != persistence.UpdateHandlerFailed {
		return ErrUpdateProcessing
	}
	if update.CallbackQueryID != "" {
		p.report(ctx, outcome.CorrelationID, observability.CategoryCallbackAck, observability.OperationAcknowledge, ackErr)
	}
	switch outcome.Status {
	case persistence.UpdateCompleted:
		for _, request := range outcome.ImmediateResponses {
			operation, sendErr := p.send(ctx, request)
			p.report(ctx, outcome.CorrelationID, observability.CategoryDelivery, operation, sendErr)
		}
	case persistence.UpdateHandlerFailed, persistence.UpdateIgnored, persistence.UpdateDecodeFailed:
	default:
		return ErrUpdateProcessing
	}
	return nil
}

func (p *Processor) send(ctx context.Context, request runtimecontract.ResponseRequest) (string, error) {
	// Defensive validation also rejects typed nils before pointer dereferences.
	if err := runtimecontract.ValidateResponse(request); err != nil {
		return observability.OperationSend, err
	}
	switch response := request.(type) {
	case runtimecontract.PublicResponse:
		_, err := p.sender.SendPublic(ctx, response)
		return observability.OperationSendPublic, err
	case *runtimecontract.PublicResponse:
		return p.send(ctx, *response)
	case runtimecontract.EphemeralResponse:
		_, err := p.sender.SendEphemeral(ctx, response)
		return observability.OperationSendEphemeral, err
	case *runtimecontract.EphemeralResponse:
		return p.send(ctx, *response)
	case runtimecontract.CallbackAcknowledgement:
		return observability.OperationAcknowledge, p.sender.AcknowledgeCallback(ctx, response)
	case *runtimecontract.CallbackAcknowledgement:
		return p.send(ctx, *response)
	default:
		return observability.OperationSend, ErrUpdateProcessing
	}
}

func (p *Processor) report(ctx context.Context, correlation runtimecontract.CorrelationID, category observability.EventCategory, operation string, err error) {
	result := observability.OutcomeOK
	if err != nil {
		result = observability.OutcomeFailed
	}
	p.diagnostics.Emit(ctx, observability.Diagnostic{Category: category, CorrelationID: correlation, Operation: operation, Outcome: result})
}
