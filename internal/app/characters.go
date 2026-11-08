package app

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/views"
	"github.com/yohamta/donburi"
)

func characterRoutes(registry *runtime.Registry, m *modules, routes map[string]boundRoute) error {
	for _, name := range []string{"character", "rename"} {
		handler, err := runtime.Bind(registry, name, func(
			ctx context.Context,
			world donburi.World,
			scope core.Scope,
			input telegram.Input,
		) (core.View, error) {
			if name == "character" && input.Args != "" {
				return m.text(ctx, scope, "command.character.usage")
			}

			var c characters.Character
			var err error
			if name == "rename" {
				c, err = m.characters.Rename(ctx, scope, input.User, input.Args)
			} else {
				c, err = m.characters.Profile(ctx, scope, input.User)
			}

			if errors.Is(err, characters.ErrName) {
				return m.text(ctx, scope, "error.name")
			}
			if err != nil {
				return m.text(ctx, scope, "error.persistence")
			}

			if err = m.projectCharacter(ctx, world, c); err != nil {
				return core.View{}, err
			}

			language, err := m.localization.Language(ctx, scope.Group)
			if err != nil {
				return core.View{}, err
			}
			return views.Character(m.bundle.Locales(), language, c), nil
		})
		if err != nil {
			return err
		}

		routes[name] = handler
	}
	return nil
}
