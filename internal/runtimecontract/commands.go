package runtimecontract

import (
	"unicode/utf8"
)

// CommandDeclaration describes one command in the application-owned group menu.
type CommandDeclaration struct {
	Name        string
	Description string
	Ephemeral   bool
}

// ValidateCommandDeclarations checks the complete menu without I/O or retaining values in errors.
func ValidateCommandDeclarations(commands []CommandDeclaration) error {
	if len(commands) > 100 {
		return validationError("commands", ReasonOutOfRange)
	}
	seen := make(map[string]bool, len(commands))
	for _, command := range commands {
		if len(command.Name) == 0 || len(command.Name) > 32 {
			return validationError("commands.name", ReasonOutOfRange)
		}
		for _, c := range command.Name {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
			default:
				return validationError("commands.name", ReasonInvalid)
			}
		}
		if seen[command.Name] {
			return validationError("commands.name", ReasonInconsistent)
		}
		seen[command.Name] = true
		if !utf8.ValidString(command.Description) {
			return validationError("commands.description", ReasonInvalid)
		}
		if n := utf8.RuneCountInString(command.Description); n < 1 || n > 256 {
			return validationError("commands.description", ReasonOutOfRange)
		}
	}
	return nil
}
