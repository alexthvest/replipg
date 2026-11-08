package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/application/grants"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/yohamta/donburi"
)

type blockedUnit struct {
	db       *sqlite.Database
	prepared chan struct{}
	release  chan struct{}
}

func (u blockedUnit) WithinGrant(ctx context.Context, fn func(grants.Transaction) error) error {
	return u.db.WithinGrant(ctx, func(tx grants.Transaction) error {
		if err := fn(tx); err != nil {
			return err
		}

		close(u.prepared)
		select {
		case <-u.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

func TestShutdownDuringGrantCommitsOrRollsBack(t *testing.T) {
	for _, commit := range []bool{true, false} {
		name := "rollback"
		if commit {
			name = "commit"
		}

		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "shutdown.db"))
			if err != nil {
				t.Fatal(err)
			}

			defer func() {
				_ = db.Close()
			}()

			bundle, err := cueassets.Load("../../assets")
			if err != nil {
				t.Fatal(err)
			}

			permissions, err := groups.New(db, 1)
			if err != nil {
				t.Fatal(err)
			}

			barrier := blockedUnit{
				db:       db,
				prepared: make(chan struct{}),
				release:  make(chan struct{}),
			}
			service, err := grants.New(barrier, permissions, core.NewID)
			if err != nil {
				t.Fatal(err)
			}

			registry := NewRegistry()
			bound, err := Bind(registry, "give", func(
				ctx context.Context,
				_ donburi.World,
				scope core.Scope,
				recipient core.User,
			) (core.View, error) {
				_, err := service.Give(ctx, grants.Request{
					Scope:     scope,
					Recipient: &recipient,
					ItemID:    "coin",
					Quantity:  5,
				}, bundle)
				if err != nil {
					return core.View{}, err
				}
				return core.View{Text: core.Literal("committed")}, nil
			})
			if err != nil {
				t.Fatal(err)
			}

			engine, err := New(registry, 2, time.Minute)
			if err != nil {
				t.Fatal(err)
			}

			if err = engine.Start(ctx); err != nil {
				t.Fatal(err)
			}

			completed := make(chan result, 1)
			go func() {
				view, err := bound(ctx, engine, core.Scope{
					Group:    -1,
					Actor:    1,
					BotID:    123,
					UpdateID: 1,
				}, core.User{
					ID:        2,
					FirstName: "Player",
				})
				completed <- result{
					view: view,
					err:  err,
				}
			}()
			<-barrier.prepared
			drain, cancel := context.WithCancel(ctx)
			defer cancel()

			if !commit {
				cancel()
			}

			stopped := make(chan error, 1)
			go func() {
				stopped <- engine.Stop(drain)
			}()
			<-engine.stopping
			if commit {
				close(barrier.release)
			}

			outcome := <-completed
			stopErr := <-stopped
			c, exists, err := db.FindCharacter(ctx, -1, 2)
			if err != nil {
				t.Fatal(err)
			}

			if commit {
				if outcome.err != nil || stopErr != nil || !exists || len(outcome.view.Text) == 0 {
					t.Fatalf("committed shutdown: %+v %v %v", outcome, stopErr, exists)
				}

				entries, err := db.Entries(ctx, -1, c.ID)
				if err != nil || len(entries) != 1 || entries[0].Quantity != 5 {
					t.Fatal("committed grant incomplete")
				}
			} else {
				if outcome.err == nil || stopErr == nil || exists || len(outcome.view.Text) != 0 {
					t.Fatalf("rollback shutdown: %+v %v %v", outcome, stopErr, exists)
				}
			}
		})
	}
}
