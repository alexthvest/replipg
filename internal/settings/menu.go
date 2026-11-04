// Package settings owns durable group language and menu preferences.
package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type CommandSpec struct {
	Name        string
	Description localization.Key
	Ephemeral   bool
}
type MenuBuilder struct {
	commands map[localization.Locale][]runtimecontract.CommandDeclaration
	hashes   map[localization.Locale]string
}

func NewMenuBuilder(c *localization.Catalog, specs []CommandSpec) (*MenuBuilder, error) {
	if c == nil {
		return nil, errors.New("missing settings catalog")
	}
	b := &MenuBuilder{commands: make(map[localization.Locale][]runtimecontract.CommandDeclaration), hashes: make(map[localization.Locale]string)}
	for _, l := range []localization.Locale{localization.English, localization.Russian} {
		commands := make([]runtimecontract.CommandDeclaration, 0, len(specs))
		for _, s := range specs {
			text, err := c.Text(l, s.Description, nil)
			if err != nil {
				return nil, err
			}
			commands = append(commands, runtimecontract.CommandDeclaration{Name: s.Name, Description: text, Ephemeral: s.Ephemeral})
		}
		if err := runtimecontract.ValidateCommandDeclarations(commands); err != nil {
			return nil, err
		}
		data, err := json.Marshal(commands)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		b.commands[l] = commands
		b.hashes[l] = hex.EncodeToString(sum[:])
	}
	return b, nil
}
func (b *MenuBuilder) Commands(l localization.Locale) ([]runtimecontract.CommandDeclaration, error) {
	v, ok := b.commands[l]
	if !ok {
		return nil, ErrInvalid
	}
	return append([]runtimecontract.CommandDeclaration(nil), v...), nil
}
func (b *MenuBuilder) Fingerprint(l localization.Locale) (string, error) {
	v, ok := b.hashes[l]
	if !ok {
		return "", ErrInvalid
	}
	return v, nil
}
