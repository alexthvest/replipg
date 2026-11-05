package localization

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func spanText(t *testing.T, text string, span TextSpan) string {
	t.Helper()
	units := utf16.Encode([]rune(text))
	if span.Offset < 0 || span.Length <= 0 || span.Offset+span.Length > len(units) {
		t.Fatalf("invalid span: %+v in %q", span, text)
	}

	return string(utf16.Decode(units[span.Offset : span.Offset+span.Length]))
}

func TestFormattedInventoryOwnerIsLiteral(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	owner := "😀<b>_`[Alice]&</b>"
	for _, locale := range []Locale{English, Russian} {
		content, err := c.Formatted(locale, "inventory.title", Args{"owner": String(owner)})
		if err != nil {
			t.Fatal(err)
		}

		title := "Inventory"
		if locale == Russian {
			title = "Инвентарь"
		}

		if content.Text != title+" • "+owner+"\n\n" || len(content.Spans) != 2 {
			t.Fatalf("unexpected formatted title: %+v", content)
		}

		if content.Spans[0].Kind != "bold" || spanText(t, content.Text, content.Spans[0]) != title || content.Spans[1].Kind != "code" || spanText(t, content.Text, content.Spans[1]) != owner {
			t.Fatalf("formatting changed dynamic text: %+v", content)
		}

		plain, err := c.Text(locale, "inventory.title", Args{"owner": String(owner)})
		if err != nil || plain != content.Text {
			t.Fatalf("plain and formatted text differ: %q %v", plain, err)
		}
	}
}

func TestFormattedRepeatedParametersFollowTemplateOrder(t *testing.T) {
	data := strings.Replace(string(english), "<b>Inventory</b> • <code>{{.owner}}</code>", "<code>{{.owner}}</code> • <b>Inventory</b> • <code>{{.owner}}</code>", 1)
	c, err := Parse([]byte(data), russian)
	if err != nil {
		t.Fatal(err)
	}

	content, err := c.Formatted(English, "inventory.title", Args{"owner": String("😀Игрок")})
	if err != nil {
		t.Fatal(err)
	}

	want := []TextSpan{{Kind: "code", Offset: 0, Length: 7}, {Kind: "bold", Offset: 10, Length: 9}, {Kind: "code", Offset: 22, Length: 7}}
	if content.Text != "😀Игрок • Inventory • 😀Игрок\n\n" || !reflect.DeepEqual(content.Spans, want) {
		t.Fatalf("template order or UTF-16 offsets lost: %+v", content)
	}
}

func TestFormattedStatsKeepRepeatedValuesSeparate(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	for _, locale := range []Locale{English, Russian} {
		content, err := c.Formatted(locale, "character.summary", Args{"health": Integer(100), "max": Integer(100), "attack": Integer(100), "defense": Integer(100), "state": String("active")})
		if err != nil {
			t.Fatal(err)
		}

		if len(content.Spans) != 3 {
			t.Fatalf("unexpected stat spans: %+v", content)
		}

		for i, want := range []string{"100/100", "100", "100"} {
			if content.Spans[i].Kind != "code" || spanText(t, content.Text, content.Spans[i]) != want {
				t.Fatalf("stat span %d: %+v", i, content)
			}
		}
	}
}

func TestCatalogRejectsMalformedFormatting(t *testing.T) {
	for _, invalid := range []string{"<b>private", "<u>private</u>", "<b>private</i>", "<b><code>private</code></b>", "</b>private", "<b private"} {
		t.Run(invalid, func(t *testing.T) {
			data := strings.Replace(string(english), "<b>Group settings</b>", invalid, 1)
			_, err := Parse([]byte(data), russian)
			if err == nil || strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), "settings.title") {
				t.Fatalf("invalid or unsafe formatting result: %v", err)
			}
		})
	}
}

func TestItemTextDoesNotInterpretMarkup(t *testing.T) {
	data := strings.Replace(string(english), "An ordinary collectible coin.", "<b>A literal coin & `text`</b>", 1)
	c, err := Parse([]byte(data), russian)
	if err != nil {
		t.Fatal(err)
	}

	item, err := c.Item(English, "coin")
	if err != nil || item.Description != "<b>A literal coin & `text`</b>" {
		t.Fatalf("item text interpreted markup: %+v %v", item, err)
	}
}
