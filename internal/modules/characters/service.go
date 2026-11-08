// Package characters owns persistent group-scoped character identities and naming.
package characters

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/core"
)

type Character struct {
	ID    core.ID
	Group core.GroupID
	User  core.UserID
	Name  string
}

type Creator interface {
	FindCharacter(context.Context, core.GroupID, core.UserID) (Character, bool, error)
	EnsureCharacter(context.Context, Character) (Character, error)
}

type Repository interface {
	Creator
	RenameCharacter(context.Context, core.GroupID, core.UserID, string) (Character, error)
	LoadCharacters(context.Context) ([]Character, error)
}

var ErrName = errors.New("name must contain 1-64 Unicode code points after trimming")

func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	length := utf8.RuneCountInString(name)
	if !utf8.ValidString(name) || length < 1 || length > 64 {
		return "", ErrName
	}
	return name, nil
}

func InitialName(user core.User) string {
	if user.Username != "" {
		return strings.TrimPrefix(user.Username, "@")
	}
	return strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
}

// Ensure can run with a transaction-bound Creator; it never commits independently.
func Ensure(
	ctx context.Context,
	repo Creator,
	ids func() (core.ID, error),
	group core.GroupID,
	user core.User,
) (Character, error) {
	if group >= 0 || user.ID <= 0 {
		return Character{}, errors.New("invalid character scope")
	}

	existing, ok, err := repo.FindCharacter(ctx, group, user.ID)
	if err != nil || ok {
		return existing, err
	}

	name := InitialName(user)
	if name == "" || !utf8.ValidString(name) {
		return Character{}, errors.New("character creation: Telegram display name required")
	}

	id, err := ids()
	if err != nil {
		return Character{}, err
	}
	return repo.EnsureCharacter(ctx, Character{
		ID:    id,
		Group: group,
		User:  user.ID,
		Name:  name,
	})
}

type Service struct {
	repo Repository
	ids  func() (core.ID, error)
}

func New(repo Repository, ids func() (core.ID, error)) (*Service, error) {
	if repo == nil || ids == nil {
		return nil, errors.New("characters: repository and IDs required")
	}
	return &Service{
		repo: repo,
		ids:  ids,
	}, nil
}

func (s *Service) Profile(ctx context.Context, scope core.Scope, user core.User) (Character, error) {
	if err := scope.Validate(); err != nil {
		return Character{}, err
	}
	if scope.Actor != user.ID {
		return Character{}, errors.New("foreign character request")
	}
	return Ensure(ctx, s.repo, s.ids, scope.Group, user)
}

func (s *Service) Rename(ctx context.Context, scope core.Scope, user core.User, name string) (Character, error) {
	name, err := ValidateName(name)
	if err != nil {
		return Character{}, err
	}

	if _, err = s.Profile(ctx, scope, user); err != nil {
		return Character{}, err
	}
	return s.repo.RenameCharacter(ctx, scope.Group, scope.Actor, name)
}

func (s *Service) Load(ctx context.Context) ([]Character, error) {
	return s.repo.LoadCharacters(ctx)
}

func (s *Service) Current(ctx context.Context, group core.GroupID, user core.UserID) (Character, error) {
	c, ok, err := s.repo.FindCharacter(ctx, group, user)
	if err != nil {
		return c, err
	}
	if !ok {
		return c, errors.New("character not found")
	}
	return c, nil
}
