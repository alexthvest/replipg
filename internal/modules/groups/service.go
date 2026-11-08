// Package groups owns group settings and game administrator rules.
package groups

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/core"
)

type State struct {
	ID       core.GroupID
	Language string
	Admins   []core.UserID
}

type Repository interface {
	LoadGroups(context.Context) ([]State, error)
	Group(context.Context, core.GroupID) (State, error)
	SetAdmin(context.Context, core.GroupID, core.UserID, bool) (State, error)
	SetLanguage(context.Context, core.GroupID, string) (State, error)
}

type Service struct {
	repo  Repository
	owner core.UserID
}

func New(repo Repository, owner core.UserID) (*Service, error) {
	if repo == nil || owner <= 0 {
		return nil, errors.New("groups: repository and positive owner required")
	}
	return &Service{
		repo:  repo,
		owner: owner,
	}, nil
}

var ErrDenied = errors.New("permission denied")

func (s *Service) IsOwner(actor core.UserID) bool {
	return actor == s.owner
}

func (s *Service) Allowed(ctx context.Context, scope core.Scope) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	if scope.Actor == s.owner {
		return true, nil
	}

	state, err := s.repo.Group(ctx, scope.Group)
	if err != nil {
		return false, err
	}

	for _, id := range state.Admins {
		if id == scope.Actor {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) Admin(ctx context.Context, scope core.Scope, user core.UserID, add bool) (State, error) {
	if err := scope.Validate(); err != nil {
		return State{}, err
	}
	if scope.Actor != s.owner {
		return State{}, ErrDenied
	}
	if user <= 0 {
		return State{}, errors.New("invalid recipient")
	}
	return s.repo.SetAdmin(ctx, scope.Group, user, add)
}

func (s *Service) Language(ctx context.Context, group core.GroupID) (string, error) {
	if group >= 0 {
		return "", errors.New("invalid group")
	}

	state, err := s.repo.Group(ctx, group)
	return state.Language, err
}

func (s *Service) ChangeLanguage(ctx context.Context, scope core.Scope, language string) (State, error) {
	allowed, err := s.Allowed(ctx, scope)
	if err != nil {
		return State{}, err
	}
	if !allowed {
		return State{}, ErrDenied
	}
	if language != "en" && language != "ru" {
		return State{}, errors.New("unsupported language")
	}
	return s.repo.SetLanguage(ctx, scope.Group, language)
}

func (s *Service) Load(ctx context.Context) ([]State, error) {
	return s.repo.LoadGroups(ctx)
}

func (s *Service) State(ctx context.Context, group core.GroupID) (State, error) {
	return s.repo.Group(ctx, group)
}
