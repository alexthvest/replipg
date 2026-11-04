package inventory

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestRenderNumberedInventoryAndDetails(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := contract.Snapshot{Revision: 2, Items: []contract.OwnedItem{{ItemID: "coin", Quantity: 10}, {ItemID: "stick", Quantity: 1}}, Weapon: "stick", Modifiers: contract.Modifiers{Attack: 1}}
	controls := viewControls{Items: []string{"inv:aaaaaaaaaaaaaaaaaaaaaa", "inv:bbbbbbbbbbbbbbbbbbbbbb"}, Next: "inv:cccccccccccccccccccccc", Back: "inv:dddddddddddddddddddddd", Unequip: "inv:eeeeeeeeeeeeeeeeeeeeee"}
	content := renderPage(catalog, inventoryPage{Current: snapshot, HasNext: true}, "player", controls)
	for _, part := range []string{"Inventory • player", "1. Coin (10)", "2. Stick (1) • Equipped", "Weapon: Stick (+1 attack)", "Armor: Empty", "+1 attack", "+0 defense"} {
		if !strings.Contains(content.Text, part) {
			t.Fatalf("missing %q in %q", part, content.Text)
		}
	}
	if content.Buttons[0][0].Text != "1" || content.Buttons[0][1].Text != "2" || content.Buttons[0][1].CallbackData != controls.Items[1] {
		t.Fatal("number mapping changed")
	}
	assertRenderedResponse(t, content)
	for _, id := range []contract.ItemID{"coin", "healing_potion", "stick"} {
		snapshot.Items = append(snapshot.Items, contract.OwnedItem{ItemID: "healing_potion", Quantity: 3})
		detail, err := renderDetail(catalog, snapshot, id, "player", controls)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(detail.Text, "Owned:") || !strings.Contains(detail.Text, "• player") {
			t.Fatal("detail missing owner or count")
		}
		assertRenderedResponse(t, detail)
		for _, row := range detail.Buttons {
			for _, button := range row {
				if strings.Contains(button.Text, "Use") {
					t.Fatal("standalone use")
				}
				if id != "stick" && (button.Text == "Equip" || button.Text == "Unequip") {
					t.Fatal("non-equipment action")
				}
			}
		}
		if id == "healing_potion" && !strings.Contains(detail.Text, "Usable during battle") {
			t.Fatal("missing battle restriction")
		}
		if id != "stick" && (len(detail.Buttons) != 1 || len(detail.Buttons[0]) != 1 || detail.Buttons[0][0].Text != "Back" || detail.Buttons[0][0].CallbackData != controls.Back) {
			t.Fatal("read-only item must offer Back only")
		}
	}
}
func TestRenderOwnerAndResponseBounds(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"", "\n\x00\u202e", strings.Repeat("😀", 100), "alice\nbob"} {
		content := renderPage(catalog, inventoryPage{}, label, viewControls{})
		if !utf8.ValidString(content.Text) || strings.ContainsAny(content.Text, "\x00\u202e") {
			t.Fatal("unsafe label")
		}
		if label == "" && !strings.HasPrefix(content.Text, "Inventory • you") {
			t.Fatal("fallback")
		}
		if !strings.Contains(content.Text, "empty") {
			t.Fatal("no empty state")
		}
		assertRenderedResponse(t, content)
	}
	item, _ := catalog.Lookup("stick")
	item.Name = strings.Repeat("😀", 80)
	item.Description = strings.Repeat("😀", 400)
	catalog.byID["stick"] = item
	snapshot := contract.Snapshot{Items: []contract.OwnedItem{{ItemID: "stick", Quantity: 9223372036854775807}}, Weapon: "stick"}
	detail, err := renderDetail(catalog, snapshot, "stick", strings.Repeat("😀", 100), viewControls{Back: "inv:aaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	assertRenderedResponse(t, detail)
}
func assertRenderedResponse(t *testing.T, content runtimecontract.MessageContent) {
	t.Helper()
	if err := runtimecontract.ValidateResponse(runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: -1, Kind: runtimecontract.ChatKindGroup}, TargetUserID: 42, Authorization: runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: 1, ValidUntil: time.Now().Add(time.Second)}, Content: content}); err != nil {
		t.Fatal(err)
	}
}

func TestRenderEightRowsAndPageLocalControls(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for page := 0; page < 2; page++ {
		for _, id := range []contract.ItemID{"stick", "cloth_armor"} {
			item, _ := catalog.Lookup(id)
			item.Name = strings.Repeat("😀", 80)
			item.AttackBonus, item.DefenseBonus = math.MaxInt64, math.MaxInt64
			catalog.byID[id] = item
		}
		snapshot := contract.Snapshot{Weapon: "stick", Armor: "cloth_armor", Modifiers: contract.Modifiers{Attack: math.MaxInt64, Defense: math.MaxInt64}}
		controls := viewControls{}
		if page == 0 {
			controls.Next = "inv:nnnnnnnnnnnnnnnnnnnnnn"
		} else {
			controls.Previous = "inv:pppppppppppppppppppppp"
		}
		for i := 0; i < 8; i++ {
			id := contract.ItemID(fmt.Sprintf("item_%d", page*8+i))
			catalog.byID[id] = ItemDefinition{ID: id, Name: strings.Repeat("😀", 79) + string(rune('A'+page*8+i)), Description: strings.Repeat("😀", 400), Category: contract.CategoryInert}
			snapshot.Items = append(snapshot.Items, contract.OwnedItem{ItemID: id, Quantity: math.MaxInt64})
			controls.Items = append(controls.Items, fmt.Sprintf("inv:%022d", page*8+i))
		}
		content := renderPage(catalog, inventoryPage{Current: snapshot, Page: page, HasNext: page == 0}, strings.Repeat("😀", 64), controls)
		assertRenderedResponse(t, content)
		if len(content.Buttons) != 2 || len(content.Buttons[0]) != 8 {
			t.Fatalf("page %d buttons %+v", page, content.Buttons)
		}
		for i, button := range content.Buttons[0] {
			if button.Text != fmt.Sprint(i+1) || button.CallbackData != controls.Items[i] {
				t.Fatalf("page-local mapping %d %d", page, i)
			}
			item, _ := catalog.Lookup(snapshot.Items[i].ItemID)
			if !strings.Contains(content.Text, fmt.Sprintf("%d. %s (%d)", i+1, item.Name, int64(math.MaxInt64))) {
				t.Fatal("numbered text does not match item control")
			}
		}
		snapshot.Items = append(snapshot.Items, contract.OwnedItem{ItemID: "coin", Quantity: 999})
		controls.Items = append(controls.Items, "inv:xxxxxxxxxxxxxxxxxxxxxx")
		bounded := renderPage(catalog, inventoryPage{Current: snapshot, Page: page}, "you", controls)
		if len(bounded.Buttons[0]) != 8 || strings.Contains(bounded.Text, "9.") || strings.Contains(bounded.Text, "Coin (999)") {
			t.Fatal("overflowing supplied page was not bounded")
		}
		want := "Next"
		if page == 1 {
			want = "Previous"
		}
		wantToken := controls.Next
		if page == 1 {
			wantToken = controls.Previous
		}
		if content.Buttons[1][0].Text != want || content.Buttons[1][0].CallbackData != wantToken {
			t.Fatalf("page navigation %+v", content.Buttons)
		}
	}
}
func TestRenderEquipmentActionsAndMissingItems(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	controls := viewControls{Equip: "inv:eeeeeeeeeeeeeeeeeeeeee", Unequip: "inv:uuuuuuuuuuuuuuuuuuuuuu", Back: "inv:bbbbbbbbbbbbbbbbbbbbbb"}
	for _, id := range []contract.ItemID{"stick", "cloth_armor"} {
		for _, equipped := range []bool{false, true} {
			snapshot := contract.Snapshot{Items: []contract.OwnedItem{{ItemID: id, Quantity: 1}}}
			if equipped {
				if id == "stick" {
					snapshot.Weapon = id
				} else {
					snapshot.Armor = id
				}
			}
			content, err := renderDetail(catalog, snapshot, id, "", controls)
			if err != nil {
				t.Fatal(err)
			}
			want, wantToken := "Equip", controls.Equip
			if equipped {
				want, wantToken = "Unequip", controls.Unequip
			}
			if content.Buttons[0][0].Text != want || content.Buttons[0][0].CallbackData != wantToken || content.Buttons[1][0].Text != "Back" {
				t.Fatalf("incorrect actions %+v", content.Buttons)
			}
			bonus := "Attack bonus: +1"
			if id == "cloth_armor" {
				bonus = "Defense bonus: +1"
			}
			if !strings.Contains(content.Text, bonus) {
				t.Fatalf("missing bonus %q", content.Text)
			}
			assertRenderedResponse(t, content)
		}
	}
	if _, err := renderDetail(catalog, contract.Snapshot{}, "missing", "", controls); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("missing item %v", err)
	}
	if _, err := renderDetail(catalog, contract.Snapshot{}, "coin", "", controls); !errors.Is(err, contract.ErrNotOwned) {
		t.Fatalf("unowned item %v", err)
	}
}
