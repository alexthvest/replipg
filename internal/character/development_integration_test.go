package character_test

import (
	"context"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type developmentScope struct{ tx runtimecontract.DBTX }

func (s developmentScope) DBTX() runtimecontract.DBTX { return s.tx }
func (developmentScope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	panic("unexpected durable delivery")
}
func (developmentScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	panic("unexpected scheduling")
}
func TestDevelopmentCommandReplayCannotRepeatHealthEffects(t *testing.T) {
	db := openCharacterDB(t, t.TempDir())
	clock := testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	tuning, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(tuning, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	presentation := character.Presentation{Catalog: catalog, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.English, nil
	}}
	routes := interaction.NewRegistry()
	if err := character.RegisterDevelopment(routes, service, clock.Now, presentation); err != nil {
		t.Fatal(err)
	}
	routes.Seal()
	invoke := func(id runtimecontract.UpdateID, name interaction.CommandName, args string) contract.Snapshot {
		t.Helper()
		var snapshot contract.Snapshot
		inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			command := interaction.Command{Identity: interaction.Identity{Update: id, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}, Name: name, Arguments: args, SentAt: clock.Now(), EphemeralMessageID: 23}
			handler, ok := routes.Resolve(command)
			if !ok {
				t.Fatal("missing probe")
			}
			result, err := handler.Handle(ctx, developmentScope{tx}, command)
			if err != nil {
				return err
			}
			if len(result.ImmediateResponses) != 1 {
				t.Fatal("missing restricted reply")
			}
			response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
			if !ok || response.TargetUserID != 7 {
				t.Fatal("wrong recipient")
			}
			snapshot, _, err = service.Lookup(ctx, tx, 7)
			return err
		})
		return snapshot
	}
	first := invoke(1, "dev_character_damage", "10")
	again := invoke(1, "dev_character_damage", "10")
	if first.Health != 90 || again != first {
		t.Fatal("duplicate damage reapplied")
	}
	defeated := invoke(2, "dev_character_defeat", "")
	again = invoke(2, "dev_character_defeat", "")
	if defeated.State != contract.StateIncapacitated || again != defeated {
		t.Fatal("duplicate defeat extended deadline")
	}
	blocked := invoke(3, "dev_character_damage", "1")
	if blocked != defeated {
		t.Fatal("pending probe bypassed recovery")
	}
	clock.Advance(5 * time.Second)
	recovered := invoke(2, "dev_character_defeat", "")
	if recovered.Health != 25 || recovered.State != contract.StateActive {
		t.Fatal("old defeat applied after recovery")
	}
	replayed := invoke(1, "dev_character_damage", "10")
	if replayed != recovered {
		t.Fatal("old damage reapplied after recovery")
	}
	var receipts int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM character_operations").Scan(&receipts); err != nil || receipts != 2 {
		t.Fatalf("receipt count=%d err=%v", receipts, err)
	}
}
