package views

import (
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/core"
)

// ShopLineWidth matches the compact shop presentation in replipg-go.
const ShopLineWidth = 38

// WrapShop bounds shop lines while preserving literal text, span styles, and
// controls. It reserves Telegram's remaining UTF-16 budget for inserted breaks.
func WrapShop(view core.View) core.View {
	var runes []rune
	var sources []int
	for index, span := range view.Text {
		for _, value := range span.Text {
			runes = append(runes, value)
			sources = append(sources, index)
		}
	}

	// Prefer replacing a word boundary to avoid adding message length.
	for start := 0; start < len(runes); {
		end := start
		space := -1
		for end < len(runes) && runes[end] != '\n' && end-start < ShopLineWidth {
			if runes[end] == ' ' {
				space = end
			}
			end++
		}
		if end == len(runes) {
			break
		}
		if runes[end] == '\n' {
			start = end + 1
			continue
		}
		if runes[end] == ' ' {
			space = end
		}
		if space >= start {
			runes[space] = '\n'
			start = space + 1
		} else {
			start = end
		}
	}

	remaining := 4096 - len(utf16.Encode(runes))
	parts := make([]strings.Builder, len(view.Text))
	column := 0
	for index, value := range runes {
		part := &parts[sources[index]]
		if column == ShopLineWidth && value != '\n' && remaining > 0 {
			part.WriteByte('\n')
			remaining--
			column = 0
		}
		part.WriteRune(value)
		column++
		if value == '\n' {
			column = 0
		}
	}

	view.Text = slices.Clone(view.Text)
	for index := range view.Text {
		view.Text[index].Text = parts[index].String()
	}
	return view
}
