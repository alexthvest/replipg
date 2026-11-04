package outbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type schemaKey struct {
	kind    runtimecontract.DeliveryKind
	version uint16
}

// Store records intents using caller transactions and reads content-free status.
// It never sends messages or commits the caller's transaction.
type Store struct {
	db          runtimecontract.DBTX
	now         func() time.Time
	generateID  func() (runtimecontract.DeliveryID, error)
	mu          sync.RWMutex
	decoders    map[schemaKey]Decoder
	reconcilers map[schemaKey]Reconciler
	diagnostics observability.DiagnosticSink
	sealed      bool
}

// NewStore constructs a store over the persistence kernel's database capability.
func NewStore(db runtimecontract.DBTX, opts Options) *Store {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.GenerateID == nil {
		opts.GenerateID = generateID
	}
	if opts.Diagnostics == nil {
		opts.Diagnostics = observability.NewJSONSink(os.Stderr)
	}
	return &Store{db: db, now: opts.Now, generateID: opts.GenerateID, decoders: make(map[schemaKey]Decoder), reconcilers: make(map[schemaKey]Reconciler), diagnostics: opts.Diagnostics}
}

// Register declares a supported kind/version and its feature-owned decoder.
// All registrations must finish before readiness; Seal freezes the registry.
func (s *Store) Register(kind runtimecontract.DeliveryKind, version uint16, decoder Decoder) error {
	if kind == "" {
		return invalid("delivery.kind", runtimecontract.ReasonRequired)
	}
	if version == 0 {
		return invalid("payload.version", runtimecontract.ReasonOutOfRange)
	}
	if decoder == nil {
		return invalid("decoder", runtimecontract.ReasonRequired)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		return ErrRegistrySealed
	}
	key := schemaKey{kind, version}
	if _, exists := s.decoders[key]; exists {
		return ErrRegistrationConflict
	}
	s.decoders[key] = decoder
	return nil
}

// Seal prevents additional registrations, and is safe to call repeatedly.
// Runtime composition must seal before readiness. Enqueue is also available
// during setup, provided its kind and version are already registered.
func (s *Store) Seal() { s.mu.Lock(); defer s.mu.Unlock(); s.sealed = true }

// Enqueue validates and inserts an intent into the caller's transaction. Success
// means insertion succeeded; durability follows only when the transaction commits.
func (s *Store) Enqueue(ctx context.Context, tx runtimecontract.DBTX, req runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := req.Validate(); err != nil {
		return "", err
	}
	if isNilDBTX(tx) {
		return "", invalid("transaction", runtimecontract.ReasonRequired)
	}
	s.mu.RLock()
	decoder := s.decoders[schemaKey{req.Kind, req.Payload.Version()}]
	s.mu.RUnlock()
	if decoder == nil {
		return "", &UnsupportedVersionError{Version: req.Payload.Version()}
	}
	response, err := decode(decoder, req.Payload)
	if err != nil {
		return "", err
	}
	now := s.now().UTC()
	if err := runtimecontract.ValidateResponseAt(response, now); err != nil {
		return "", err
	}
	routing, err := responseRouting(response)
	if err != nil {
		return "", err
	}
	id, err := s.generateID()
	if err != nil {
		return "", errors.New("delivery identifier generation failed")
	}
	if id == "" {
		return "", invalid("delivery.id", runtimecontract.ReasonRequired)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO outbound_deliveries (
		id, kind, content_version, content, destination_chat_id, target_user_id,
		authorization_kind, callback_query_id, incoming_ephemeral_message_id,
		authorization_expires_at, replace_callback_message, state,
		max_attempts, initial_delay_ms, max_delay_ms, attempt_count, next_attempt_at,
		created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, 0, ?, ?, ?)
	ON CONFLICT(id) DO NOTHING`, id, req.Kind, req.Payload.Version(), req.Payload.Data(),
		routing.chat, routing.target, routing.authorization, routing.callback,
		routing.incoming, routing.expires, routing.replace,
		req.Retry.MaxAttempts, durationMilliseconds(req.Retry.InitialDelay), durationMilliseconds(req.Retry.MaxDelay),
		now.UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return "", storageError(ctx, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return "", storageError(ctx, err)
	}
	if count != 1 {
		return "", &ConflictError{}
	}
	return id, nil
}

// Status reads only operational columns, never content or authorization values.
// Unknown reason codes are replaced with a stable safe category.
func (s *Store) Status(ctx context.Context, id runtimecontract.DeliveryID) (DeliveryStatus, error) {
	return deliveryStatus(ctx, s.db, id)
}

// StatusInTransaction reads the same content-free status as Status using the
// caller's transaction, including changes that have not committed yet.
func (s *Store) StatusInTransaction(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.DeliveryID) (DeliveryStatus, error) {
	return deliveryStatus(ctx, tx, id)
}

func deliveryStatus(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.DeliveryID) (DeliveryStatus, error) {
	if err := ctx.Err(); err != nil {
		return DeliveryStatus{}, err
	}
	if id == "" {
		return DeliveryStatus{}, invalid("delivery.id", runtimecontract.ReasonRequired)
	}
	if isNilDBTX(tx) {
		return DeliveryStatus{}, invalid("transaction", runtimecontract.ReasonRequired)
	}
	var status DeliveryStatus
	var next, terminal sql.NullInt64
	var reason sql.NullString
	var created, updated int64
	rows, err := tx.QueryContext(ctx, `SELECT id, state, attempt_count, max_attempts,
		next_attempt_at, reason_code, created_at, updated_at, terminal_at
		FROM outbound_deliveries WHERE id = ?`, id)
	if err != nil {
		return DeliveryStatus{}, storageError(ctx, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return DeliveryStatus{}, storageError(ctx, err)
		}
		return DeliveryStatus{}, ErrNotFound
	}
	err = rows.Scan(&status.ID, &status.State,
		&status.Attempts, &status.MaxAttempts, &next, &reason, &created, &updated, &terminal)
	if err != nil {
		return DeliveryStatus{}, storageError(ctx, err)
	}
	if err := rows.Close(); err != nil {
		return DeliveryStatus{}, storageError(ctx, err)
	}
	status.CreatedAt = time.UnixMilli(created).UTC()
	status.UpdatedAt = time.UnixMilli(updated).UTC()
	if next.Valid {
		status.NextAttemptAt = time.UnixMilli(next.Int64).UTC()
	}
	if terminal.Valid {
		status.TerminalAt = time.UnixMilli(terminal.Int64).UTC()
	}
	if reason.Valid {
		status.ReasonCode = safeReason(reason.String)
	}
	return status, nil
}

func generateID() (runtimecontract.DeliveryID, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return runtimecontract.DeliveryID(hex.EncodeToString(random[:])), nil
}

func invalid(field string, reason runtimecontract.ValidationReason) error {
	return &runtimecontract.ValidationError{Field: field, Reason: reason}
}

func decode(decoder Decoder, payload runtimecontract.VersionedPayload) (response runtimecontract.ResponseRequest, err error) {
	defer func() {
		if recover() != nil {
			response = nil
			err = ErrDecode
		}
	}()
	response, err = decoder(payload)
	if err != nil {
		return nil, ErrDecode
	}
	return response, nil
}

// Keep raw driver errors out of public diagnostics while preserving cancellation.
func storageError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, persistence.ErrContention) {
		return persistence.ErrContention
	}
	if errors.Is(err, persistence.ErrPersistenceClosed) {
		return persistence.ErrPersistenceClosed
	}
	return ErrStorage
}

func isNilDBTX(tx runtimecontract.DBTX) bool {
	if tx == nil {
		return true
	}
	value := reflect.ValueOf(tx)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Round up without overflowing a maximum time.Duration.
func durationMilliseconds(duration time.Duration) int64 {
	milliseconds := duration.Milliseconds()
	if duration%time.Millisecond != 0 {
		milliseconds++
	}
	return milliseconds
}

func safeReason(reason string) string {
	switch reason {
	case "validation", "authorization", "conflict", "rate_limit", "transient", "permanent", "cancellation", "cancelled", "reconciliation_confirmed", "reconciliation_retry", "reconciliation_permanent", "rejected", "transient_error", "rate_limited", "retry_exhausted", "indeterminate", "interrupted", "invalid_request", "authorization_expired", "unsupported_version", "permanent_failure":
		return reason
	default:
		return "delivery_failed"
	}
}
