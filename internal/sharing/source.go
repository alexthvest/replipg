package sharing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const sharePrefix = "share:v1:"

type sourceDecision uint8

const (
	sourceInvalid sourceDecision = iota
	sourceValid
	sourceUnavailable
	sourceExpired
)

func parseShareToken(data string) (string, bool) {
	if len(data) != len(sharePrefix)+32 || !strings.HasPrefix(data, sharePrefix) {
		return "", false
	}
	token := data[len(sharePrefix):]
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return token, true
}

// fingerprint validates every explicit entity before canonical ordering. The
// transport mapper already excludes validated automatic lexical annotations.
func fingerprint(body runtimecontract.MessageContent) ([]byte, error) {
	body.Buttons = nil
	if err := runtimecontract.ValidateResponse(runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -1, Kind: runtimecontract.ChatKindGroup}, Content: body}); err != nil {
		return nil, err
	}
	entities := append([]runtimecontract.TextEntity(nil), body.Entities...)
	for _, entity := range entities {
		if !utf8.ValidString(entity.URL) {
			return nil, errors.New("sharing: invalid source formatting")
		}
	}
	sort.Slice(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		if a.Offset != b.Offset {
			return a.Offset < b.Offset
		}
		if a.Length != b.Length {
			return a.Length < b.Length
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.URL < b.URL
	})
	// Length-prefixed fields prevent text and entity serialization ambiguities.
	encoded := binary.BigEndian.AppendUint32(nil, 1)
	addString := func(s string) {
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(s)))
		encoded = append(encoded, s...)
	}
	addString(body.Text)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(entities)))
	for _, entity := range entities {
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(entity.Offset))
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(entity.Length))
		addString(string(entity.Kind))
		addString(entity.URL)
	}
	sum := sha256.Sum256(encoded)
	return sum[:], nil
}
func verifySource(ctx context.Context, tx runtimecontract.DBTX, cb interaction.Callback, now time.Time) (viewRecord, runtimecontract.MessageContent, sourceDecision, error) {
	var empty viewRecord
	var body runtimecontract.MessageContent
	token, ok := parseShareToken(cb.Data)
	if !ok || cb.ActorIsBot || cb.User <= 0 || cb.Chat >= 0 || (cb.ChatKind != runtimecontract.ChatKindGroup && cb.ChatKind != runtimecontract.ChatKindSupergroup) || cb.QueryID == "" || cb.EphemeralMessageID <= 0 {
		return empty, body, sourceInvalid, nil
	}
	v, err := readView(ctx, tx, token)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, body, sourceInvalid, nil
	}
	if err != nil {
		return empty, body, sourceInvalid, err
	}
	if v.Owner != cb.User || v.Destination.ChatID != cb.Chat || v.Destination.Kind != cb.ChatKind {
		return empty, body, sourceInvalid, nil
	}
	source := cb.Source
	if source != nil && source.ReceiverUserID != 0 && source.ReceiverUserID != cb.User {
		return empty, body, sourceInvalid, nil
	}
	if v.Retired || !v.UsableUntil.IsZero() && !now.Before(v.UsableUntil) {
		return v, body, sourceExpired, nil
	}
	if source == nil || source.EphemeralMessageID <= 0 || source.EphemeralMessageID != int64(cb.EphemeralMessageID) {
		return v, body, sourceUnavailable, nil
	}
	present := false
	for _, data := range source.CallbackData {
		if data == cb.Data {
			present = true
			break
		}
	}
	if !present {
		return v, body, sourceUnavailable, nil
	}
	digest, err := fingerprint(source.Body)
	if err != nil || v.FingerprintVersion != 1 || !bytes.Equal(digest, v.BodyHash) {
		return v, body, sourceUnavailable, nil
	}
	if v.BoundMessageID != 0 && v.BoundMessageID != source.EphemeralMessageID {
		return v, body, sourceUnavailable, nil
	}
	if v.BoundMessageID == 0 {
		v.BoundMessageID = source.EphemeralMessageID
		if err = updateView(ctx, tx, v); err != nil {
			return v, body, sourceUnavailable, err
		}
		v, err = readView(ctx, tx, token)
		if err != nil {
			return v, body, sourceUnavailable, err
		}
	}
	body = cloneContent(source.Body)
	body.Buttons = nil
	return v, body, sourceValid, nil
}
