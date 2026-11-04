package settings

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"time"
)

var ErrInvalid = errors.New("invalid settings input")
var ErrStale = errors.New("settings control is stale")

type storageError struct{ operation string }

func (e storageError) Error() string { return "settings storage: " + e.operation }
func (storageError) StorageFailure() {}
func storage(op string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return storageError{op}
}

type Store struct{ menu *MenuBuilder }

func NewStore(menu *MenuBuilder) *Store { return &Store{menu: menu} }

type Snapshot struct {
	ChatID          runtimecontract.ChatID
	Locale          localization.Locale
	Revision        int64
	DesiredHash     string
	AppliedHash     sql.NullString
	AppliedRevision sql.NullInt64
	NextAttempt     time.Time
	Attempts        int64
	Failure         sql.NullString
	UpdatedAt       time.Time
}

func (s Snapshot) Pending() bool {
	return !s.AppliedHash.Valid || !s.AppliedRevision.Valid || s.AppliedHash.String != s.DesiredHash || s.AppliedRevision.Int64 != s.Revision
}

const columns = `chat_id,locale,revision,desired_menu_hash,applied_menu_hash,applied_revision,menu_next_attempt_at,menu_attempts,menu_failure_code,updated_at`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Snapshot, error) {
	var v Snapshot
	var l string
	var next, updated int64
	if err := row.Scan(&v.ChatID, &l, &v.Revision, &v.DesiredHash, &v.AppliedHash, &v.AppliedRevision, &next, &v.Attempts, &v.Failure, &updated); err != nil {
		return v, err
	}
	locale, err := localization.ParseLocale(l)
	if err != nil {
		return v, ErrInvalid
	}
	v.Locale = locale
	v.NextAttempt = time.UnixMilli(next)
	v.UpdatedAt = time.UnixMilli(updated)
	if v.Failure.Valid {
		switch v.Failure.String {
		case "transient", "rate_limited", "forbidden", "invalid", "indeterminate":
		default:
			return v, ErrInvalid
		}
	}
	validHash := func(h string) bool {
		b, e := hex.DecodeString(h)
		return e == nil && len(b) == 32 && fmt.Sprintf("%x", b) == h
	}
	if v.ChatID >= 0 || v.Revision < 1 || v.Attempts < 0 || !validHash(v.DesiredHash) || v.AppliedHash.Valid != v.AppliedRevision.Valid || (v.AppliedHash.Valid && (!validHash(v.AppliedHash.String) || v.AppliedRevision.Int64 < 1)) {
		return v, ErrInvalid
	}
	return v, nil
}
func (s *Store) Inspect(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, now time.Time) (Snapshot, error) {
	if s == nil || s.menu == nil || tx == nil || chat >= 0 {
		return Snapshot{}, ErrInvalid
	}
	v, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM group_settings WHERE chat_id=?`, chat))
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v, storage("read", err)
	}
	hash, err := s.menu.Fingerprint(localization.English)
	if err != nil {
		return v, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_settings VALUES(?,'en',1,?,NULL,NULL,?,0,NULL,?)`, chat, hash, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return v, storage("create", err)
	}
	return Snapshot{ChatID: chat, Locale: localization.English, Revision: 1, DesiredHash: hash, NextAttempt: now, UpdatedAt: now}, nil
}
func (s *Store) Resolve(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID) (localization.Locale, error) {
	v, err := s.Inspect(ctx, tx, chat, time.Now())
	return v.Locale, err
}

// view reads callback presentation without managing a group for a rejected control.
func (s *Store) view(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, now time.Time) (Snapshot, error) {
	if s == nil || s.menu == nil || tx == nil || chat >= 0 {
		return Snapshot{}, ErrInvalid
	}
	v, err := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM group_settings WHERE chat_id=?`, chat))
	if err == nil {
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v, storage("read", err)
	}
	hash, err := s.menu.Fingerprint(localization.English)
	return Snapshot{ChatID: chat, Locale: localization.English, Revision: 1, DesiredHash: hash, NextAttempt: now, UpdatedAt: now}, err
}
func (s *Store) Change(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, revision int64, locale localization.Locale, now time.Time) (Snapshot, error) {
	return s.change(ctx, tx, chat, revision, locale, now, nil)
}
func (s *Store) change(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, revision int64, locale localization.Locale, now time.Time, authorize func() (time.Time, bool)) (Snapshot, error) {
	v, err := s.Inspect(ctx, tx, chat, now)
	if err != nil {
		return v, err
	}
	if authorize != nil {
		var allowed bool
		now, allowed = authorize()
		if !allowed {
			return v, ErrEvidenceExpired
		}
	}
	if v.Revision != revision {
		return v, ErrStale
	}
	hash, err := s.menu.Fingerprint(locale)
	if err != nil {
		return v, err
	}
	if v.Locale == locale {
		return v, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE group_settings SET locale=?,revision=revision+1,desired_menu_hash=?,menu_next_attempt_at=?,menu_attempts=0,menu_failure_code=NULL,updated_at=? WHERE chat_id=? AND revision=?`, string(locale), hash, now.UnixMilli(), now.UnixMilli(), chat, revision)
	if err != nil {
		return v, storage("change", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return v, storage("change result", err)
	}
	if n != 1 {
		return v, ErrStale
	}
	return s.Inspect(ctx, tx, chat, now)
}
func (s *Store) Prepare(ctx context.Context, tx runtimecontract.DBTX, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+columns+` FROM group_settings`)
	if err != nil {
		return storage("prepare read", err)
	}
	var values []Snapshot
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			_ = rows.Close()
			return storage("prepare validate", err)
		}
		values = append(values, v)
	}
	iteration := rows.Err()
	closed := rows.Close()
	if iteration != nil {
		return storage("prepare iterate", iteration)
	}
	if closed != nil {
		return storage("prepare close", closed)
	}
	for _, v := range values {
		hash, err := s.menu.Fingerprint(v.Locale)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE group_settings SET desired_menu_hash=?,applied_menu_hash=NULL,applied_revision=NULL,menu_attempts=0,menu_failure_code=NULL,menu_next_attempt_at=? WHERE chat_id=?`, hash, now.UnixMilli(), v.ChatID); err != nil {
			return storage("prepare write", err)
		}
	}
	return purge(ctx, tx, now)
}
func purge(ctx context.Context, tx runtimecontract.DBTX, now time.Time) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM settings_controls WHERE token IN (SELECT token FROM settings_controls WHERE expires_at<=? LIMIT 1000)`, now.UnixMilli())
	if err != nil {
		return storage("purge controls", err)
	}
	return nil
}
