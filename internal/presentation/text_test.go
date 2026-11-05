package presentation

import (
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestBuilderShiftsEntitiesWithoutMutatingFragments(t *testing.T) {
	fragment := runtimecontract.MessageContent{Text: "Игрок", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 0, Length: 5}}}
	var b Builder

	b.Write("😀\n")
	b.Append(fragment)
	b.Write(" ")
	b.Styled("(1)", runtimecontract.TextEntityCode)
	b.Styled("", runtimecontract.TextEntityItalic)
	content := b.Content()
	want := []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 3, Length: 5}, {Kind: runtimecontract.TextEntityCode, Offset: 9, Length: 3}}

	if content.Text != "😀\nИгрок (1)" || !reflect.DeepEqual(content.Entities, want) || fragment.Entities[0].Offset != 0 {
		t.Fatalf("invalid assembly: %+v; original %+v", content, fragment)
	}

	if err := runtimecontract.ValidateResponse(runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -1, Kind: runtimecontract.ChatKindSupergroup}, Content: content}); err != nil {
		t.Fatal(err)
	}
}
