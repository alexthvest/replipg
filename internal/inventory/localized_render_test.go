package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/item"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func loadLocalizedSeedCatalog() (fixtureCatalog, error) {
	catalog, err := LoadCatalog()
	if err != nil {
		return fixtureCatalog{}, err
	}

	seed, err := item.LoadCatalog()
	if err != nil {
		return fixtureCatalog{}, err
	}

	catalog.items = seed.Items()
	for _, definition := range catalog.items {
		catalog.byID[definition.ID] = definition
	}

	return catalog, nil
}

func TestLocalizedInventoryPresentation(t *testing.T) {
	catalog, err := loadLocalizedSeedCatalog()
	if err != nil {
		t.Fatal(err)
	}

	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	ids := []string{}
	for _, item := range catalog.Items() {
		ids = append(ids, string(item.ID))
	}

	if err := c.ValidateItems(ids, ValidateItemText); err != nil {
		t.Fatal(err)
	}

	before := catalog.Items()
	p := Presentation{Catalog: c}
	s := contract.Snapshot{Items: []contract.OwnedItem{{ItemID: "coin", Quantity: 10}, {ItemID: "stick", Quantity: 1}, {ItemID: "cloth_armor", Quantity: 1}, {ItemID: "healing_potion", Quantity: 3}}, Weapon: "stick", Modifiers: contract.Modifiers{Attack: 1}}
	controls := viewControls{Items: []string{"inv:aaaaaaaaaaaaaaaaaaaaaa"}, Next: "inv:nnnnnnnnnnnnnnnnnnnnnn", Equip: "inv:eeeeeeeeeeeeeeeeeeeeee", Unequip: "inv:uuuuuuuuuuuuuuuuuuuuuu", Back: "inv:bbbbbbbbbbbbbbbbbbbbbb"}
	for _, l := range []localization.Locale{localization.English, localization.Russian} {
		page, err := renderPageLocalized(catalog, inventoryPage{Current: s}, "alice\n", controls, p, l)
		if err != nil {
			t.Fatal(err)
		}

		assertRenderedResponse(t, page)
		want := "Inventory • alice"
		next := "Next"
		if l == localization.Russian {
			want = "Инвентарь • alice"
			next = "Далее"
		}

		if !strings.Contains(page.Text, want) || page.Buttons[1][0].Text != next {
			t.Fatal(page)
		}

		for _, item := range s.Items {
			detail, err := renderDetailLocalized(catalog, s, item.ItemID, "", controls, p, l)
			if err != nil {
				t.Fatal(err)
			}

			assertRenderedResponse(t, detail)
			translation, err := c.Item(l, string(item.ItemID))
			if err != nil || !strings.Contains(detail.Text, translation.Name) || !strings.Contains(detail.Text, translation.Description) {
				t.Fatal("translated item absent")
			}

			if item.ItemID == "cloth_armor" {
				name := "Leather armor"
				if l == localization.Russian {
					name = "Кожаная броня"
				}

				if !strings.Contains(detail.Text, name) {
					t.Fatal(detail.Text)
				}
			}
		}
	}

	after := catalog.Items()
	for i := range before {
		if before[i] != after[i] {
			t.Fatal("presentation mutated mechanics")
		}
	}
}

func TestInventoryOldControlUsesCurrentLocale(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	locale := localization.English
	calls := 0
	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		calls++
		return locale, nil
	}}
	h := inventoryHandler{service: service, now: clock.Now, presentation: p}
	cmd := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, Name: "inventory", EphemeralMessageID: 1, SentAt: clock.Now()}
	var token string

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		result, err := h.Handle(ctx, inventoryScope{tx}, cmd)
		if err != nil {
			return err
		}

		token = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons[0][0].CallbackData
		return nil
	})
	locale = localization.Russian
	callback := interaction.Callback{Identity: cmd.Identity, QueryID: "query", Data: token, EphemeralMessageID: 1}
	callback.Update = 2
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := h.Handle(ctx, inventoryScope{tx}, callback)
		if err != nil {
			return err
		}

		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !strings.Contains(response.Content.Text, "Кожаная броня") || !strings.Contains(response.Content.Text, "Категория:") || response.Content.Buttons[len(response.Content.Buttons)-1][0].Text != "Назад" || response.EditMessageID != 1 {
			t.Fatal("old controls did not render current locale", response.Content)
		}

		return nil
	})

	if calls != 2 {
		t.Fatal("locale must resolve once per accepted interaction")
	}
}

func TestLocalizedOwnerPreservesSanitizedPlayerName(t *testing.T) {
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ input, want string }{{"you", "you"}, {"\x00you", "you"}, {"\u200byou", "you"}, {"\n", "вы"}, {"", "вы"}, {"Имя_42", "Имя_42"}} {
		r := localizedRenderer{catalog: c, locale: localization.Russian}
		got := r.owner(tc.input)
		if got != tc.want || r.err != nil {
			t.Errorf("owner = %q %v want %q", got, r.err, tc.want)
		}
	}
}

func TestLocalizedInventoryDevelopmentMessages(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.Russian, nil
	}}
	h := developmentHandler{service: service, owner: 42, now: clock.Now, presentation: p}
	cmd := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, Name: "dev_inventory_grant", EphemeralMessageID: 1, SentAt: clock.Now()}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		_, err := service.Open(ctx, tx, 42)
		return err
	})
	for _, tc := range []struct{ args, want string }{{"coin -1", "Использование:"}, {"coin 2", "Награда добавлена"}} {
		cmd.Arguments = tc.args
		inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			result, err := h.Handle(ctx, inventoryScope{tx}, cmd)
			if err != nil {
				return err
			}

			text := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Text

			if !strings.Contains(text, tc.want) {
				t.Fatal(text)
			}

			return nil
		})
		cmd.Update++
	}
}

func TestRejectedInventoryControlDoesNotResolveLocale(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		calls++
		return localization.English, nil
	}}
	h := inventoryHandler{service: service, now: clock.Now, presentation: p}
	event := interaction.Callback{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "query", Data: "inv:aaaaaaaaaaaaaaaaaaaaaa", EphemeralMessageID: 1}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := h.Handle(ctx, inventoryScope{tx}, event)

		if len(result.ImmediateResponses) != 0 {
			t.Fatal("unknown control responded")
		}

		return err
	})

	if calls != 0 {
		t.Fatal("unknown control created locale state")
	}
}

func TestLocalizedMaximumPageAndDetailBudgets(t *testing.T) {
	catalog, err := loadLocalizedSeedCatalog()
	if err != nil {
		t.Fatal(err)
	}

	resources := map[string][]byte{}
	for _, locale := range []string{"en", "ru"} {
		data, err := os.ReadFile("../localization/locales/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}

		values := map[string]string{}
		if err := json.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}

		for _, item := range catalog.Items() {
			values["items."+string(item.ID)+".name"] = strings.Repeat("😀", 80)
			values["items."+string(item.ID)+".description"] = strings.Repeat("😀", 400)
		}

		resources[locale], err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
	}

	c, err := localization.Parse(resources["en"], resources["ru"])
	if err != nil {
		t.Fatal(err)
	}

	ids := []string{}
	for _, item := range catalog.Items() {
		ids = append(ids, string(item.ID))
	}

	if err := c.ValidateItems(ids, ValidateItemText); err != nil {
		t.Fatal(err)
	}

	snapshot := contract.Snapshot{Weapon: "stick", Armor: "cloth_armor", Modifiers: contract.Modifiers{Attack: math.MaxInt64, Defense: math.MaxInt64}}
	controls := viewControls{Previous: "inv:pppppppppppppppppppppp", Next: "inv:nnnnnnnnnnnnnnnnnnnnnn", Back: "inv:bbbbbbbbbbbbbbbbbbbbbb", Unequip: "inv:uuuuuuuuuuuuuuuuuuuuuu"}
	for i := 0; i < 8; i++ {
		snapshot.Items = append(snapshot.Items, contract.OwnedItem{ItemID: itemcontract.ItemID(ids[i%len(ids)]), Quantity: math.MaxInt64})
		controls.Items = append(controls.Items, fmt.Sprintf("inv:%022d", i))
	}

	for _, id := range []itemcontract.ItemID{"stick", "cloth_armor"} {
		item, _ := catalog.Lookup(id)
		if id == "stick" {
			item.AttackBonus = math.MaxInt64
		} else {
			item.DefenseBonus = math.MaxInt64
		}
		catalog.byID[id] = item
	}

	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		p := Presentation{Catalog: c}
		content, err := renderPageLocalized(catalog, inventoryPage{Current: snapshot}, strings.Repeat("😀", 64), controls, p, locale)
		if err != nil {
			t.Fatal(err)
		}

		assertRenderedResponse(t, content)

		if len(content.Buttons) != 2 || len(content.Buttons[0]) != 8 || !strings.Contains(content.Text, "9223372036854775807") {
			t.Fatal("maximum page lost rows, controls or integer values")
		}

		for _, id := range []itemcontract.ItemID{"stick", "cloth_armor", "healing_potion"} {
			detail, err := renderDetailLocalized(catalog, snapshot, id, strings.Repeat("😀", 64), controls, p, locale)
			if err != nil {
				t.Fatal(err)
			}

			assertRenderedResponse(t, detail)

			if !strings.Contains(detail.Text, strings.Repeat("😀", 400)) {
				t.Fatal("maximum description was altered")
			}
		}
	}
}

func TestPreparedInventoryResponseKeepsAcceptedLanguageSnapshot(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	locale := localization.English
	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return locale, nil
	}}
	h := inventoryHandler{service: service, now: clock.Now, presentation: p}
	cmd := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup, ActorUsername: "Player_Name"}, Name: "inventory", EphemeralMessageID: 1, SentAt: clock.Now()}
	var token string

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		result, err := h.Handle(ctx, inventoryScope{tx}, cmd)
		if err != nil {
			return err
		}

		page := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !strings.Contains(page.Content.Text, "Coin (10)") {
			t.Fatal(page.Content.Text)
		}

		token = page.Content.Buttons[0][0].CallbackData
		return nil
	})
	callback := interaction.Callback{Identity: cmd.Identity, QueryID: "query", Data: token, EphemeralMessageID: 1}
	callback.Update = 2
	var accepted runtimecontract.EphemeralResponse

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := h.Handle(ctx, inventoryScope{tx}, callback)
		if err != nil {
			return err
		}

		accepted = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
		return nil
	})
	before, err := json.Marshal(accepted)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(accepted.Content.Text, "Leather armor • Player_Name") || !strings.Contains(accepted.Content.Text, "Owned: (1)") || !strings.Contains(accepted.Content.Text, "Defense bonus: +1") || accepted.Content.Buttons[0][0].Text != "Equip" || accepted.Content.Buttons[1][0].Text != "Back" {
		t.Fatal("English accepted content incomplete", accepted.Content)
	}

	locale = localization.Russian
	callback.Update++
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := h.Handle(ctx, inventoryScope{tx}, callback)
		if err != nil {
			return err
		}

		next := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !strings.Contains(next.Content.Text, "Кожаная броня • Player_Name") || !strings.Contains(next.Content.Text, "В наличии: (1)") || next.Content.Buttons[0][0].Text != "Экипировать" {
			t.Fatal("new interaction did not use selected language", next.Content)
		}

		return nil
	})
	after, err := json.Marshal(accepted)
	if err != nil {
		t.Fatal(err)
	}

	if string(before) != string(after) {
		t.Fatal("previously prepared response text, values, buttons or authorization changed with locale")
	}
}
