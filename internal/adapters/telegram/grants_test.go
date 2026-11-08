package telegram

import "testing"

func TestGiveParser(t *testing.T) {
	for _, args := range []string{
		"",
		"coin",
		"coin 0",
		"coin -1",
		"coin +1",
		"coin 1.5",
		"coin 1001",
		"coin nope",
		"coin 999999999999999999999999999999",
		"coin 1 extra",
	} {
		if _, _, err := ParseGive(args); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}

	for _, args := range []string{"coin 1", "stick 1000", " coin   5 "} {
		if id, quantity, err := ParseGive(args); err != nil || id == "" || quantity <= 0 {
			t.Fatalf("valid %q: %v", args, err)
		}
	}
}
