package settings

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"strings"
	"time"
)

func (s *Store) CreateControl(ctx context.Context, tx runtimecontract.DBTX, v Snapshot, actor runtimecontract.UserID, selection localization.Locale, now time.Time) (string, error) {
	if actor <= 0 {
		return "", ErrInvalid
	}
	if _, err := s.menu.Fingerprint(selection); err != nil {
		return "", err
	}
	if err := purge(ctx, tx, now); err != nil {
		return "", err
	}
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", errors.New("settings token generation failed")
	}
	token := hex.EncodeToString(data[:])
	_, err := tx.ExecContext(ctx, `INSERT INTO settings_controls VALUES(?,?,?,?,?,?,0)`, token, v.ChatID, actor, v.Revision, string(selection), now.Add(5*time.Minute).UnixMilli())
	if err != nil {
		return "", storage("create control", err)
	}
	return "set:" + token, nil
}
func (s *Store) ApplyControl(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, actor runtimecontract.UserID, data string, now time.Time) (Snapshot, error) {
	return s.applyControl(ctx, tx, chat, actor, data, now, nil)
}

var ErrEvidenceExpired = errors.New("settings permission evidence expired")

func (s *Store) applyControl(ctx context.Context, tx runtimecontract.DBTX, chat runtimecontract.ChatID, actor runtimecontract.UserID, data string, now time.Time, authorize func() (time.Time, bool)) (Snapshot, error) {
	token := strings.TrimPrefix(data, "set:")
	if len(data) != 36 || !strings.HasPrefix(data, "set:") {
		return Snapshot{}, ErrStale
	}
	if b, err := hex.DecodeString(token); err != nil || len(b) != 16 {
		return Snapshot{}, ErrStale
	}
	var boundChat runtimecontract.ChatID
	var boundActor runtimecontract.UserID
	var rev, expires int64
	var locale string
	var consumed int
	err := tx.QueryRowContext(ctx, `SELECT chat_id,actor_id,expected_revision,selection,expires_at,consumed FROM settings_controls WHERE token=?`, token).Scan(&boundChat, &boundActor, &rev, &locale, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrStale
	}
	if err != nil {
		return Snapshot{}, storage("read control", err)
	}
	if boundChat != chat || boundActor != actor || consumed != 0 || now.UnixMilli() >= expires {
		return Snapshot{}, ErrStale
	}
	l, err := localization.ParseLocale(locale)
	if err != nil {
		return Snapshot{}, storage("control selection", err)
	}
	guard := authorize
	if authorize != nil {
		guard = func() (time.Time, bool) {
			at, allowed := authorize()
			return at, allowed && at.UnixMilli() < expires
		}
	}
	v, err := s.change(ctx, tx, chat, rev, l, now, guard)
	if err != nil {
		return v, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE settings_controls SET consumed=1 WHERE token=? AND consumed=0`, token)
	if err != nil {
		return v, storage("consume control", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return v, storage("consume result", err)
	}
	if n != 1 {
		return v, ErrStale
	}
	return v, nil
}
