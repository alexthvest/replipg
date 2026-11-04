package interaction

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var ErrNoRoute = errors.New("interaction: no registered route")

// DeliveryStore records outbound intent using the current transaction.
type DeliveryStore interface {
	Enqueue(context.Context, runtimecontract.DBTX, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error)
}

// JobStore records scheduled work using the current transaction.
type JobStore interface {
	Schedule(context.Context, runtimecontract.DBTX, runtimecontract.JobRequest) (runtimecontract.JobID, error)
}

// RouterOptions supplies diagnostics and deterministic time and correlation generation.
type RouterOptions struct {
	Now              func() time.Time
	NewCorrelationID func() (runtimecontract.CorrelationID, error)
	Diagnostics      observability.DiagnosticSink
}

// DispatchOutcome describes a durably committed update. ImmediateResponses
// belongs to the caller and is populated only for a freshly completed update.
type DispatchOutcome struct {
	Status             persistence.UpdateStatus
	CorrelationID      runtimecontract.CorrelationID
	ImmediateResponses []runtimecontract.ResponseRequest
}

// Router combines feature route ownership with transactional dispatch.
type Router struct {
	*Registry
	transactions runtimecontract.Transactor
	cursors      persistence.CursorStore
	deliveries   DeliveryStore
	jobs         JobStore
	options      RouterOptions
}

// NewRouter composes stores that share the transaction owner's database.
func NewRouter(registry *Registry, transactions runtimecontract.Transactor, cursors persistence.CursorStore, deliveries DeliveryStore, jobs JobStore, options RouterOptions) *Router {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewCorrelationID == nil {
		options.NewCorrelationID = observability.GenerateCorrelationID
	}
	if options.Diagnostics == nil {
		options.Diagnostics = observability.NewJSONSink(os.Stderr)
	}
	return &Router{Registry: registry, transactions: transactions, cursors: cursors, deliveries: deliveries, jobs: jobs, options: options}
}

// Dispatch commits feature state, durable effects, completion and cursor together.
// Feature failures are terminal only after their transaction has rolled back.
// An invalid immediate response returns a typed ValidationError alongside a
// durable handler-failed outcome. If failure recording fails, the outcome is
// empty and the persistence error takes precedence.
func (r *Router) Dispatch(ctx context.Context, event Event) (DispatchOutcome, error) {
	if event == nil || (reflect.ValueOf(event).Kind() == reflect.Pointer && reflect.ValueOf(event).IsNil()) {
		return DispatchOutcome{}, ErrNoRoute
	}
	handler, ok := r.Resolve(event)
	if !ok {
		return r.RecordIgnored(ctx, event.UpdateID())
	}
	if preparing, ok := handler.(PreparingHandler); ok {
		var err error
		handler, err = prepareHandler(ctx, preparing, event)
		if err != nil {
			return DispatchOutcome{}, err
		}
	}
	outcome, err := r.newOutcome(ctx, event.UpdateID(), persistence.UpdateCompleted, "")
	if err != nil {
		return DispatchOutcome{}, err
	}
	result := DispatchOutcome{}
	recorded := false
	err = r.transactions.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		prior, found, err := previousOutcome(ctx, tx, outcome)
		if err != nil {
			return err
		}
		if found {
			result = prior
			return nil
		}
		scope := &transactionScope{tx: tx, deliveries: r.deliveries, jobs: r.jobs}
		if err := invokeHandler(ctx, handler, scope, event); err != nil {
			return err
		}
		responses, err := validatedResponses(scope.responses, r.options.Now())
		if err != nil {
			return &handlerFailure{code: "invalid_response", cause: err}
		}
		outcome.CompletedAt = r.options.Now().UTC()
		if err := r.cursors.RecordTerminal(ctx, tx, outcome); err != nil {
			return err
		}
		result = dispatchOutcome(outcome)
		result.ImmediateResponses = responses
		recorded = true
		return nil
	})
	if err == nil {
		if recorded {
			r.options.Diagnostics.Emit(ctx, observability.Diagnostic{Category: observability.CategoryDispatch, CorrelationID: result.CorrelationID, Operation: observability.OperationDispatch, Outcome: observability.OutcomeCompleted, Fields: observability.SafeFields{UpdateID: outcome.UpdateID}})
		}
		return result, nil
	}
	var failure *handlerFailure
	var txErr *persistence.TransactionError
	if !errors.As(err, &failure) || !errors.As(err, &txErr) || txErr.Stage != persistence.TransactionStageCallback || ctx.Err() != nil {
		return DispatchOutcome{}, err
	}
	outcome.Status = persistence.UpdateHandlerFailed
	outcome.FailureCode = failure.code
	result, err = r.recordTerminal(ctx, outcome)
	if err != nil {
		return DispatchOutcome{}, err
	}
	return result, failure.cause
}

// RecordIgnored completes an unsupported transport update with a known ID.
func (r *Router) RecordIgnored(ctx context.Context, id runtimecontract.UpdateID) (DispatchOutcome, error) {
	outcome, err := r.newOutcome(ctx, id, persistence.UpdateIgnored, "")
	if err != nil {
		return DispatchOutcome{}, err
	}
	return r.recordTerminal(ctx, outcome)
}

// RecordDecodeFailure completes a malformed payload whose update ID is known.
// Raw payloads and decoder errors are deliberately excluded from this API.
func (r *Router) RecordDecodeFailure(ctx context.Context, id runtimecontract.UpdateID) (DispatchOutcome, error) {
	outcome, err := r.newOutcome(ctx, id, persistence.UpdateDecodeFailed, "decode_failed")
	if err != nil {
		return DispatchOutcome{}, err
	}
	return r.recordTerminal(ctx, outcome)
}

func (r *Router) newOutcome(ctx context.Context, id runtimecontract.UpdateID, status persistence.UpdateStatus, code string) (persistence.UpdateOutcome, error) {
	if err := ctx.Err(); err != nil {
		return persistence.UpdateOutcome{}, err
	}
	correlation, err := r.options.NewCorrelationID()
	if err != nil {
		return persistence.UpdateOutcome{}, errors.New("interaction: correlation generation failed")
	}
	outcome := persistence.UpdateOutcome{UpdateID: id, Status: status, CorrelationID: correlation, FailureCode: code, CompletedAt: r.options.Now().UTC()}
	if err := outcome.Validate(); err != nil {
		return persistence.UpdateOutcome{}, err
	}
	return outcome, nil
}

func (r *Router) recordTerminal(ctx context.Context, outcome persistence.UpdateOutcome) (DispatchOutcome, error) {
	result := DispatchOutcome{}
	recorded := false
	err := r.transactions.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		prior, found, err := previousOutcome(ctx, tx, outcome)
		if err != nil {
			return err
		}
		if found {
			result = prior
			return nil
		}
		outcome.CompletedAt = r.options.Now().UTC()
		if err := r.cursors.RecordTerminal(ctx, tx, outcome); err != nil {
			return err
		}
		recorded = true
		result = dispatchOutcome(outcome)
		return nil
	})
	if err != nil {
		return DispatchOutcome{}, err
	}
	if recorded {
		diagnosticOutcome := observability.OutcomeFailed
		if outcome.Status == persistence.UpdateIgnored {
			diagnosticOutcome = observability.OutcomeIgnored
		}
		r.options.Diagnostics.Emit(ctx, observability.Diagnostic{Category: observability.CategoryDispatch, CorrelationID: result.CorrelationID, Operation: observability.OperationDispatch, Outcome: diagnosticOutcome, Fields: observability.SafeFields{UpdateID: outcome.UpdateID}})
	}
	return result, nil
}

func dispatchOutcome(outcome persistence.UpdateOutcome) DispatchOutcome {
	return DispatchOutcome{Status: outcome.Status, CorrelationID: outcome.CorrelationID}
}

// Read through the current transaction so concurrent dispatch cannot run a
// handler between a completion check and the immutable terminal write.
func previousOutcome(ctx context.Context, tx runtimecontract.DBTX, outcome persistence.UpdateOutcome) (DispatchOutcome, bool, error) {
	var status string
	var correlation string
	err := tx.QueryRowContext(ctx, `SELECT status,correlation_id FROM processed_updates WHERE update_id=?`, int64(outcome.UpdateID)).Scan(&status, &correlation)
	if err == nil {
		return DispatchOutcome{Status: persistence.UpdateStatus(status), CorrelationID: runtimecontract.CorrelationID(correlation)}, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DispatchOutcome{}, false, err
	}
	var cursor int64
	err = tx.QueryRowContext(ctx, `SELECT last_completed_id FROM runtime_cursors WHERE source='telegram'`).Scan(&cursor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DispatchOutcome{}, false, err
	}
	if outcome.UpdateID <= runtimecontract.UpdateID(cursor) {
		return DispatchOutcome{Status: persistence.UpdateIgnored, CorrelationID: outcome.CorrelationID}, true, nil
	}
	return DispatchOutcome{}, false, nil
}

type handlerFailure struct {
	code  string
	cause error
}

func (e *handlerFailure) Error() string { return "interaction: " + e.code }
func (e *handlerFailure) Unwrap() error { return e.cause }

func invokeHandler(ctx context.Context, handler Handler, scope *transactionScope, event Event) (err error) {
	defer func() {
		if recover() != nil {
			if scope.failure != nil {
				err = scope.failure
			} else if ctx.Err() != nil {
				err = ctx.Err()
			} else {
				err = &handlerFailure{code: "handler_panic"}
			}
		}
	}()
	result, err := handler.Handle(ctx, scope, event)
	scope.responses = result.ImmediateResponses
	if scope.failure != nil {
		return scope.failure
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if persistence.IsStorageFailure(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &handlerFailure{code: "handler_error"}
}

// transactionScope binds every feature effect to the dispatch transaction.
// Its capability exposes persistence only; no service can perform delivery here.
type transactionScope struct {
	tx         runtimecontract.DBTX
	deliveries DeliveryStore
	jobs       JobStore
	failure    error
	responses  []runtimecontract.ResponseRequest
}

var _ runtimecontract.WorkScope = (*transactionScope)(nil)

func (s *transactionScope) DBTX() runtimecontract.DBTX { return trackedDBTX{scope: s} }
func (s *transactionScope) track(err error) {
	if err != nil && s.failure == nil {
		s.failure = err
	}
}
func (s *transactionScope) EnqueueDelivery(ctx context.Context, req runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	id, err := s.deliveries.Enqueue(ctx, s.DBTX(), req)
	if persistence.IsStorageFailure(err) {
		s.track(err)
	}
	return id, err
}
func (s *transactionScope) Schedule(ctx context.Context, req runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	id, err := s.jobs.Schedule(ctx, s.DBTX(), req)
	if persistence.IsStorageFailure(err) {
		s.track(err)
	}
	return id, err
}

type trackedDBTX struct{ scope *transactionScope }

func (tx trackedDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	result, err := tx.scope.tx.ExecContext(ctx, query, args...)
	tx.scope.track(err)
	return result, err
}
func (tx trackedDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := tx.scope.tx.QueryContext(ctx, query, args...)
	tx.scope.track(err)
	return rows, err
}
func (tx trackedDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.scope.tx.QueryRowContext(ctx, query, args...)
}

// Snapshot the closed response set so handler-owned pointers and slices cannot
// change a validated request between commit and delivery.
func validatedResponses(requests []runtimecontract.ResponseRequest, now time.Time) ([]runtimecontract.ResponseRequest, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	responses := make([]runtimecontract.ResponseRequest, len(requests))
	for i, request := range requests {
		if err := runtimecontract.ValidateResponseAt(request, now); err != nil {
			return nil, err
		}
		switch response := request.(type) {
		case runtimecontract.PublicResponse:
			response.Content = cloneResponseContent(response.Content)
			responses[i] = response
		case *runtimecontract.PublicResponse:
			copied := *response
			copied.Content = cloneResponseContent(copied.Content)
			responses[i] = copied
		case runtimecontract.EphemeralResponse:
			responses[i] = cloneEphemeralResponse(response)
		case *runtimecontract.EphemeralResponse:
			responses[i] = cloneEphemeralResponse(*response)
		case runtimecontract.CallbackAcknowledgement:
			responses[i] = response
		case *runtimecontract.CallbackAcknowledgement:
			responses[i] = *response
		}
	}
	return responses, nil
}

func cloneResponseContent(content runtimecontract.MessageContent) runtimecontract.MessageContent {
	if content.Entities != nil {
		content.Entities = append([]runtimecontract.TextEntity{}, content.Entities...)
	}
	if content.Buttons != nil {
		rows := make([][]runtimecontract.InlineButton, len(content.Buttons))
		for i, row := range content.Buttons {
			rows[i] = append([]runtimecontract.InlineButton{}, row...)
		}
		content.Buttons = rows
	}
	return content
}

func cloneEphemeralResponse(response runtimecontract.EphemeralResponse) runtimecontract.EphemeralResponse {
	response.Content = cloneResponseContent(response.Content)
	switch authorization := response.Authorization.(type) {
	case *runtimecontract.CallbackAuthorization:
		copied := *authorization
		response.Authorization = &copied
	case *runtimecontract.IncomingEphemeralAuthorization:
		copied := *authorization
		response.Authorization = &copied
	case *runtimecontract.AdministratorAuthorization:
		response.Authorization = &runtimecontract.AdministratorAuthorization{}
	}
	return response
}

func prepareHandler(ctx context.Context, preparing PreparingHandler, event Event) (handler Handler, err error) {
	defer func() {
		if recover() != nil {
			handler = nil
			err = errors.New("interaction: preparation failed")
		}
	}()
	handler, err = preparing.Prepare(ctx, event)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if err != nil {
		return nil, errors.New("interaction: preparation failed")
	}
	if nilHandler(handler) {
		return nil, errors.New("interaction: invalid prepared handler")
	}
	return handler, nil
}
