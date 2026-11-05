// Package presentation assembles localized text and transport-neutral entities.
package presentation

import (
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Localized preserves the formatting authored in the selected catalog message.
func Localized(c *localization.Catalog, l localization.Locale, key localization.Key, args localization.Args) (runtimecontract.MessageContent, error) {
	text, err := c.Formatted(l, key, args)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	content := runtimecontract.MessageContent{Text: text.Text}
	for _, span := range text.Spans {
		content.Entities = append(content.Entities, runtimecontract.TextEntity{Kind: runtimecontract.TextEntityKind(span.Kind), Offset: span.Offset, Length: span.Length})
	}

	return content, nil
}

// Builder shifts fragment entities as literal text is appended.
type Builder struct {
	content runtimecontract.MessageContent
	length  int
}

// Write appends plain text without interpreting markup.
func (b *Builder) Write(text string) {
	b.content.Text += text
	b.length += len(utf16.Encode([]rune(text)))
}

// Styled appends one explicitly styled literal fragment.
func (b *Builder) Styled(text string, kind runtimecontract.TextEntityKind) {
	length := len(utf16.Encode([]rune(text)))
	if length > 0 {
		b.content.Entities = append(b.content.Entities, runtimecontract.TextEntity{Kind: kind, Offset: b.length, Length: length})
	}

	b.Write(text)
}

// Append preserves an existing fragment's entities and controls.
func (b *Builder) Append(content runtimecontract.MessageContent) {
	for _, entity := range content.Entities {
		entity.Offset += b.length
		b.content.Entities = append(b.content.Entities, entity)
	}

	b.content.Buttons = append(b.content.Buttons, content.Buttons...)
	b.Write(content.Text)
}

// Content returns the assembled message.
func (b *Builder) Content() runtimecontract.MessageContent { return b.content }
