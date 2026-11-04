package runtimecontract

import (
	"strings"
	"testing"
)

func TestCommandDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		commands []CommandDeclaration
		valid    bool
	}{
		{"nil", nil, true}, {"empty", []CommandDeclaration{}, true},
		{"valid", []CommandDeclaration{{Name: "character", Description: "Inspect your character", Ephemeral: true}}, true},
		{"uppercase", []CommandDeclaration{{Name: "Character", Description: "Inspect"}}, false},
		{"slash", []CommandDeclaration{{Name: "/character", Description: "Inspect"}}, false},
		{"missing", []CommandDeclaration{{Description: "Inspect"}}, false},
		{"long", []CommandDeclaration{{Name: strings.Repeat("a", 33), Description: "Inspect"}}, false},
		{"description", []CommandDeclaration{{Name: "character"}}, false},
		{"long description", []CommandDeclaration{{Name: "character", Description: strings.Repeat("a", 257)}}, false},
		{"invalid utf8", []CommandDeclaration{{Name: "character", Description: string([]byte{255})}}, false},
		{"duplicate", []CommandDeclaration{{Name: "a", Description: "A"}, {Name: "a", Description: "B"}}, false},
		{"count", make([]CommandDeclaration, 101), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCommandDeclarations(tc.commands); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
