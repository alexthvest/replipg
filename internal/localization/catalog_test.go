package localization

import (
	"errors"
	"strings"
	"testing"
)

func TestCatalogBilingualAndPlural(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []Locale{English, Russian} {
		for _, n := range []int64{0, 1, 2, 4, 5, 11, 14, 21, 25, 101, 111} {
			s, e := c.Count(l, "recovery.seconds", n, nil)
			if e != nil || s == "" {
				t.Fatalf("%s %d: %q %v", l, n, s, e)
			}
		}
	}
	if _, err := c.Count(Russian, "recovery.seconds", -1, nil); err == nil {
		t.Fatal("negative count accepted")
	}
	if _, err := c.Text(English, "character.summary", nil); err == nil {
		t.Fatal("missing args accepted")
	}
	if _, err := ParseLocale("RU"); err == nil {
		t.Fatal("unsupported locale accepted")
	}
	if err := c.ValidateItems([]string{"coin", "stick", "cloth_armor", "healing_potion"}, func(v ItemText) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
func TestStrictResource(t *testing.T) {
	for _, data := range []string{`{"x":"a","x":"b"}`, `{"x":null}`, `{"x":{}}`, `{"x":[]}`, `{"x":3}`, `{} {}`} {
		if _, err := parseResource([]byte(data)); err == nil {
			t.Errorf("accepted %s", data)
		}
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Text(Russian, "inventory.title", Args{"owner": String("Alice_1")})
	if err != nil || !strings.Contains(s, "Alice_1") {
		t.Fatalf("%q %v", s, err)
	}
}

func TestCompleteCatalogValidation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"unknown", `"settings.title": "Group settings"`, `"unknown": "Group settings"`},
		{"missing", `"settings.title": "Group settings",`, ``},
		{"empty", `"settings.title": "Group settings"`, `"settings.title": " "`},
		{"control", `"settings.title": "Group settings"`, `"settings.title": "bad\u202e"`},
		{"wrong placeholder", `Language: {{.language}}`, `Language: {{.private}}`},
		{"missing placeholder", `Language: {{.language}}`, `Language`},
		{"function", `Language: {{.language}}`, `{{printf "%s" .language}}`},
		{"nested field", `Language: {{.language}}`, `{{.language.private}}`},
		{"pipeline", `Language: {{.language}}`, `{{.language | printf}}`},
		{"branch", `Language: {{.language}}`, `{{if .language}}yes{{end}}`},
		{"template definition", `Language: {{.language}}`, `{{define "extra"}}x{{end}}{{.language}}`},
		{"literal plural collision", `"settings.title": "Group settings"`, `"recovery.seconds": "Group settings"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(string(english), tc.old, tc.replacement, 1)
			if data == string(english) {
				t.Fatal("fixture did not change")
			}
			if _, err := Parse([]byte(data), russian); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Text(English, "inventory.title", Args{"owner": Integer(1)}); err == nil {
		t.Fatal("wrong type accepted")
	}
	if _, err := c.Text(English, "settings.title", Args{"secret": String("value")}); err == nil {
		t.Fatal("extra parameter accepted")
	}
	if _, err := c.Text(English, "inventory.title", Args{"owner": String(strings.Repeat("😀", 2100))}); err == nil {
		t.Fatal("UTF16 overflow accepted")
	}
	for _, tc := range []struct {
		locale Locale
		count  int64
		want   string
	}{{English, 0, "0 seconds"}, {English, 1, "1 second"}, {Russian, 0, "0 секунд"}, {Russian, 1, "1 секунда"}, {Russian, 2, "2 секунды"}, {Russian, 4, "4 секунды"}, {Russian, 5, "5 секунд"}, {Russian, 11, "11 секунд"}, {Russian, 12, "12 секунд"}, {Russian, 14, "14 секунд"}, {Russian, 21, "21 секунда"}, {Russian, 22, "22 секунды"}, {Russian, 25, "25 секунд"}, {Russian, 101, "101 секунда"}, {Russian, 111, "111 секунд"}} {
		got, err := c.Count(tc.locale, "recovery.seconds", tc.count, nil)
		if err != nil || got != tc.want {
			t.Errorf("got %q %v want %q", got, err, tc.want)
		}
	}
}

func TestCatalogErrorsAreActionableAndSafe(t *testing.T) {
	missing := strings.Replace(string(english), `"settings.title": "Group settings",`, "", 1)
	_, err := Parse([]byte(missing), russian)
	var catalogErr *CatalogError
	if !errors.As(err, &catalogErr) || !strings.Contains(err.Error(), "en") || !strings.Contains(err.Error(), "settings.title") {
		t.Fatalf("missing safe locale/key: %v", err)
	}
	for _, data := range []string{`{"private-input-key":"private-input-value"}`, `{"settings.title":null,"private-input-key":"private-input-value"}`, strings.Replace(string(english), "Language: {{.language}}", "private-input-value {{.secret}}", 1)} {
		_, err := Parse([]byte(data), russian)
		catalogErr = nil
		if !errors.As(err, &catalogErr) {
			t.Fatalf("untyped invalid catalog error: %v", err)
		}
		for _, secret := range []string{"private-input-key", "private-input-value", "secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Fatal("catalog diagnostic leaked resource input")
			}
		}
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	validatorErr := errors.New("private-validator-cause")
	err = c.ValidateItems([]string{"coin", "stick", "cloth_armor", "healing_potion"}, func(ItemText) error { return validatorErr })
	catalogErr = nil
	if !errors.As(err, &catalogErr) || !strings.Contains(err.Error(), "items.coin") || strings.Contains(err.Error(), validatorErr.Error()) || errors.Is(err, validatorErr) {
		t.Fatalf("unsafe or untyped validator diagnostic: %v", err)
	}
	err = c.ValidateItems([]string{"private-item-identity"}, func(ItemText) error { return nil })
	catalogErr = nil
	if !errors.As(err, &catalogErr) || strings.Contains(err.Error(), "private-item-identity") {
		t.Fatalf("unsafe or untyped item coverage diagnostic: %v", err)
	}
}
