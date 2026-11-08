package views

import (
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

func TestSettingsStyles(t *testing.T) {
	// A zero catalog is enough to verify styling; complete catalog validation is tested in its module.
	view := Settings(localization.Catalog{}, "ru")
	if len(view.Text) != 3 || view.Text[0].Style != core.Bold || view.Text[2].Style != core.Code {
		t.Fatalf("styles: %+v", view)
	}

	if got := Preview("A\nB\r\nC", 80); got != "A B C" {
		t.Fatal(got)
	}
}
