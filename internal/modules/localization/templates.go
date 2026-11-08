package localization

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/alexthvest/replipg/internal/core"
)

var placeholder = regexp.MustCompile(`\{([a-z_]+)\}`)

// Template parameters are part of each interface key's contract.
var parameters = map[string][]string{
	"command.replipg.balance": {"account", "amount"},
	"command.items.given":     {"item"},
	"command.admin.updated":   {"user"},
	"command.give.success": {
		"recipient",
		"item",
		"quantity",
	},
}

func validateTemplate(key, text string) error {
	var found []string

	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(found, match[1]) {
			found = append(found, match[1])
		}
	}

	rest := placeholder.ReplaceAllString(text, "")
	if strings.ContainsAny(rest, "{}") {
		return fmt.Errorf("locale %s: malformed template", key)
	}

	expected := slices.Clone(parameters[key])
	slices.Sort(expected)
	slices.Sort(found)
	if !slices.Equal(expected, found) {
		return fmt.Errorf("locale %s: unexpected or missing placeholders", key)
	}
	return nil
}

func (c Catalog) Render(language, key string, values map[string]string) (core.Text, error) {
	text, ok := c.Lookup(language, key)
	if !ok {
		return nil, fmt.Errorf("missing translation %s", key)
	}
	if len(values) != len(parameters[key]) {
		return nil, fmt.Errorf("incorrect parameters for %s", key)
	}

	var result core.Text
	offset := 0

	for _, match := range placeholder.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("missing parameter %s", name)
		}

		if match[0] > offset {
			result = append(result, core.Span{Text: text[offset:match[0]]})
		}

		result = append(result, core.Span{
			Text:  value,
			Style: core.Code,
		})
		offset = match[1]
	}

	if offset < len(text) {
		result = append(result, core.Span{Text: text[offset:]})
	}
	return result, nil
}
