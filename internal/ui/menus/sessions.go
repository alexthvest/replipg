// Package menus owns bounded ephemeral menu sessions. Call only on the runtime owner.
package menus

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

var ErrInvalid = errors.New("invalid or expired menu")

func (s *Sessions) Contains(token string) bool {
	return s.pool[token] != nil
}

type Action struct {
	Kind     string
	Language string
	Page     int
	Entry    core.ID
	ItemID   string
	Order    core.ID
	Quantity int64
	Quote    core.ID
}

type Session struct {
	Token             string
	Kind              string
	Group             core.GroupID
	Opener            core.UserID
	Actor             core.UserID
	Message           int
	Page              int
	Character         core.ID
	Used              time.Time
	Screen            string
	Selection         core.ID
	Entry             core.ID
	Order             core.ID
	Quantity          int64
	Dirty             bool
	DesiredRevision   uint64
	DeliveredRevision uint64
	Desired           core.View
	Delivered         core.View
	inFlight          uint64
	retryAt           time.Time
	failures          int
	actionList        []Action
	confirmations     map[string]Action
	actions           map[string]Action
	sequence          uint64
	Refs              []string
}

type Sessions struct {
	ttl   time.Duration
	limit int
	now   func() time.Time
	token func() (string, error)
	pool  map[string]*Session
	index map[Dependency]map[string]bool
}

func RandomToken() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func New(ttl time.Duration, limit int, now func() time.Time, token func() (string, error)) (*Sessions, error) {
	if ttl <= 0 || limit <= 0 || now == nil || token == nil {
		return nil, errors.New("menus: positive TTL, limit, clock and tokens required")
	}
	return &Sessions{
		ttl:   ttl,
		limit: limit,
		now:   now,
		token: token,
		pool:  map[string]*Session{},
		index: map[Dependency]map[string]bool{},
	}, nil
}

func (s *Sessions) Sweep() {
	now := s.now()

	for token, session := range s.pool {
		if !now.Before(session.Used.Add(s.ttl)) {
			s.Remove(token)
		}
	}
}

func (s *Sessions) Create(kind string, scope core.Scope, opener core.UserID) (*Session, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if kind == "" {
		return nil, ErrInvalid
	}

	s.Sweep()
	var token string

	for range 8 {
		candidate, err := s.token()
		if err != nil {
			return nil, err
		}

		if candidate != "" && len(candidate) <= 32 && !strings.Contains(candidate, ":") && s.pool[candidate] == nil {
			token = candidate
			break
		}
	}

	if token == "" {
		return nil, errors.New("menu token collision limit reached")
	}

	if len(s.pool) >= s.limit {
		var victim *Session

		for _, candidate := range s.pool {
			if victim == nil ||
				candidate.Used.Before(victim.Used) ||
				(candidate.Used.Equal(victim.Used) &&
					candidate.Token < victim.Token) {
				victim = candidate
			}
		}

		s.Remove(victim.Token)
	}

	session := &Session{
		Token:   token,
		Kind:    kind,
		Group:   scope.Group,
		Opener:  opener,
		Actor:   scope.Actor,
		Used:    s.now(),
		Dirty:   true,
		actions: map[string]Action{},
	}
	s.pool[token] = session
	s.Depend(session, Dependency{
		Kind:  "group",
		Group: scope.Group,
	}, Dependency{Kind: "content"})
	return session, nil
}

func (s *Sessions) Bind(token string, scope core.Scope, message int) (*Session, error) {
	session := s.pool[token]
	if session == nil ||
		session.Group != scope.Group ||
		session.Message != 0 ||
		message <= 0 ||
		!s.now().Before(session.Used.Add(s.ttl)) {
		return nil, ErrInvalid
	}
	if session.Opener != 0 && session.Opener != scope.Actor {
		return nil, ErrInvalid
	}
	for _, bound := range s.pool {
		if bound != session && bound.Group == scope.Group && bound.Message == message {
			return nil, ErrInvalid
		}
	}

	session.Message = message
	session.Dirty = true
	return session, nil
}

func (s *Sessions) Actions(session *Session, actions []Action) ([]string, error) {
	if s.pool[session.Token] != session {
		return nil, ErrInvalid
	}

	if slices.Equal(session.actionList, actions) && len(session.actions) == len(actions) {
		return slices.Clone(session.Refs), nil
	}

	session.actionList = slices.Clone(actions)
	session.actions = map[string]Action{}
	var payloads []string

	for _, action := range actions {
		session.sequence++
		ref := strconv.FormatUint(session.sequence, 36)
		payload := fmt.Sprintf("m:%s:%s", session.Token, ref)
		if len(payload) > 64 {
			return nil, ErrInvalid
		}

		session.actions[ref] = action
		payloads = append(payloads, payload)
	}
	session.Refs = slices.Clone(payloads)
	return payloads, nil
}

func (s *Sessions) Resolve(data string, scope core.Scope, message int) (*Session, Action, error) {
	fields := strings.Split(data, ":")
	if len(fields) != 3 || fields[0] != "m" || len(data) > 64 {
		return nil, Action{}, ErrInvalid
	}

	session := s.pool[fields[1]]
	if session == nil ||
		session.Message == 0 ||
		session.Message != message ||
		session.Group != scope.Group ||
		(session.Opener != 0 &&
			session.Opener != scope.Actor) ||
		!s.now().Before(session.Used.Add(s.ttl)) {
		return nil, Action{}, ErrInvalid
	}

	action, ok := session.actions[fields[2]]
	if !ok {
		action, ok = session.confirmations[fields[2]]
	}
	if !ok {
		return nil, Action{}, ErrInvalid
	}

	session.Used = s.now()
	return session, action, nil
}

// AcceptConfirmation preserves an accepted financial control for this session's
// lifetime so a repeat can return its durable result after navigation changed.
func (s *Sessions) AcceptConfirmation(session *Session, quote core.ID) {
	if session.confirmations == nil {
		session.confirmations = map[string]Action{}
	}
	for ref, action := range session.actions {
		if action.Kind == "confirm" && action.Quote == quote {
			session.confirmations[ref] = action
		}
	}
}
