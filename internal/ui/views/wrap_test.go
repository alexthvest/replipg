package views

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/core"
)

func TestWrapShopPreservesUnicodeStylesAndControls(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("😀", 80),
		strings.Repeat("я", 90),
		strings.Repeat("long word ", 20),
		strings.Repeat("x", 38) + "\nnext",
	} {
		t.Run(value[:4], func(t *testing.T) {
			original := core.View{
				Text: core.Text{
					{
						Text:  "😀 Shop",
						Style: core.Bold,
					},
					{Text: " • "},
					{
						Text:  "Buy",
						Style: core.Code,
					},
					{Text: "\n\n"},
					{
						Text:  value,
						Style: core.Italic,
					},
				},
				Rows: [][]core.Button{{{
					Caption: "1",
					Data:    "m:session:ref",
					Style:   core.ButtonPrimary,
				}}},
				Menu:     "session",
				Feedback: "literal feedback",
			}
			wrapped := WrapShop(original)
			var text strings.Builder
			for index, span := range wrapped.Text {
				text.WriteString(span.Text)
				if span.Style != original.Text[index].Style ||
					strings.Join(strings.Fields(span.Text), "") != strings.Join(strings.Fields(original.Text[index].Text), "") {
					t.Fatal("wrapping changed literal styled content")
				}
			}
			for _, line := range strings.Split(text.String(), "\n") {
				if utf8.RuneCountInString(line) > ShopLineWidth {
					t.Fatalf("shop line exceeds width: %q", line)
				}
			}
			if original.Text[4].Text != value {
				t.Fatal("wrapping mutated the caller's text")
			}
			if !reflect.DeepEqual(wrapped.Rows, original.Rows) || wrapped.Menu != original.Menu || wrapped.Feedback != original.Feedback {
				t.Fatal("wrapping changed controls or delivery metadata")
			}
			if !reflect.DeepEqual(WrapShop(wrapped), wrapped) {
				t.Fatal("repeated wrapping changed the menu")
			}
		})
	}
}

func TestWrapShopKeepsTelegramMessageLimit(t *testing.T) {
	for _, units := range []int{4080, 4096} {
		original := strings.Repeat("😀", units/2)
		wrapped := WrapShop(core.View{Text: core.Text{{
			Text:  original,
			Style: core.Code,
		}}})
		text := wrapped.Text[0].Text
		if len(utf16.Encode([]rune(text))) > 4096 || strings.ReplaceAll(text, "\n", "") != original {
			t.Fatal("wrapping overflowed or truncated a deliverable message")
		}
	}
}
