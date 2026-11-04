package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestPreparationPrecedesAdmissionAndRollsBack(t *testing.T) {
	for _, mode := range []string{"success", "rejection", "timeout", "cancel", "commit"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.HandlerTimeout = 100 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			prepared, registered, listened := false, false, false
			var acquired *persistence.DB

			runtime, err := New(cfg, Options{
				Open: func(ctx context.Context, opts persistence.Options) (*persistence.DB, error) {
					var err error

					acquired, err = persistence.Open(ctx, opts)
					return acquired, err
				},
				Prepare: func(prepCtx context.Context, tx runtimecontract.DBTX) error {
					prepared = true
					var count int

					if err := tx.QueryRowContext(prepCtx, `SELECT count(*) FROM characters`).Scan(&count); err != nil {
						return err
					}

					if _, err := tx.ExecContext(prepCtx, `CREATE TABLE prepare_probe(value INTEGER)`); err != nil {
						return err
					}

					switch mode {
					case "rejection":
						return errors.New("private-preparation")
					case "timeout":
						<-prepCtx.Done()
						return prepCtx.Err()
					case "cancel":
						cancel()
						<-prepCtx.Done()
						return prepCtx.Err()
					case "commit":
						// A deferred foreign key violation fails the outer commit.
						if _, err := tx.ExecContext(prepCtx, `CREATE TABLE prepare_child(id TEXT REFERENCES characters(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
							return err
						}

						_, err := tx.ExecContext(prepCtx, `INSERT INTO prepare_child VALUES('absent')`)
						return err
					}
					return nil
				},
				Listen: func(string, string) (net.Listener, error) {
					listened = true
					return nil, errors.New("stop after preparation")
				},
				Register: func(Registrations) error { registered = true; return nil },
			})
			if err != nil {
				t.Fatal(err)
			}

			err = runtime.Run(ctx)
			var failure *StartupError

			wantStage := "feature_preparation"
			if mode == "success" {
				wantStage = "health_listener"
			}
			if !errors.As(err, &failure) || failure.Stage != wantStage {
				t.Fatalf("error %v, want %s", err, wantStage)
			}

			if !prepared || registered || listened != (mode == "success") {
				t.Fatalf("order prepared=%v registered=%v listened=%v", prepared, registered, listened)
			}

			select {
			case <-runtime.Ready():
				t.Fatal("became ready")
			default:
			}

			if err := acquired.QueryRowContext(context.Background(), `SELECT 1`).Scan(new(int)); err == nil {
				t.Fatal("storage not closed")
			}

			db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: cfg.StorageDir, StorageFilesystem: cfg.StorageFilesystem, BusyTimeout: cfg.BusyTimeout})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()

			var count int

			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM sqlite_schema WHERE name='prepare_probe'`).Scan(&count); err != nil {
				t.Fatal(err)
			}

			want := 0
			if mode == "success" {
				want = 1
			}

			if count != want {
				t.Fatalf("committed probe=%d want %d", count, want)
			}
		})
	}
}
