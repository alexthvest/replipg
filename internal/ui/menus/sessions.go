// Package menus owns bounded ephemeral menu sessions. Call only on the runtime owner.
package menus

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

var ErrInvalid = errors.New("invalid or expired menu")

type Action struct {
	Kind     string
	Language string
	Page     int
	Entry    core.ID
	ItemID   string
}

type Session struct {
	Token     string
	Kind      string
	Group     core.GroupID
	Opener    core.UserID
	Message   int
	Page      int
	Character core.ID
	Used      time.Time
	actions   map[string]Action
	sequence  uint64
	Refs      []string
}

type Sessions struct {
	ttl   time.Duration
	limit int
	now   func() time.Time
	token func() (string, error)
	pool  map[string]*Session
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
	}, nil
}

func (s *Sessions) Sweep() {
	now := s.now()

	for token, session := range s.pool {
		if !now.Before(session.Used.Add(s.ttl)) {
			delete(s.pool, token)
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

		delete(s.pool, victim.Token)
	}

	session := &Session{
		Token:   token,
		Kind:    kind,
		Group:   scope.Group,
		Opener:  opener,
		Used:    s.now(),
		actions: map[string]Action{},
	}
	s.pool[token] = session
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

	session.Message = message
	session.Used = s.now()
	return session, nil
}

func (s *Sessions) Actions(session *Session, actions []Action) ([]string, error) {
	if s.pool[session.Token] != session {
		return nil, ErrInvalid
	}

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
		return nil, Action{}, ErrInvalid
	}

	session.Used = s.now()
	return session, action, nil
}
