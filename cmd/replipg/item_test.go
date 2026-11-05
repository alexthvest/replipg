package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/item"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type itemProbeScope struct{ runtimecontract.WorkScope }

func (itemProbeScope) DBTX() runtimecontract.DBTX { return nil }

func TestItemProbeLocalizedReadOnly(t *testing.T) {
	items, err := item.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(100, 0)
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		h := itemDevelopmentHandler{owner: 42, items: items, catalog: catalog, now: func() time.Time { return now }, resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
			return locale, nil
		}}
		for _, check := range []struct{ args, en, ru string }{
			{"stick", "Attack bonus: +1", "Бонус атаки: +1"}, {"cloth_armor", "Defense bonus: +1", "Бонус защиты: +1"}, {"healing_potion", "10", "10"}, {"coin", "Coin", "Монета"},
			{"iron_sword", "Attack bonus: +3", "Бонус атаки: +3"},
			{"chainmail", "Defense bonus: +3", "Бонус защиты: +3"},
			{"greater_healing_potion", "25", "25"},
			{"ruby", "Ruby", "Рубин"},
			{"", "Usage:", "Использование:"}, {"coin extra", "Usage:", "Использование:"}, {"../secret", "Usage:", "Использование:"}, {"unknown", "Unknown item", "Неизвестный предмет"},
		} {
			command := interaction.Command{Identity: interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}, Name: "dev_item", Arguments: check.args, EphemeralMessageID: 1, SentAt: now}
			result, err := h.Handle(context.Background(), itemProbeScope{}, &command)
			if err != nil || len(result.ImmediateResponses) != 1 {
				t.Fatalf("%s %q: %+v %v", locale, check.args, result, err)
			}

			response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
			want := check.en
			if locale == localization.Russian {
				want = check.ru
			}

			if !strings.Contains(response.Content.Text, want) {
				t.Fatalf("%q lacks %q", response.Content.Text, want)
			}

			if response.TargetUserID != 42 || len(response.Content.Buttons) != 0 {
				t.Fatal("wrong visibility or callbacks")
			}

			if err := runtimecontract.ValidateResponseAt(response, now); err != nil {
				t.Fatal(err)
			}

			for _, known := range []struct{ id, enCategory, ruCategory string }{
				{"stick", "weapon", "оружие"},
				{"cloth_armor", "armor", "броня"},
				{"healing_potion", "consumable", "расходуемый"},
				{"coin", "inert", "обычный предмет"},
			} {
				if check.args != known.id {
					continue
				}

				if len(response.Content.Entities) < 2 || response.Content.Entities[0].Kind != runtimecontract.TextEntityBold || response.Content.Entities[1].Kind != runtimecontract.TextEntityCode {
					t.Fatalf("item name or identifier lost formatting: %+v", response.Content)
				}

				category := known.enCategory
				if locale == localization.Russian {
					category = known.ruCategory
				}

				if !strings.Contains(response.Content.Text, "ID: "+known.id) || !strings.Contains(response.Content.Text, category) {
					t.Fatalf("missing stable ID or category: %q", response.Content.Text)
				}

				if known.id == "coin" && strings.Count(response.Content.Text, "\n") != 2 {
					t.Fatal("inert item gained mechanics")
				}
			}
		}
	}
}

func TestItemProbeRejectsBeforeLookup(t *testing.T) {
	now := time.Unix(100, 0)
	h := itemDevelopmentHandler{owner: 42, now: func() time.Time { return now }, resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		t.Fatal("rejected request resolved locale")
		return localization.English, nil
	}}
	valid := interaction.Command{Identity: interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "dev_item", Arguments: "coin", EphemeralMessageID: 1, SentAt: now}
	var nilCommand *interaction.Command
	events := []interaction.Event{nil, nilCommand, interaction.GroupMessage{}}
	for _, mutate := range []func(*interaction.Command){
		func(c *interaction.Command) { c.User = 7 }, func(c *interaction.Command) { c.ActorIsBot = true }, func(c *interaction.Command) { c.SenderIsChat = true }, func(c *interaction.Command) { c.Chat = 1 }, func(c *interaction.Command) { c.ChatKind = "private" }, func(c *interaction.Command) { c.EphemeralMessageID = 0 }, func(c *interaction.Command) { c.SentAt = now.Add(-15 * time.Second) }, func(c *interaction.Command) { c.Update = 0 }, func(c *interaction.Command) { c.Name = "inventory" },
	} {
		c := valid
		mutate(&c)
		events = append(events, c, &c)
	}

	for _, event := range events {
		result, err := h.Handle(context.Background(), nil, event)
		if err != nil || len(result.ImmediateResponses) != 0 {
			t.Fatalf("acted on invalid event: %+v %v", result, err)
		}
	}
}
