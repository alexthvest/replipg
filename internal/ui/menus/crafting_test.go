package menus

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

func TestCraftQuantityChoices(t *testing.T) {
	for _, tt := range []struct {
		current   int64
		available int64
		want      []int64
	}{
		{current: 1, available: 25, want: []int64{1, 1, 1, 2, 11, 25}},
		{current: 15, available: 25, want: []int64{1, 5, 14, 16, 25, 25}},
		{current: math.MaxInt64, available: math.MaxInt64, want: []int64{1, math.MaxInt64 - 10, math.MaxInt64 - 1, math.MaxInt64, math.MaxInt64, math.MaxInt64}},
		{current: 100, available: 7, want: []int64{1, 1, 6, 7, 7, 7}},
	} {
		choices := CraftQuantityChoices(tt.current, tt.available, "Макс.")
		var captions []string
		var quantities []int64
		for _, choice := range choices {
			captions = append(captions, choice.Caption)
			quantities = append(quantities, choice.Quantity)
		}
		if !reflect.DeepEqual(captions, []string{"1", "-10", "-1", "+1", "+10", "Макс."}) || !reflect.DeepEqual(quantities, tt.want) {
			t.Fatalf("choices: %+v, want %v", choices, tt.want)
		}
	}
	if len(CraftQuantityChoices(1, 0, "Max")) != 0 {
		t.Fatal("unavailable material has quantity controls")
	}
}

func TestCraftSelectionFreezesExactAccumulatedIntent(t *testing.T) {
	var selection CraftSelection
	for i := range 20 {
		if err := selection.Set(core.ID(fmt.Sprint(i)), int64(i+1), 25, false); err != nil {
			t.Fatal(err)
		}
	}
	frozen := selection.Freeze("confirmation")
	frozen[0].Quantity = 999
	if picks, err := selection.Frozen("confirmation"); err != nil || len(picks) != 20 || picks[0].Quantity != 1 {
		t.Fatalf("frozen selection mutated: %+v %v", picks, err)
	}
	if _, err := selection.Frozen("foreign"); err == nil {
		t.Fatal("foreign confirmation accepted")
	}
	if err := selection.Set("0", 2, 25, false); err != nil {
		t.Fatal(err)
	}
	if _, err := selection.Frozen("confirmation"); err == nil {
		t.Fatal("edited confirmation accepted")
	}
	selection.Remove("0")
	if selection.Quantity("0") != 0 || selection.Quantity("1") != 2 || len(selection.Picks()) != 19 {
		t.Fatal("remove affected other selections")
	}
	if err := selection.Set("instance", 2, 2, true); err == nil {
		t.Fatal("individual quantity accepted")
	}
	if err := selection.Set("instance", 1, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := selection.Set("reserved", 1, 0, false); err == nil {
		t.Fatal("reserved material accepted")
	}
	picks := selection.Picks()
	picks[0].Quantity = 999
	if selection.Quantity("1") != 2 {
		t.Fatal("selection alias escaped")
	}
}

func TestCraftSessionScopeAndAcceptedConfirmation(t *testing.T) {
	now := time.Unix(1, 0)
	sessions, err := New(time.Minute, 10, func() time.Time {
		return now
	}, func() (string, error) {
		return "craft", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{
		Group: -1,
		Actor: 2,
	}
	session, err := sessions.Create("craft", scope, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Bind(session.Token, scope, 7); err != nil {
		t.Fatal(err)
	}
	refs, err := sessions.Actions(session, []Action{{
		Kind:  "confirm",
		Quote: "frozen",
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []core.Scope{{
		Group: -2,
		Actor: 2,
	}, {
		Group: -1,
		Actor: 3,
	}} {
		if _, _, err = sessions.Resolve(refs[0], foreign, 7); err == nil {
			t.Fatal("foreign callback accepted")
		}
	}
	if _, _, err = sessions.Resolve(refs[0], scope, 8); err == nil {
		t.Fatal("foreign message accepted")
	}
	sessions.AcceptConfirmation(session, "frozen")
	if _, err = sessions.Actions(session, []Action{{Kind: "refresh"}}); err != nil {
		t.Fatal(err)
	}
	if _, action, err := sessions.Resolve(refs[0], scope, 7); err != nil || action.Quote != "frozen" {
		t.Fatal("accepted confirmation did not retain its exact token")
	}
	now = now.Add(2 * time.Minute)
	if _, _, err = sessions.Resolve(refs[0], scope, 7); err == nil {
		t.Fatal("expired callback accepted")
	}
}

func TestCraftDraftCommitBackAndClear(t *testing.T) {
	var selection CraftSelection
	selection.Page = 2
	selection.MaterialPage = 3
	selection.Edit("new", "materials")
	selection.Draft.Quantity = 11
	if len(selection.Picks()) != 0 || selection.Draft.Origin != "materials" {
		t.Fatal("opening or stepping a draft changed composition")
	}
	selection.Draft = nil
	if len(selection.Picks()) != 0 || selection.MaterialPage != 3 {
		t.Fatal("abandoning draft lost origin page")
	}
	for i := range 8 {
		selection.Edit(core.ID(fmt.Sprint(i)), "materials")
		selection.Draft.Quantity = int64(i + 1)
		if err := selection.Save(20, false); err != nil {
			t.Fatal(err)
		}
	}
	selection.Freeze("old")
	selection.Edit("0", "composition")
	selection.Draft.Quantity = 19
	if selection.Quantity("0") != 1 {
		t.Fatal("editing changed saved amount")
	}
	if _, err := selection.Frozen("old"); err != nil {
		t.Fatal("draft invalidated unchanged intent")
	}
	if err := selection.Save(10, false); err == nil || selection.Draft.Quantity != 19 {
		t.Fatal("shortage silently reduced draft")
	}
	selection.Draft.Quantity = 2
	if err := selection.Save(10, false); err != nil {
		t.Fatal(err)
	}
	if _, err := selection.Frozen("old"); err == nil {
		t.Fatal("committed change kept old confirmation")
	}
	selection.Remove("0")
	if len(selection.Picks()) != 7 || selection.Quantity("1") != 2 {
		t.Fatal("removal changed other ingredients")
	}
	selection.Edit("instance", "materials")
	selection.Draft.Quantity = 2
	if err := selection.Save(2, true); err == nil {
		t.Fatal("individual draft accepted two instances")
	}
	selection.Clear()
	if len(selection.Picks()) != 0 || selection.Draft != nil || selection.Page != 0 || selection.MaterialPage != 0 || selection.Confirmation() != "" {
		t.Fatal("clear retained state")
	}
}
