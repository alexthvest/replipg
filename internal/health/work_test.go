package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
)

func TestDurableCountsAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatal(err)
	}

	source := NewWorkStatus(db)
	empty, err := source.Snapshot(ctx)
	if err != nil || empty != (WorkCounts{}) {
		t.Fatalf("empty=%+v err=%v", empty, err)
	}

	for i, state := range []string{"pending", "attempting", "retrying", "reconciliation_required", "completed", "permanent_failure"} {
		var next, terminal, reason any

		if state == "pending" || state == "retrying" {
			next = int64(100)
		}
		if state == "completed" || state == "permanent_failure" {
			terminal = int64(100)
		}
		if state == "reconciliation_required" || state == "permanent_failure" {
			reason = "private-reason"
		}
		for n := 0; n <= i; n++ {
			_, err := db.ExecContext(ctx, `INSERT INTO outbound_deliveries(id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,reason_code,created_at,updated_at,terminal_at) VALUES(?, 'private-kind',1,?,-123,0,?,3,1,10,0,?,?,100,100,?)`, fmt.Sprintf("private-delivery-%d-%d", i, n), []byte("private-content"), state, next, reason, terminal)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	for i, state := range []string{"pending", "claimed", "retrying", "completed", "cancelled", "permanent_failure"} {
		var next, terminal, reason, owner, correlation, lease any

		if state == "retrying" {
			next = int64(100)
		}
		if state == "completed" || state == "cancelled" || state == "permanent_failure" {
			terminal = int64(100)
		}
		if state == "permanent_failure" {
			reason = "private-reason"
		}
		if state == "claimed" {
			owner = "private-owner"
			correlation = "private-correlation"
			lease = int64(100)
		}
		for n := 0; n <= i; n++ {
			_, err := db.ExecContext(ctx, `INSERT INTO scheduled_jobs(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,claim_owner,claim_correlation_id,lease_until,reason_code,created_at,updated_at,terminal_at) VALUES(?,'private-type',1,?,100,?,3,1,10,0,?,?,?,?,?,100,100,?)`, fmt.Sprintf("private-job-%d-%d", i, n), []byte("private-payload"), state, next, owner, correlation, lease, reason, terminal)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	got, err := source.Snapshot(ctx)
	want := WorkCounts{Deliveries: DeliveryCounts{Pending: 1, Attempting: 2, Retrying: 3, ReconciliationRequired: 4, Completed: 5, PermanentFailure: 6}, Jobs: JobCounts{Pending: 1, Claimed: 2, Retrying: 3, Completed: 4, Cancelled: 5, PermanentFailure: 6}}
	if err != nil || got != want {
		t.Fatalf("counts=%+v want=%+v err=%v", got, want, err)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "private") || strings.Contains(string(body), "123") {
		t.Fatalf("unsafe output: %s", body)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	got, err = source.Snapshot(ctx)
	if !errors.Is(err, ErrWorkStatusUnavailable) || got != (WorkCounts{}) {
		t.Fatalf("failure=%+v %v", got, err)
	}
}
