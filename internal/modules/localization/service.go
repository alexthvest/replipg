package localization

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/core"
)

type GroupSettings interface {
	Language(context.Context, core.GroupID) (string, error)
}

type Service struct {
	settings GroupSettings
}

func NewService(settings GroupSettings) (*Service, error) {
	if settings == nil {
		return nil, errors.New("localization: settings required")
	}
	return &Service{settings: settings}, nil
}

func (s *Service) Language(ctx context.Context, group core.GroupID) (string, error) {
	return s.settings.Language(ctx, group)
}
