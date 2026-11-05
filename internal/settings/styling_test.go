package settings

import (
	"context"
	"testing"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestSettingsTitleAndLanguageStyles(t *testing.T) {
	db, store, catalog, now := fixture(t)
	h := handler{store: store, catalog: catalog}
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		for _, admin := range []bool{true, false} {
			err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				view, err := store.Inspect(ctx, tx, -1, now)
				if err != nil {
					return err
				}

				view.Locale = locale
				content, err := h.render(ctx, tx, view, 7, admin, "", now)
				if err != nil {
					return err
				}

				if len(content.Entities) != 1 || content.Entities[0].Kind != runtimecontract.TextEntityBold || content.Entities[0].Offset != 0 {
					t.Fatalf("missing settings title: %+v", content)
				}

				if !admin {
					if len(content.Buttons) != 0 {
						t.Fatal("non-admin gained controls")
					}

					return nil
				}

				for i, language := range []localization.Locale{localization.English, localization.Russian} {
					want := runtimecontract.ButtonStyle("")
					if language != locale {
						want = runtimecontract.ButtonPrimary
					}

					if content.Buttons[i][0].Style != want {
						t.Fatalf("language %s: %+v", language, content.Buttons)
					}
				}

				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
