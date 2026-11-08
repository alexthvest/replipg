package localization

import (
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
)

func translations() map[string]map[string]string {
	result := map[string]map[string]string{
		"en": {},
		"ru": {},
	}

	for _, lang := range []string{"en", "ru"} {
		for _, key := range append(append([]string{}, baselineKeys...), inventoryKeys...) {
			result[lang][key] = "text"
		}

		result[lang]["command.admin.updated"] = "Updated {user}."
		result[lang]["command.items.given"] = "Added {item}."
		result[lang]["command.give.success"] = "Granted {quantity} {item} to {recipient}."
	}
	return result
}

func TestValidationAndTemplateLiterals(t *testing.T) {
	values := translations()
	catalog, err := New(values)
	if err != nil {
		t.Fatal(err)
	}

	values["en"]["command.ping.message"] = "changed"
	if catalog.Text("en", "command.ping.message") != "text" {
		t.Fatal("mutable catalog escaped")
	}

	spans, err := catalog.Render("ru", "command.admin.updated", map[string]string{"user": "🎲`[name]"})
	if err != nil || len(spans) != 3 || spans[1].Text != "🎲`[name]" || spans[1].Style != core.Code {
		t.Fatalf("literal rendering: %+v %v", spans, err)
	}

	for _, mutate := range []func(map[string]map[string]string){
		func(v map[string]map[string]string) {
			delete(v["ru"], "command.ping.message")
		},
		func(v map[string]map[string]string) {
			v["en"]["command.admin.updated"] = "{unknown}"
		},
		func(v map[string]map[string]string) {
			v["ru"]["command.admin.updated"] = "no parameter"
		},
		func(v map[string]map[string]string) {
			v["en"]["language.en"] = "   "
		},
		func(v map[string]map[string]string) {
			v["en"]["language.en"] = strings.Repeat("x", 1025)
		},
		func(v map[string]map[string]string) {
			delete(v, "ru")
		},
	} {
		candidate := translations()
		mutate(candidate)
		if _, err = New(candidate); err == nil {
			t.Fatal("invalid translations accepted")
		}
	}
}
