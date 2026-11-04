package settings

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func fixture(t *testing.T) (*persistence.DB, *Store, *localization.Catalog, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, m); err != nil {
		t.Fatal(err)
	}
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewMenuBuilder(c, []CommandSpec{{"settings", "command.settings", true}})
	if err != nil {
		t.Fatal(err)
	}
	return db, NewStore(b), c, time.Unix(1000, 0)
}
func TestControlsAndGroupIsolation(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	var token string
	err := db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		v, err := s.Inspect(ctx, tx, -1, now)
		if err != nil {
			return err
		}
		token, err = s.CreateControl(ctx, tx, v, 1, localization.Russian, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		chat  runtimecontract.ChatID
		actor runtimecontract.UserID
		at    time.Time
	}{{-2, 1, now}, {-1, 2, now}, {-1, 1, now.Add(5 * time.Minute)}} {
		err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
			_, err := s.ApplyControl(ctx, tx, bad.chat, bad.actor, token, bad.at)
			return err
		})
		if !errors.Is(err, ErrStale) {
			t.Fatalf("bad control accepted: %v", err)
		}
	}
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		v, err := s.ApplyControl(ctx, tx, -1, 1, token, now)
		if err != nil {
			return err
		}
		if v.Locale != localization.Russian || v.Revision != 2 || !v.Pending() {
			t.Fatalf("change: %+v", v)
		}
		v, err = s.Change(ctx, tx, -1, 2, localization.Russian, now)
		if err != nil {
			return err
		}
		if v.Revision != 2 {
			t.Fatal("same selection bumped revision")
		}
		other, err := s.Inspect(ctx, tx, -2, now)
		if err == nil && other.Locale != localization.English {
			t.Fatal("group leaked")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := s.ApplyControl(ctx, tx, -1, 1, token, now)
		return err
	})
	if !errors.Is(err, ErrStale) {
		t.Fatal("consumed accepted")
	}
}

type trackingTx struct {
	db     *persistence.DB
	active bool
}

func (t *trackingTx) WithinTransaction(ctx context.Context, f func(context.Context, runtimecontract.DBTX) error) error {
	return t.db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		t.active = true
		defer func() { t.active = false }()
		return f(ctx, tx)
	})
}

type writerFunc func(context.Context, runtimecontract.ChatID, []runtimecontract.CommandDeclaration) error

func (f writerFunc) ReplaceGroupMenu(ctx context.Context, c runtimecontract.ChatID, d []runtimecontract.CommandDeclaration) error {
	return f(ctx, c, d)
}
func TestWorkerFencesInflightSuccessAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
			db, s, _, now := fixture(t)
			ctx := context.Background()
			if _, err := s.Inspect(ctx, db, -1, now); err != nil {
				t.Fatal(err)
			}
			tx := &trackingTx{db: db}
			calls := 0
			writer := writerFunc(func(ctx context.Context, chat runtimecontract.ChatID, commands []runtimecontract.CommandDeclaration) error {
				if tx.active {
					t.Fatal("network inside transaction")
				}
				calls++
				if calls == 1 {
					err := db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
						_, err := s.Change(ctx, tx, -1, 1, localization.Russian, now)
						return err
					})
					if err != nil {
						t.Fatal(err)
					}
					if failed {
						return &runtimecontract.MenuError{Kind: runtimecontract.RateLimited, RetryAfter: time.Hour}
					}
				}
				return nil
			})
			w, err := NewMenuWorker(tx, s, writer, WorkerOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			v, err := s.Inspect(ctx, db, -1, now)
			if err != nil {
				t.Fatal(err)
			}
			if !v.Pending() || v.Attempts != 0 || v.NextAttempt.After(now) {
				t.Fatalf("obsolete result affected target: %+v", v)
			}
			if _, err = w.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			v, err = s.Inspect(ctx, db, -1, now)
			if err != nil || v.Pending() {
				t.Fatalf("not synchronized: %+v %v", v, err)
			}
			now = now.Add(15 * time.Minute)
			if worked, err := w.RunOnce(ctx); err != nil || !worked {
				t.Fatal("refresh missing", err)
			}
		})
	}
}

type memberFunc func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error)

func (f memberFunc) VerifyGroupMember(ctx context.Context, c runtimecontract.ChatID, u runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
	return f(ctx, c, u)
}

type scope struct{ tx runtimecontract.DBTX }

func (s scope) DBTX() runtimecontract.DBTX { return s.tx }
func (scope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	return "", ErrInvalid
}
func (scope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	return "", ErrInvalid
}
func TestPreparedPermissionAndLocalizedSettings(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	calls := 0
	status := runtimecontract.Administrator
	h := handler{s, c, memberFunc(func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
		calls++
		return runtimecontract.MembershipResult{Status: status, CheckedAt: now}, nil
	}), func() time.Time { return now }}
	command := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, Name: "settings", SentAt: now, EphemeralMessageID: 1}
	ordinary := command
	ordinary.EphemeralMessageID = 0
	p, err := h.Prepare(ctx, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Handle(ctx, scope{db}, ordinary); err != nil || calls != 0 {
		t.Fatal("ordinary command permission lookup")
	}
	p, err = h.Prepare(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	var result interaction.HandlerResult
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		result, err = p.Handle(ctx, scope{tx}, command)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if len(response.Content.Buttons) != 2 {
		t.Fatal("admin controls missing")
	}
	token := response.Content.Buttons[1][0].CallbackData
	callback := interaction.Callback{Identity: interaction.Identity{Update: 2, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "q", EphemeralMessageID: 1, Data: token}
	p, err = h.Prepare(ctx, callback)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		result, err = p.Handle(ctx, scope{tx}, callback)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	response = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if !strings.Contains(response.Content.Text, "Русский") || response.EditMessageID != 1 {
		t.Fatal("selection not translated/replaced")
	}
	status = runtimecontract.Member
	command.Update = 3
	p, err = h.Prepare(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	result, err = p.Handle(ctx, scope{db}, command)
	if err != nil {
		t.Fatal(err)
	}
	response = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if len(response.Content.Buttons) != 0 {
		t.Fatal("member controls visible")
	}
}

func TestWorkerRetriesAndStartupRefresh(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	if _, err := s.Inspect(ctx, db, -1, now); err != nil {
		t.Fatal(err)
	}
	fail := true
	w, err := NewMenuWorker(db, s, writerFunc(func(context.Context, runtimecontract.ChatID, []runtimecontract.CommandDeclaration) error {
		if fail {
			return &runtimecontract.MenuError{Kind: runtimecontract.RateLimited, RetryAfter: 10 * time.Second}
		}
		return nil
	}), WorkerOptions{Now: func() time.Time { return now }, InitialDelay: time.Second, MaximumDelay: 4 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := int64(1); attempt <= 4; attempt++ {
		if _, err = w.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		v, err := s.Inspect(ctx, db, -1, now)
		if err != nil {
			t.Fatal(err)
		}
		if v.Attempts != attempt || !v.Pending() || !v.NextAttempt.Equal(now.Add(10*time.Second)) {
			t.Fatalf("retry not persisted %+v", v)
		}
		if worked, err := w.RunOnce(ctx); err != nil || worked {
			t.Fatal("retried before due", err)
		}
		now = now.Add(10 * time.Second)
	}
	fail = false
	if _, err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil || v.Pending() {
		t.Fatal("not acknowledged", err)
	}
	if err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error { return s.Prepare(ctx, tx, now) }); err != nil {
		t.Fatal(err)
	}
	v, err = s.Inspect(ctx, db, -1, now)
	if err != nil || !v.Pending() || v.NextAttempt.After(now) {
		t.Fatal("startup did not refresh", err)
	}
}
func TestCorruptSelectionFailsWithoutOverwrite(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	if _, err := s.Inspect(ctx, db, -1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA ignore_check_constraints=ON; UPDATE group_settings SET locale='fr' WHERE chat_id=-1`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Inspect(ctx, db, -1, now)
	var storage runtimecontract.StorageFailure
	if !errors.As(err, &storage) {
		t.Fatal("corruption not storage error", err)
	}
	var locale string
	if err = db.QueryRowContext(ctx, `SELECT locale FROM group_settings WHERE chat_id=-1`).Scan(&locale); err != nil || locale != "fr" {
		t.Fatal("corruption overwritten", err)
	}
}

func TestPermissionEvidenceExpiresAndBindsIdentity(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	h := handler{s, c, memberFunc(func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
		return runtimecontract.MembershipResult{Status: runtimecontract.Administrator, CheckedAt: now}, nil
	}), func() time.Time { return now }}
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateControl(ctx, db, v, 1, localization.Russian, now)
	if err != nil {
		t.Fatal(err)
	}
	callback := interaction.Callback{Identity: interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "q", EphemeralMessageID: 1, Data: token}
	p, err := h.Prepare(ctx, callback)
	if err != nil {
		t.Fatal(err)
	}
	forged := callback
	forged.Update = 2
	result, err := p.Handle(ctx, scope{db}, forged)
	if err != nil || len(result.ImmediateResponses) != 0 {
		t.Fatal("evidence reused", err)
	}
	now = now.Add(5 * time.Second)
	result, err = p.Handle(ctx, scope{db}, callback)
	if err != nil {
		t.Fatal(err)
	}
	response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if !strings.Contains(response.Content.Text, "Open /settings again") || len(response.Content.Buttons) != 0 {
		t.Fatal("expired evidence not rejected")
	}
	v, err = s.Inspect(ctx, db, -1, now)
	if err != nil || v.Locale != localization.English {
		t.Fatal("expired evidence changed language")
	}
}

type delayedRead struct {
	runtimecontract.DBTX
	after func()
}

func (d delayedRead) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	row := d.DBTX.QueryRowContext(ctx, q, args...)
	d.after()
	return row
}
func TestEvidenceExpiresDuringStorageRead(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateControl(ctx, db, v, 1, localization.Russian, now)
	if err != nil {
		t.Fatal(err)
	}
	h := handler{s, c, memberFunc(func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
		return runtimecontract.MembershipResult{Status: runtimecontract.Administrator, CheckedAt: now}, nil
	}), func() time.Time { return now }}
	e := interaction.Callback{Identity: interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "q", EphemeralMessageID: 1, Data: token}
	p, err := h.Prepare(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	delayed := delayedRead{db, func() {
		reads++
		if reads == 2 {
			now = now.Add(5 * time.Second)
		}
	}}
	result, err := p.Handle(ctx, scope{delayed}, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons) != 0 {
		t.Fatal("expired evidence issued controls")
	}
	v, err = s.Inspect(ctx, db, -1, now)
	if err != nil || v.Locale != localization.English {
		t.Fatal("SQL wait reused expired permission", err)
	}
}

func TestSettingsSaveAndCommitFailuresRollback(t *testing.T) {
	for _, mode := range []string{"save", "commit"} {
		t.Run(mode, func(t *testing.T) {
			db, s, _, now := fixture(t)
			ctx := context.Background()
			v, err := s.Inspect(ctx, db, -1, now)
			if err != nil {
				t.Fatal(err)
			}
			token, err := s.CreateControl(ctx, db, v, 1, localization.Russian, now)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "save" {
				if _, err = db.ExecContext(ctx, `CREATE TRIGGER settings_fail BEFORE UPDATE OF locale ON group_settings BEGIN SELECT RAISE(ABORT,'private'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if _, err := s.ApplyControl(ctx, tx, -1, 1, token, now); err != nil {
					return err
				}
				if mode == "commit" {
					if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, `INSERT INTO settings_controls VALUES('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',-2,1,1,'en',10000000,0)`)
					return err
				}
				return nil
			})
			if err == nil {
				t.Fatal("failure injection succeeded")
			}
			v, err = s.Inspect(ctx, db, -1, now)
			if err != nil || v.Locale != localization.English || v.Revision != 1 {
				t.Fatal("failed transaction changed locale", err)
			}
			var consumed int
			if err = db.QueryRowContext(ctx, `SELECT consumed FROM settings_controls WHERE token=?`, strings.TrimPrefix(token, "set:")).Scan(&consumed); err != nil || consumed != 0 {
				t.Fatal("failed transaction consumed token", err)
			}
		})
	}
}
func TestSameLanguageControlConsumedWithoutRevisionChange(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateControl(ctx, db, v, 1, localization.English, now)
	if err != nil {
		t.Fatal(err)
	}
	err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		updated, err := s.ApplyControl(ctx, tx, -1, 1, token, now)
		if err == nil && (updated.Revision != 1 || updated.Locale != localization.English) {
			t.Fatal("same language changed revision")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyControl(ctx, db, -1, 1, token, now); !errors.Is(err, ErrStale) {
		t.Fatal("same language token reusable", err)
	}
}

func TestControlExpiresDuringStorageReadWithFreshPermission(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateControl(ctx, db, v, 1, localization.Russian, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE settings_controls SET expires_at=?`, now.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	h := handler{s, c, memberFunc(func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
		return runtimecontract.MembershipResult{Status: runtimecontract.Administrator, CheckedAt: now}, nil
	}), func() time.Time { return now }}
	e := interaction.Callback{Identity: interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "q", EphemeralMessageID: 1, Data: token}
	p, err := h.Prepare(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	delayed := delayedRead{db, func() {
		reads++
		if reads == 2 {
			now = now.Add(2 * time.Second)
		}
	}}
	_, err = p.Handle(ctx, scope{delayed}, e)
	if err != nil {
		t.Fatal(err)
	}
	v, err = s.Inspect(ctx, db, -1, now)
	if err != nil || v.Locale != localization.English {
		t.Fatal("SQL wait reused expired control", err)
	}
}

func TestStartupRebuildsChangedEnabledCommands(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	old := v.DesiredHash
	w, err := NewMenuWorker(db, s, writerFunc(func(context.Context, runtimecontract.ChatID, []runtimecontract.CommandDeclaration) error { return nil }), WorkerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	builder, err := NewMenuBuilder(c, []CommandSpec{{"settings", "command.settings", true}, {"dev_inventory_heal", "command.dev_inventory_heal", true}})
	if err != nil {
		t.Fatal(err)
	}
	updated := NewStore(builder)
	if err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error { return updated.Prepare(ctx, tx, now) }); err != nil {
		t.Fatal(err)
	}
	v, err = updated.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.DesiredHash == old || !v.Pending() || v.Revision != 1 || v.NextAttempt.After(now) {
		t.Fatal("enabled command change not due without locale change")
	}
	calls := 0
	w, err = NewMenuWorker(db, updated, writerFunc(func(_ context.Context, _ runtimecontract.ChatID, commands []runtimecontract.CommandDeclaration) error {
		calls++
		if len(commands) != 2 || commands[1].Name != "dev_inventory_heal" || !commands[1].Ephemeral {
			t.Fatal("changed enabled command set not published")
		}
		return nil
	}), WorkerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart publication missing")
	}
	if err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error { return s.Prepare(ctx, tx, now) }); err != nil {
		t.Fatal(err)
	}
	v, err = s.Inspect(ctx, db, -1, now)
	if err != nil || v.DesiredHash != old || !v.Pending() {
		t.Fatal("disabled command not removed from target", err)
	}
}

func TestRejectedSettingsCallbacksNeverManageGroups(t *testing.T) {
	db, s, c, now := fixture(t)
	ctx := context.Background()
	v, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.CreateControl(ctx, db, v, 1, localization.Russian, now)
	if err != nil {
		t.Fatal(err)
	}
	h := handler{s, c, memberFunc(func(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
		return runtimecontract.MembershipResult{Status: runtimecontract.Administrator, CheckedAt: now}, nil
	}), func() time.Time { return now }}
	for _, data := range []string{"set:malformed", token} {
		e := interaction.Callback{Identity: interaction.Identity{Update: 1, Chat: -2, User: 1, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "q", EphemeralMessageID: 1, Data: data}
		p, err := h.Prepare(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
			r, err := p.Handle(ctx, scope{tx}, e)
			if err != nil {
				return err
			}
			if len(r.ImmediateResponses) != 1 || len(r.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons) != 0 {
				t.Fatal("rejection did not show readonly reopen guidance")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM group_settings`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected callback managed group", count, err)
	}
}

type failedAckTransactor struct {
	db    *persistence.DB
	calls int
}

func (f *failedAckTransactor) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	f.calls++
	if f.calls == 2 {
		return errors.New("injected acknowledgement storage failure")
	}
	return f.db.WithinTransaction(ctx, fn)
}
func TestSuccessfulRemotePublicationSurvivesFailedAcknowledgementAndRestart(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	initial, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	published := 0
	writer := writerFunc(func(context.Context, runtimecontract.ChatID, []runtimecontract.CommandDeclaration) error {
		published++
		return nil
	})
	w, err := NewMenuWorker(&failedAckTransactor{db: db}, s, writer, WorkerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	worked, err := w.RunOnce(ctx)
	if err == nil || !worked || published != 1 {
		t.Fatal("acknowledgement failure was hidden", worked, err, published)
	}
	after, err := s.Inspect(ctx, db, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Pending() || after.DesiredHash != initial.DesiredHash || after.Revision != initial.Revision || after.Attempts != 0 || !after.NextAttempt.Equal(initial.NextAttempt) {
		t.Fatal("failed acknowledgement discarded desired state")
	}
	restarted := NewStore(s.menu)
	if err = db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error { return restarted.Prepare(ctx, tx, now) }); err != nil {
		t.Fatal(err)
	}
	w, err = NewMenuWorker(db, restarted, writer, WorkerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if worked, err = w.RunOnce(ctx); err != nil || !worked {
		t.Fatal("restart publication failed", worked, err)
	}
	after, err = restarted.Inspect(ctx, db, -1, now)
	if err != nil || after.Pending() || published != 2 {
		t.Fatal("restart did not republish and acknowledge", published, err)
	}
}
func TestTransientMenuFailuresUseCappedExponentialBackoff(t *testing.T) {
	db, s, _, now := fixture(t)
	ctx := context.Background()
	if _, err := s.Inspect(ctx, db, -1, now); err != nil {
		t.Fatal(err)
	}
	w, err := NewMenuWorker(db, s, writerFunc(func(context.Context, runtimecontract.ChatID, []runtimecontract.CommandDeclaration) error {
		return errors.New("unclassified private provider failure")
	}), WorkerOptions{Now: func() time.Time { return now }, InitialDelay: time.Second, MaximumDelay: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for index, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second} {
		worked, err := w.RunOnce(ctx)
		if err != nil || !worked {
			t.Fatal(worked, err)
		}
		v, err := s.Inspect(ctx, db, -1, now)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Pending() || v.Attempts != int64(index+1) || !v.NextAttempt.Equal(now.Add(delay)) || !v.Failure.Valid || v.Failure.String != "transient" {
			t.Fatalf("retry %d delay %v: %+v", index, delay, v)
		}
		if worked, err = w.RunOnce(ctx); err != nil || worked {
			t.Fatal("retried before durable delay", worked, err)
		}
		now = now.Add(delay)
	}
}
