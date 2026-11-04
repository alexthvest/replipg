// Package localization provides strict immutable English and Russian presentation.
package localization

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type Locale string

const (
	English Locale = "en"
	Russian Locale = "ru"
)

func ParseLocale(s string) (Locale, error) {
	l := Locale(s)
	if l != English && l != Russian {
		return "", errors.New("localization: unsupported locale")
	}

	return l, nil
}

type Key string

type ParamKind uint8

const (
	TextParam ParamKind = iota + 1
	IntegerParam
)

type Value struct {
	kind    ParamKind
	text    string
	integer int64
}

func String(s string) Value { return Value{kind: TextParam, text: s} }

func Integer(n int64) Value { return Value{kind: IntegerParam, integer: n} }

type Args map[string]Value

type ItemText struct{ Name, Description string }

type messageSpec struct {
	plural bool
	limit  int
	params map[string]ParamKind
}

type Catalog struct {
	resources map[Locale]map[string]string
	templates map[Locale]map[string]*template.Template
}

//go:embed locales/en.json
var english []byte

//go:embed locales/ru.json
var russian []byte

func Load() (*Catalog, error) { return Parse(english, russian) }

// Parse validates complete resource fixtures with the same schema as Load.
func Parse(en, ru []byte) (*Catalog, error) {
	c := &Catalog{resources: make(map[Locale]map[string]string), templates: make(map[Locale]map[string]*template.Template)}
	for _, v := range []struct {
		l    Locale
		data []byte
	}{{English, en}, {Russian, ru}} {
		resource, err := parseResource(v.data)
		if err != nil {
			return nil, invalidCatalog("localization: %s invalid resource", v.l)
		}

		required := map[string]messageSpec{}
		c.templates[v.l] = make(map[string]*template.Template)
		for k, s := range manifest {
			if s.plural {
				for _, f := range forms(v.l) {
					expanded := string(k) + "." + f
					if _, exists := required[expanded]; exists {
						return nil, invalidCatalog("localization: manifest key collision")
					}

					required[expanded] = s
				}
			} else {
				if _, exists := required[string(k)]; exists {
					return nil, invalidCatalog("localization: manifest key collision")
				}

				required[string(k)] = s
			}
		}

		for k := range resource {
			if _, ok := required[k]; !ok {
				return nil, invalidCatalog("localization: %s unknown key", v.l)
			}
		}

		keys := make([]string, 0, len(required))
		for k := range required {
			keys = append(keys, k)
		}

		sort.Strings(keys)
		for _, k := range keys {
			s := required[k]
			value, ok := resource[k]
			if !ok {
				return nil, invalidCatalog("localization: %s missing %s", v.l, k)
			}

			if !safe(value, true) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > s.limit || (s.limit == 4096 && len(utf16.Encode([]rune(value))) > 4096) {
				return nil, invalidCatalog("localization: %s invalid %s", v.l, k)
			}

			compiled, err := compileTemplate(value, s.params)
			if err != nil {
				return nil, invalidCatalog("localization: %s invalid template %s", v.l, k)
			}

			c.templates[v.l][k] = compiled
		}

		c.resources[v.l] = resource
	}
	return c, nil
}

func parseResource(data []byte) (map[string]string, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("invalid resource")
	}

	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("invalid resource")
	}

	m := map[string]string{}
	for d.More() {
		t, e := d.Token()
		k, ok := t.(string)
		if e != nil || !ok {
			return nil, errors.New("invalid key")
		}

		if _, ok := m[k]; ok {
			return nil, errors.New("duplicate key")
		}

		t, e = d.Token()
		v, ok := t.(string)
		if e != nil || !ok {
			return nil, errors.New("invalid value")
		}

		m[k] = v
	}

	last, e := d.Token()
	if e != nil || last != json.Delim('}') {
		return nil, errors.New("invalid resource")
	}

	if _, e = d.Token(); !errors.Is(e, io.EOF) {
		return nil, errors.New("trailing data")
	}

	return m, nil
}

func forms(l Locale) []string {
	if l == Russian {
		return []string{"one", "few", "many", "other"}
	}

	return []string{"one", "other"}
}

func plural(l Locale, n int64) string {
	if l == English {
		if n == 1 {
			return "one"
		}

		return "other"
	}

	if n%10 == 1 && n%100 != 11 {
		return "one"
	}

	if n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14) {
		return "few"
	}

	return "many"
}

func (c *Catalog) Text(l Locale, k Key, a Args) (string, error) {
	s, ok := manifest[k]
	if !ok || s.plural {
		return "", errors.New("localization: invalid message key")
	}

	return c.render(l, string(k), s, a)
}

func (c *Catalog) Count(l Locale, k Key, n int64, a Args) (string, error) {
	s, ok := manifest[k]
	if !ok || !s.plural || n < 0 {
		return "", errors.New("localization: invalid count")
	}

	args := Args{}
	for k, v := range a {
		args[k] = v
	}

	if _, exists := args["count"]; exists {
		return "", errors.New("localization: count supplied twice")
	}

	args["count"] = Integer(n)
	return c.render(l, string(k)+"."+plural(l, n), s, args)
}

func (c *Catalog) render(l Locale, k string, s messageSpec, a Args) (string, error) {
	if c == nil {
		return "", errors.New("localization: missing catalog")
	}

	r, ok := c.resources[l]
	if !ok {
		return "", errors.New("localization: unsupported locale")
	}

	_, ok = r[k]
	if !ok {
		return "", errors.New("localization: missing message")
	}

	v, err := executeTemplate(c.templates[l][k], s.params, a)
	if err != nil {
		return "", err
	}

	if !safe(v, true) || utf8.RuneCountInString(v) > s.limit || (s.limit == 4096 && len(utf16.Encode([]rune(v))) > 4096) {
		return "", errors.New("localization: invalid rendered text")
	}

	return v, nil
}

func compileTemplate(text string, params map[string]ParamKind) (*template.Template, error) {
	bad := errors.New("localization: invalid template")
	if strings.Contains(text, "{{/*") {
		return nil, bad
	}

	t, err := template.New("message").Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, bad
	}

	if len(t.Templates()) != 1 {
		return nil, bad
	}

	seen := map[string]bool{}
	for _, node := range t.Root.Nodes {
		switch n := node.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 {
				return nil, bad
			}

			cmd := n.Pipe.Cmds[0]
			if len(cmd.Args) != 1 {
				return nil, bad
			}

			field, ok := cmd.Args[0].(*parse.FieldNode)
			if !ok || len(field.Ident) != 1 {
				return nil, bad
			}

			name := field.Ident[0]
			if _, ok := params[name]; !ok {
				return nil, bad
			}

			seen[name] = true
		default:
			return nil, bad
		}
	}

	if len(seen) != len(params) {
		return nil, bad
	}

	return t, nil
}

func executeTemplate(t *template.Template, params map[string]ParamKind, args Args) (string, error) {
	bad := errors.New("localization: invalid message parameters")
	if len(args) != len(params) {
		return "", bad
	}

	values := map[string]any{}
	for k, kind := range params {
		v, ok := args[k]
		if !ok || v.kind != kind {
			return "", bad
		}

		if kind == TextParam {
			values[k] = v.text
		} else {
			values[k] = v.integer
		}
	}

	var out bytes.Buffer

	if err := t.Execute(&out, values); err != nil {
		return "", bad
	}

	return out.String(), nil
}

func safe(s string, newline bool) bool {
	if !utf8.ValidString(s) {
		return false
	}

	for _, r := range s {
		if r == unicode.ReplacementChar || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) || (unicode.IsControl(r) && (!newline || r != '\n')) {
			return false
		}
	}
	return true
}

func (c *Catalog) Item(l Locale, id string) (ItemText, error) {
	name, e := c.Text(l, Key("items."+id+".name"), nil)
	if e != nil {
		return ItemText{}, e
	}

	desc, e := c.Text(l, Key("items."+id+".description"), nil)
	return ItemText{Name: name, Description: desc}, e
}

func (c *Catalog) ValidateItems(ids []string, validate func(ItemText) error) error {
	if c == nil {
		return invalidCatalog("localization: missing catalog")
	}

	if validate == nil {
		return invalidCatalog("localization: missing item validator")
	}

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return invalidCatalog("localization: duplicate item")
		}

		seen[id] = true
		for _, l := range []Locale{English, Russian} {
			v, e := c.Item(l, id)
			if e != nil {
				return invalidCatalog("localization: %s missing retained item translation", l)
			}

			if e = validate(v); e != nil {
				return invalidCatalog("localization: %s invalid items.%s text", l, id)
			}
		}
	}

	for key := range c.resources[English] {
		if strings.HasPrefix(key, "items.") && strings.HasSuffix(key, ".name") {
			id := strings.TrimSuffix(strings.TrimPrefix(key, "items."), ".name")
			if !seen[id] {
				return invalidCatalog("localization: item catalog coverage mismatch")
			}
		}
	}
	return nil
}
