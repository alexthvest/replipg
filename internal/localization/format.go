package localization

import (
	"errors"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode/utf16"
)

// TextSpan describes formatting in visible text using UTF-16 offsets.
// Kinds are limited to bold, italic, and code, independent of a transport SDK.
type TextSpan struct {
	Kind   string
	Offset int
	Length int
}

// FormattedText retains literal visible text alongside its formatting spans.
type FormattedText struct {
	Text  string
	Spans []TextSpan
}

// Formatted renders a message with its catalog-authored presentation. Parameters
// are appended as literal text and are never interpreted as formatting tags.
func (c *Catalog) Formatted(l Locale, k Key, args Args) (FormattedText, error) {
	spec, ok := manifest[k]
	if !ok || spec.plural {
		return FormattedText{}, errors.New("localization: invalid message key")
	}

	return c.renderFormatted(l, string(k), spec, args)
}

// Only explicitly styled messages interpret tags. Item names, descriptions,
// button labels, and other plain resources retain their literal text.
func styledMessage(key string) bool {
	switch Key(key) {
	case "item.probe", "character.summary", "inventory.title", "inventory.equipment",
		"inventory.equipped", "inventory.unequipped", "inventory.detail",
		"inventory.bonuses", "inventory.slot_attack", "inventory.slot_defense",
		"inventory.healing", "inventory.attack", "inventory.defense", "settings.title":
		return true
	default:
		return false
	}
}

func formatTemplate(t *template.Template, params map[string]ParamKind, args Args, styled bool) (FormattedText, error) {
	bad := errors.New("localization: invalid message parameters")
	if len(args) != len(params) {
		return FormattedText{}, bad
	}

	for name, kind := range params {
		if value, ok := args[name]; !ok || value.kind != kind {
			return FormattedText{}, bad
		}
	}

	var text strings.Builder
	var spans []TextSpan
	var active string
	offset := 0
	appendText := func(value string) {
		length := len(utf16.Encode([]rune(value)))
		if active != "" && length > 0 {
			if len(spans) > 0 && spans[len(spans)-1].Kind == active && spans[len(spans)-1].Offset+spans[len(spans)-1].Length == offset {
				spans[len(spans)-1].Length += length
			} else {
				spans = append(spans, TextSpan{Kind: active, Offset: offset, Length: length})
			}
		}

		text.WriteString(value)
		offset += length
	}

	for _, node := range t.Root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode:
			literal := string(n.Text)
			for styled && strings.Contains(literal, "<") {
				start := strings.IndexByte(literal, '<')
				appendText(literal[:start])
				literal = literal[start:]
				end := strings.IndexByte(literal, '>')
				if end < 0 {
					return FormattedText{}, errors.New("localization: invalid formatting")
				}

				tag := literal[1:end]
				closing := strings.HasPrefix(tag, "/")
				kind := map[string]string{"b": "bold", "i": "italic", "code": "code"}[strings.TrimPrefix(tag, "/")]
				if kind == "" || closing && active != kind || !closing && active != "" {
					return FormattedText{}, errors.New("localization: invalid formatting")
				}

				if closing {
					active = ""
				} else {
					active = kind
				}

				literal = literal[end+1:]
			}
			appendText(literal)
		case *parse.ActionNode:
			// compileTemplate admits only a single validated field per action.
			field := n.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
			value := args[field.Ident[0]]
			if value.kind == IntegerParam {
				appendText(strconv.FormatInt(value.integer, 10))
			} else {
				appendText(value.text)
			}
		}
	}

	if active != "" {
		return FormattedText{}, errors.New("localization: invalid formatting")
	}

	return FormattedText{Text: text.String(), Spans: spans}, nil
}
