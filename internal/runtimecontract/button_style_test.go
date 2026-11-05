package runtimecontract

import "testing"

func TestButtonStyles(t *testing.T) {
	for _, style := range []ButtonStyle{"", ButtonPrimary, ButtonSuccess, ButtonDanger, "purple"} {
		t.Run(string(style), func(t *testing.T) {
			response := PublicResponse{Destination: GroupDestination{ChatID: -1, Kind: ChatKindGroup}, Content: MessageContent{Text: "Label", Buttons: [][]InlineButton{{{Text: "Action", CallbackData: "action", Style: style}}}}}
			err := ValidateResponse(response)
			if (err != nil) != (style == "purple") {
				t.Fatalf("style %q: %v", style, err)
			}
		})
	}
}
