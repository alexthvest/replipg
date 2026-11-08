package telegram

import (
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

func TestShopStylesPreserveTelegramEntitiesAndControls(t *testing.T) {
	text, spans, err := entities(core.Text{
		{Text: "😀 "},
		{
			Text:  "<literal description>",
			Style: core.Italic,
		},
	})
	if err != nil || text != "😀 <literal description>" || len(spans) != 1 ||
		spans[0].Type != models.MessageEntityTypeItalic || spans[0].Offset != 3 || spans[0].Length != 21 {
		t.Fatalf("italic Unicode entity: %q %+v %v", text, spans, err)
	}

	markup, err := keyboard([][]core.Button{{
		{
			Caption: "Buy",
			Data:    "m:session:buy",
			Style:   core.ButtonPrimary,
		},
		{
			Caption: "Confirm",
			Data:    "m:session:confirm",
			Style:   core.ButtonSuccess,
		},
		{
			Caption: "Back",
			Data:    "m:session:back",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	row := markup.InlineKeyboard[0]
	if row[0].Style != "primary" || row[1].Style != "success" || row[2].Style != "" || row[1].CallbackData != "m:session:confirm" {
		t.Fatalf("button styles or callbacks changed: %+v", row)
	}

	if _, err = keyboard([][]core.Button{{{
		Caption: "Invalid",
		Data:    "m:session:invalid",
		Style:   "unknown",
	}}}); err == nil {
		t.Fatal("unknown button style accepted")
	}
}
