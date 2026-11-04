package runtimecontract

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

var (
	_ DBTX = (*sql.DB)(nil)
	_ DBTX = (*sql.Tx)(nil)
)

func TestIdentifierValuesRemainOpaqueAndLossless(t *testing.T) {
	updateID := UpdateID(-1 << 62)
	chatID := ChatID(1<<62 - 1)
	userID := UserID(-42)
	deliveryID := DeliveryID("delivery-01HZ")
	jobID := JobID("job-01HZ")
	correlationID := CorrelationID("correlation-01HZ")

	if int64(updateID) != -1<<62 {
		t.Fatalf("UpdateID lost its signed 64-bit value: %d", updateID)
	}
	if int64(chatID) != 1<<62-1 {
		t.Fatalf("ChatID lost its signed 64-bit value: %d", chatID)
	}
	if int64(userID) != -42 {
		t.Fatalf("UserID lost its signed 64-bit value: %d", userID)
	}
	if string(deliveryID) != "delivery-01HZ" || string(jobID) != "job-01HZ" || string(correlationID) != "correlation-01HZ" {
		t.Fatalf("opaque identifiers changed: delivery=%q job=%q correlation=%q", deliveryID, jobID, correlationID)
	}
}

func TestResponseRequestsAcceptSupportedVisibilityAndContent(t *testing.T) {
	destination := GroupDestination{ChatID: -100123, Kind: ChatKindSupergroup}
	validUntil := time.Unix(1_800_000_000, 0)
	content := MessageContent{
		Text:     "hello world",
		Entities: []TextEntity{{Kind: TextEntityBold, Offset: 0, Length: 5}},
		Buttons:  [][]InlineButton{{{Text: "Choose", CallbackData: "choice:1"}}},
	}
	tests := []struct {
		name string
		req  ResponseRequest
	}{
		{name: "public", req: PublicResponse{Destination: destination, Content: content}},
		{name: "ephemeral callback", req: EphemeralResponse{Destination: destination, TargetUserID: 42, Authorization: CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: validUntil}, Content: content}},
		{name: "ephemeral incoming", req: EphemeralResponse{Destination: destination, TargetUserID: 42, Authorization: IncomingEphemeralAuthorization{EphemeralMessageID: 7, ValidUntil: validUntil}, Content: content}},
		{name: "ephemeral administrator", req: EphemeralResponse{Destination: destination, TargetUserID: 42, Authorization: AdministratorAuthorization{}, Content: content}},
		{name: "callback acknowledgement", req: CallbackAcknowledgement{CallbackQueryID: "callback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateResponse(tt.req); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	ephemeral := EphemeralResponse{Destination: destination, TargetUserID: 42, Authorization: CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: validUntil}, Content: content}
	if err := ValidateResponseAt(ephemeral, validUntil.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("ValidateAt() before expiry error = %v", err)
	}
}

func TestResponseValidationReturnsTypedContentFreeReasons(t *testing.T) {
	validDestination := GroupDestination{ChatID: -10, Kind: ChatKindGroup}
	validContent := MessageContent{Text: "private-secret"}
	validUntil := time.Unix(1_800_000_000, 0)
	tooLong := strings.Repeat("x", maxMessageTextUTF16+1)
	tests := []struct {
		name   string
		req    ResponseRequest
		field  string
		reason ValidationReason
	}{
		{name: "missing destination", req: PublicResponse{Destination: GroupDestination{Kind: ChatKindGroup}, Content: validContent}, field: "destination.chat_id", reason: ReasonRequired},
		{name: "nonnegative group destination", req: PublicResponse{Destination: GroupDestination{ChatID: 10, Kind: ChatKindGroup}, Content: validContent}, field: "destination.chat_id", reason: ReasonOutOfRange},
		{name: "private destination", req: PublicResponse{Destination: GroupDestination{ChatID: -10, Kind: "private"}, Content: validContent}, field: "destination.kind", reason: ReasonUnsupported},
		{name: "empty text", req: PublicResponse{Destination: validDestination}, field: "content.text", reason: ReasonRequired},
		{name: "text too long", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: tooLong}}, field: "content.text", reason: ReasonOutOfRange},
		{name: "invalid utf8", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: string([]byte{0xff})}}, field: "content.text", reason: ReasonInvalid},
		{name: "unsupported entity", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Entities: []TextEntity{{Kind: "secret-kind", Length: 1}}}}, field: "content.entities.kind", reason: ReasonUnsupported},
		{name: "entity range", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Entities: []TextEntity{{Kind: TextEntityBold, Offset: 1, Length: 1}}}}, field: "content.entities.range", reason: ReasonOutOfRange},
		{name: "entity starts within surrogate pair", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "😀", Entities: []TextEntity{{Kind: TextEntityBold, Offset: 1, Length: 1}}}}, field: "content.entities.range", reason: ReasonInvalid},
		{name: "entity ends within surrogate pair", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "😀", Entities: []TextEntity{{Kind: TextEntityBold, Offset: 0, Length: 1}}}}, field: "content.entities.range", reason: ReasonInvalid},
		{name: "link missing url", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Entities: []TextEntity{{Kind: TextEntityTextLink, Length: 1}}}}, field: "content.entities.url", reason: ReasonRequired},
		{name: "non-link url", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Entities: []TextEntity{{Kind: TextEntityBold, Length: 1, URL: "secret-url"}}}}, field: "content.entities.url", reason: ReasonInconsistent},
		{name: "empty button row", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{}}}}, field: "content.buttons.row", reason: ReasonRequired},
		{name: "button text", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{{CallbackData: "secret-data"}}}}}, field: "content.buttons.text", reason: ReasonRequired},
		{name: "button callback", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{{Text: "secret-label"}}}}}, field: "content.buttons.callback_data", reason: ReasonRequired},
		{name: "button callback too long", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{{Text: "label", CallbackData: strings.Repeat("s", 65)}}}}}, field: "content.buttons.callback_data", reason: ReasonOutOfRange},
		{name: "button text invalid utf8", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{{Text: string([]byte{0xff}), CallbackData: "callback"}}}}}, field: "content.buttons.text", reason: ReasonInvalid},
		{name: "button callback invalid utf8", req: PublicResponse{Destination: validDestination, Content: MessageContent{Text: "x", Buttons: [][]InlineButton{{{Text: "label", CallbackData: string([]byte{0xff})}}}}}, field: "content.buttons.callback_data", reason: ReasonInvalid},
		{name: "missing target", req: EphemeralResponse{Destination: validDestination, Authorization: AdministratorAuthorization{}, Content: validContent}, field: "target.user_id", reason: ReasonRequired},
		{name: "negative target", req: EphemeralResponse{Destination: validDestination, TargetUserID: -1, Authorization: AdministratorAuthorization{}, Content: validContent}, field: "target.user_id", reason: ReasonOutOfRange},
		{name: "bot target", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, TargetIsBot: true, Authorization: AdministratorAuthorization{}, Content: validContent}, field: "target.user_id", reason: ReasonInvalid},
		{name: "missing authorization", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Content: validContent}, field: "authorization", reason: ReasonRequired},
		{name: "missing callback context", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: CallbackAuthorization{ValidUntil: validUntil}, Content: validContent}, field: "authorization.callback_query_id", reason: ReasonRequired},
		{name: "missing callback expiry", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: CallbackAuthorization{CallbackQueryID: "secret"}, Content: validContent}, field: "authorization.valid_until", reason: ReasonRequired},
		{name: "invalid incoming ephemeral", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: IncomingEphemeralAuthorization{}, Content: validContent}, field: "authorization.ephemeral_message_id", reason: ReasonOutOfRange},
		{name: "missing incoming expiry", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: IncomingEphemeralAuthorization{EphemeralMessageID: 1}, Content: validContent}, field: "authorization.valid_until", reason: ReasonRequired},
		{name: "typed nil authorization", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: (*CallbackAuthorization)(nil), Content: validContent}, field: "authorization", reason: ReasonRequired},
		{name: "replace ephemeral callback", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: CallbackAuthorization{CallbackQueryID: "secret", OriginatedFromEphemeral: true, ValidUntil: validUntil}, ReplaceCallbackMessage: true, Content: validContent}, field: "authorization.replace_callback_message", reason: ReasonInconsistent},
		{name: "replace without callback", req: EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: AdministratorAuthorization{}, ReplaceCallbackMessage: true, Content: validContent}, field: "authorization.replace_callback_message", reason: ReasonInconsistent},
		{name: "missing acknowledgement", req: CallbackAcknowledgement{}, field: "callback_query_id", reason: ReasonRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationError(t, ValidateResponse(tt.req), tt.field, tt.reason)
		})
	}
	t.Run("expired authorization", func(t *testing.T) {
		req := EphemeralResponse{Destination: validDestination, TargetUserID: 1, Authorization: CallbackAuthorization{CallbackQueryID: "secret", ValidUntil: validUntil}, Content: validContent}
		assertValidationError(t, ValidateResponseAt(req, validUntil), "authorization.valid_until", ReasonOutOfRange)
	})
}

func TestTypedNilResponseRequestsReturnValidationError(t *testing.T) {
	var public *PublicResponse
	var ephemeral *EphemeralResponse
	var acknowledgement *CallbackAcknowledgement
	for name, request := range map[string]ResponseRequest{
		"public":                   public,
		"ephemeral":                ephemeral,
		"callback acknowledgement": acknowledgement,
	} {
		t.Run(name, func(t *testing.T) {
			assertValidationError(t, validateResponseRequest(request), "response", ReasonRequired)
		})
	}
}

func validateResponseRequest(request ResponseRequest) error {
	return ValidateResponse(request)
}

func TestDurableWorkValuesValidateAndProtectPayload(t *testing.T) {
	source := []byte(`{"secret":"value"}`)
	payload, err := NewVersionedPayload(2, source)
	if err != nil {
		t.Fatalf("NewVersionedPayload() error = %v", err)
	}
	source[0] = 'X'
	got := payload.Data()
	got[0] = 'Y'
	if string(payload.Data()) != `{"secret":"value"}` {
		t.Fatal("payload aliases caller-owned bytes")
	}
	if err := payload.ValidateVersion(1, 2); err != nil {
		t.Fatalf("ValidateVersion() error = %v", err)
	}
	retry := RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute}
	if err := (DeliveryRequest{Kind: "telegram.message", Payload: payload, Retry: retry}).Validate(); err != nil {
		t.Fatalf("DeliveryRequest.Validate() error = %v", err)
	}
	job := JobRequest{ID: "job-stable", Type: "encounter.resolve", DueAt: time.Unix(1_800_000_000, 0), Payload: payload, Retry: retry}
	if err := job.Validate(); err != nil {
		t.Fatalf("JobRequest.Validate() error = %v", err)
	}
}

func TestDurableWorkValidationReasons(t *testing.T) {
	payload, err := NewVersionedPayload(1, []byte("secret-payload"))
	if err != nil {
		t.Fatalf("NewVersionedPayload() error = %v", err)
	}
	validRetry := RetryPolicy{MaxAttempts: 2, InitialDelay: time.Second, MaxDelay: time.Minute}
	tests := []struct {
		name   string
		err    func() error
		field  string
		reason ValidationReason
	}{
		{name: "zero payload version", err: func() error { _, err := NewVersionedPayload(0, []byte("secret")); return err }, field: "payload.version", reason: ReasonOutOfRange},
		{name: "empty payload", err: func() error { _, err := NewVersionedPayload(1, nil); return err }, field: "payload.data", reason: ReasonRequired},
		{name: "unsupported version", err: func() error { return payload.ValidateVersion(2, 3) }, field: "payload.version", reason: ReasonUnsupported},
		{name: "no supported versions", err: func() error { return payload.ValidateVersion() }, field: "payload.version", reason: ReasonUnsupported},
		{name: "attempts", err: func() error { return (RetryPolicy{InitialDelay: time.Second, MaxDelay: time.Second}).Validate() }, field: "retry_policy.max_attempts", reason: ReasonOutOfRange},
		{name: "initial delay", err: func() error { return (RetryPolicy{MaxAttempts: 1, MaxDelay: time.Second}).Validate() }, field: "retry_policy.initial_delay", reason: ReasonOutOfRange},
		{name: "maximum delay", err: func() error { return (RetryPolicy{MaxAttempts: 1, InitialDelay: time.Second}).Validate() }, field: "retry_policy.max_delay", reason: ReasonOutOfRange},
		{name: "delay order", err: func() error {
			return (RetryPolicy{MaxAttempts: 1, InitialDelay: time.Minute, MaxDelay: time.Second}).Validate()
		}, field: "retry_policy.max_delay", reason: ReasonInconsistent},
		{name: "delivery kind", err: func() error { return (DeliveryRequest{Payload: payload, Retry: validRetry}).Validate() }, field: "delivery.kind", reason: ReasonRequired},
		{name: "delivery payload", err: func() error { return (DeliveryRequest{Kind: "kind", Retry: validRetry}).Validate() }, field: "payload.version", reason: ReasonOutOfRange},
		{name: "job id", err: func() error {
			return (JobRequest{Type: "type", DueAt: time.Unix(1, 0), Payload: payload, Retry: validRetry}).Validate()
		}, field: "job.id", reason: ReasonRequired},
		{name: "job type", err: func() error {
			return (JobRequest{ID: "id", DueAt: time.Unix(1, 0), Payload: payload, Retry: validRetry}).Validate()
		}, field: "job.type", reason: ReasonRequired},
		{name: "job due time", err: func() error {
			return (JobRequest{ID: "id", Type: "type", Payload: payload, Retry: validRetry}).Validate()
		}, field: "job.due_at", reason: ReasonRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertValidationError(t, tt.err(), tt.field, tt.reason)
		})
	}
}

func assertValidationError(t *testing.T, err error, field string, reason ValidationReason) {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if validation.Field != field || validation.Reason != reason {
		t.Fatalf("ValidationError = %#v, want field %q reason %q", validation, field, reason)
	}
	for _, secret := range []string{"private-secret", "secret-kind", "secret-url", "secret-label", "secret-data", "secret-payload", "secret-value"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error %q exposes content %q", err, secret)
		}
	}
}

func TestDBTXExposesStandardDatabaseCapabilities(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	wantResult := result{lastInsertID: 17, rowsAffected: 3}
	wantRow := &sql.Row{}
	db := &recordingDBTX{execResult: wantResult, queryRow: wantRow}
	var capability DBTX = db

	gotResult, err := capability.ExecContext(ctx, "INSERT", "value")
	if err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	if gotResult != wantResult {
		t.Fatalf("ExecContext() result = %#v, want %#v", gotResult, wantResult)
	}
	if _, err := capability.QueryContext(ctx, "SELECT many", 1); err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if got := capability.QueryRowContext(ctx, "SELECT one", 2); got != wantRow {
		t.Fatalf("QueryRowContext() row = %p, want %p", got, wantRow)
	}

	wantCalls := []recordedCall{
		{ctx: ctx, query: "INSERT", args: []any{"value"}},
		{ctx: ctx, query: "SELECT many", args: []any{1}},
		{ctx: ctx, query: "SELECT one", args: []any{2}},
	}
	if len(db.calls) != len(wantCalls) {
		t.Fatalf("recorded %d calls, want %d", len(db.calls), len(wantCalls))
	}
	for i, want := range wantCalls {
		got := db.calls[i]
		if got.ctx != want.ctx || got.query != want.query || len(got.args) != 1 || got.args[0] != want.args[0] {
			t.Errorf("call %d = %#v, want %#v", i, got, want)
		}
	}
}

func TestWorkScopeExposesTransactionScopedDurableWork(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextKey{}, "work")
	db := &recordingDBTX{}
	scope := &recordingWorkScope{
		db:         db,
		deliveryID: DeliveryID("delivery-result"),
		jobID:      JobID("job-result"),
	}
	var capability WorkScope = scope

	if got := capability.DBTX(); got != db {
		t.Fatalf("DBTX() = %T, want original transaction capability", got)
	}
	deliveryID, err := capability.EnqueueDelivery(ctx, DeliveryRequest{})
	if err != nil {
		t.Fatalf("EnqueueDelivery() error = %v", err)
	}
	if deliveryID != scope.deliveryID || scope.deliveryContext != ctx {
		t.Fatalf("EnqueueDelivery() = (%q, context %v), want (%q, original context)", deliveryID, scope.deliveryContext, scope.deliveryID)
	}
	jobID, err := capability.Schedule(ctx, JobRequest{})
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if jobID != scope.jobID || scope.jobContext != ctx {
		t.Fatalf("Schedule() = (%q, context %v), want (%q, original context)", jobID, scope.jobContext, scope.jobID)
	}
}

type contextKey struct{}

type result struct {
	lastInsertID int64
	rowsAffected int64
}

func (r result) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r result) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type recordedCall struct {
	ctx   context.Context
	query string
	args  []any
}

type recordingDBTX struct {
	execResult sql.Result
	queryRow   *sql.Row
	calls      []recordedCall
}

func (db *recordingDBTX) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	db.record(ctx, query, args)
	return db.execResult, nil
}

func (db *recordingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	db.record(ctx, query, args)
	return nil, nil
}

func (db *recordingDBTX) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	db.record(ctx, query, args)
	return db.queryRow
}

func (db *recordingDBTX) record(ctx context.Context, query string, args []any) {
	db.calls = append(db.calls, recordedCall{ctx: ctx, query: query, args: args})
}

type recordingWorkScope struct {
	db              DBTX
	deliveryID      DeliveryID
	jobID           JobID
	deliveryContext context.Context
	jobContext      context.Context
}

func (s *recordingWorkScope) DBTX() DBTX { return s.db }

func (s *recordingWorkScope) EnqueueDelivery(ctx context.Context, _ DeliveryRequest) (DeliveryID, error) {
	s.deliveryContext = ctx
	return s.deliveryID, nil
}

func (s *recordingWorkScope) Schedule(ctx context.Context, _ JobRequest) (JobID, error) {
	s.jobContext = ctx
	return s.jobID, nil
}
