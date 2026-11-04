package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type inertHandler struct{}

func (inertHandler) Execute(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
	return JobOutcome{}
}

func openStore(t *testing.T, dir string) (*persistence.DB, *Service) {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}
	s := New(db, testsupport.NewManualClock(time.Unix(1800000000, 0)))
	if err := s.Register("feature.work", 1, inertHandler{}); err != nil {
		t.Fatal(err)
	}
	s.SealRegistrations()
	return db, s
}

func request(t *testing.T) runtimecontract.JobRequest {
	t.Helper()
	payload, err := runtimecontract.NewVersionedPayload(1, []byte("opaque-private-feature-result"))
	if err != nil {
		t.Fatal(err)
	}
	return runtimecontract.JobRequest{ID: "stable-job", Type: "feature.work", DueAt: time.Unix(1800000100, 123000000), Payload: payload, Retry: runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute}}
}

func schedule(ctx context.Context, db *persistence.DB, s *Service, req runtimecontract.JobRequest) error {
	return db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		id, err := s.Schedule(ctx, tx, req)
		if err == nil && id != req.ID {
			return errors.New("unexpected job identity")
		}
		return err
	})
}

func TestScheduleSurvivesReopenWithOpaqueRequest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, s := openStore(t, dir)
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, s = openStore(t, dir)
	status, err := s.Status(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.ID != req.ID || status.State != StatePending || !status.DueAt.Equal(req.DueAt) || status.AttemptCount != 0 {
		t.Fatalf("status = %+v", status)
	}
	if strings.Contains(fmt.Sprintf("%+v", status), "opaque-private") {
		t.Fatal("status exposes payload")
	}
	for _, field := range []string{"Payload", "Type", "ClaimOwner", "ClaimCorrelationID"} {
		if _, ok := reflect.TypeOf(status).FieldByName(field); ok {
			t.Fatalf("status exposes %s", field)
		}
	}
	var version, maxAttempts, initialMS, maxMS int
	var payload []byte
	if err := db.QueryRowContext(ctx, `SELECT payload_version,payload,max_attempts,initial_delay_ms,max_delay_ms FROM scheduled_jobs WHERE id=?`, req.ID).Scan(&version, &payload, &maxAttempts, &initialMS, &maxMS); err != nil {
		t.Fatal(err)
	}
	if version != 1 || string(payload) != string(req.Payload.Data()) || maxAttempts != 3 || initialMS != 1000 || maxMS != 60000 {
		t.Fatal("persisted request changed")
	}
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatalf("identical schedule: %v", err)
	}
}

func TestRollbackAndConflictingRequests(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	rollback := errors.New("rollback")
	err := db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := s.Schedule(ctx, tx, req); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback = %v", err)
	}
	if _, err := s.Status(ctx, req.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back status = %v", err)
	}
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*runtimecontract.JobRequest){
		func(r *runtimecontract.JobRequest) { r.DueAt = r.DueAt.Add(time.Second) },
		func(r *runtimecontract.JobRequest) { r.Retry.MaxAttempts++ },
		func(r *runtimecontract.JobRequest) {
			r.Payload, _ = runtimecontract.NewVersionedPayload(1, []byte("different"))
		},
	} {
		changed := req
		mutate(&changed)
		if err := schedule(ctx, db, s, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("conflict = %v", err)
		}
	}
}

func TestRegistryAndVersionValidation(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	req.Payload, _ = runtimecontract.NewVersionedPayload(2, []byte("unsupported"))
	if err := schedule(ctx, db, s, req); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("version error = %v", err)
	}
	req = request(t)
	req.Type = "unregistered"
	if err := schedule(ctx, db, s, req); !errors.Is(err, ErrUnregisteredType) {
		t.Fatalf("type error = %v", err)
	}
	if err := s.Register("later", 1, inertHandler{}); !errors.Is(err, ErrRegistrationsClosed) {
		t.Fatalf("late registration = %v", err)
	}
	s = New(db, testsupport.NewManualClock(time.Unix(1800000000, 0)))
	if err := s.Register("feature.work", 1, inertHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("feature.work", 1, inertHandler{}); !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := s.Register("feature.work", 2, inertHandler{}); err != nil {
		t.Fatalf("distinct version = %v", err)
	}
	if err := s.Register("", 1, inertHandler{}); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("invalid registration = %v", err)
	}
	if err := schedule(ctx, db, s, request(t)); !errors.Is(err, ErrRegistrationsOpen) {
		t.Fatalf("unsealed admission = %v", err)
	}
	if _, err := s.Status(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestScheduleRoundsUpToStoragePrecision(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	req.DueAt = req.DueAt.Add(time.Nanosecond)
	req.Retry.InitialDelay = time.Nanosecond
	req.Retry.MaxDelay = time.Microsecond
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.DueAt.Before(req.DueAt) || status.DueAt.Sub(req.DueAt) >= time.Millisecond {
		t.Fatalf("due time rounded unsafely: %v", status.DueAt)
	}
	var initial, maxDelay int64
	if err := db.QueryRowContext(ctx, `SELECT initial_delay_ms,max_delay_ms FROM scheduled_jobs WHERE id=?`, req.ID).Scan(&initial, &maxDelay); err != nil {
		t.Fatal(err)
	}
	if initial != 1 || maxDelay != 1 {
		t.Fatalf("retry precision = %d %d", initial, maxDelay)
	}
}

func TestInvalidRequestNeverPersists(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	req.Retry.MaxAttempts = 0
	err := schedule(ctx, db, s, req)
	var validation *runtimecontract.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("invalid request = %v", err)
	}
	if _, err := s.Status(ctx, req.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid request persisted: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Schedule(ctx, db, request(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request = %v", err)
	}
}

func TestSafeBoundaries(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	var tx *sql.Tx
	if _, err := s.Schedule(ctx, tx, request(t)); !errors.Is(err, ErrTransactionRequired) {
		t.Fatalf("nil tx = %v", err)
	}
	var handler *inertHandler
	registry := New(db, testsupport.NewManualClock(time.Unix(1800000000, 0)))
	if err := registry.Register("type", 1, handler); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("nil handler = %v", err)
	}
	if _, err := s.Status(ctx, ""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("invalid ID = %v", err)
	}
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET reason_code=? WHERE id=?`, "opaque-private-feature-result", req.ID); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.ReasonCode != "unspecified_failure" {
		t.Fatalf("unsafe reason exposed: %v", status)
	}
	privateErr := errors.New("private SQL and payload")
	safe := storeError("schedule", privateErr)
	if errors.Unwrap(safe) != nil || strings.Contains(fmt.Sprintf("%+v", safe), "private") {
		t.Fatalf("unsafe error chain: %v", safe)
	}
	if err := storeError("schedule", context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestConcurrentRegistrationHasOneOwner(t *testing.T) {
	db, _ := openStore(t, t.TempDir())
	s := New(db, testsupport.NewManualClock(time.Unix(1800000000, 0)))
	results := make(chan error, 16)
	for range 16 {
		go func() { results <- s.Register("feature.work", 1, inertHandler{}) }()
	}
	winners := 0
	for range 16 {
		err := <-results
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrRegistrationConflict) {
			t.Fatalf("registration = %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("owners = %d", winners)
	}
}
