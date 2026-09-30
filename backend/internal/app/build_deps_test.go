package app

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
)

// BuildSharedDeps refuses three ways before it touches the database, and each
// refusal is the only thing standing between a misconfiguration and a pod that
// comes up serving. They are the boot contract this file's comments claim —
// "fail-fast before any listener binds", "a boot misconfiguration fails
// loudly" — and none of them was exercised.

// notAPgxPool satisfies postgres.PgxPool without being *pgxpool.Pool, which is
// the case the first guard exists for. Every method panics: reaching one means
// the guard let a pool through that the rest of the function cannot use.
type notAPgxPool struct{}

func (notAPgxPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("Exec on a stub pool")
}
func (notAPgxPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("Query on a stub pool")
}
func (notAPgxPool) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("QueryRow on a stub pool")
}
func (notAPgxPool) Begin(context.Context) (pgx.Tx, error) { panic("Begin on a stub pool") }
func (notAPgxPool) Ping(context.Context) error            { panic("Ping on a stub pool") }
func (notAPgxPool) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	panic("SendBatch on a stub pool")
}
func (notAPgxPool) Close()                  { panic("Close on a stub pool") }
func (notAPgxPool) Stat() *pgxpool.Stat     { panic("Stat on a stub pool") }
func (notAPgxPool) Config() *pgxpool.Config { panic("Config on a stub pool") }

func TestBuildSharedDeps_RefusesBeforeTouchingTheDatabase(t *testing.T) {
	// A single backend that builds cleanly, so the storage guard fires only
	// in the case that removes it.
	goodStorage := config.Storage{
		Backends: map[string]config.StorageBackend{
			"primary": {
				Kind: "s3-compatible", Endpoint: "http://127.0.0.1:1", Region: "us-east-1",
				Auth: config.StorageBackendAuth{Mode: "static_keys", AccessKey: "u", SecretKey: "u"},
			},
		},
	}

	cases := map[string]struct {
		db      *postgres.DB
		cfg     config.Config
		wantErr string
	}{
		"pool of the wrong type": {
			db:  &postgres.DB{Pool: notAPgxPool{}},
			cfg: config.Config{Auth: config.Auth{SigningKey: "k"}, Storage: goodStorage},
			// Everything downstream type-asserts this pool; letting it past
			// here turns a boot error into a panic on the first request.
			wantErr: "not *pgxpool.Pool",
		},
		"no signing key": {
			db: &postgres.DB{Pool: &pgxpool.Pool{}},
			// Without it nothing can mint or verify a token, so every
			// authenticated call would fail — after the listeners bound and
			// the pod reported ready.
			cfg:     config.Config{Storage: goodStorage},
			wantErr: "auth.signing_key",
		},
		"no storage backends": {
			db: &postgres.DB{Pool: &pgxpool.Pool{}},
			// The registry has no default backend, so an empty map is not a
			// smaller deployment — it is one where every object write resolves
			// to nothing.
			cfg:     config.Config{Auth: config.Auth{SigningKey: "k"}},
			wantErr: "storage backend",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			deps, err := BuildSharedDeps(context.Background(), tc.cfg, tc.db, zap.NewNop())
			if err == nil {
				t.Fatalf("BuildSharedDeps returned no error; deps != nil: %v", deps != nil)
			}
			if deps != nil {
				t.Errorf("BuildSharedDeps returned deps alongside the error: %+v", deps)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q — whoever hits this is mid-deploy",
					err, tc.wantErr)
			}
		})
	}
}

// The watcher registry is the mechanism the fix above depends on, and every
// shutdown path is required to go through it. Its contract is three claims in
// two doc comments — nil-safe, idempotent, releases everything — and each one
// is load-bearing: a shutdown path that panics or that cancels only some
// watchers leaves pgxpool.Close blocked exactly as a missing call would.
func TestSharedDeps_WatcherRegistry(t *testing.T) {
	t.Run("nil-safe on both sides", func(t *testing.T) {
		// Called at construction time, before there is anything to register
		// on. A panic here fails a boot that was about to succeed.
		var nilDeps *SharedDeps
		nilDeps.RegisterWatcherStop(func() {})
		nilDeps.StopWatchers()

		deps := &SharedDeps{}
		deps.RegisterWatcherStop(nil)
		deps.StopWatchers() // a nil cancel must not have been stored
	})

	t.Run("cancels every registered watcher", func(t *testing.T) {
		deps := &SharedDeps{}
		calls := make([]int, 0, 3)
		for i := range 3 {
			deps.RegisterWatcherStop(func() { calls = append(calls, i) })
		}
		deps.StopWatchers()
		if len(calls) != 3 {
			t.Fatalf("cancelled %d of 3 watchers %v — the ones missed still hold a connection", len(calls), calls)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		deps := &SharedDeps{}
		var n int
		deps.RegisterWatcherStop(func() { n++ })

		deps.StopWatchers()
		deps.StopWatchers()
		// Shutdown paths overlap — App.Shutdown and a test cleanup can both
		// reach here — and a context cancel called twice is harmless, but a
		// second pass over a list that was never cleared would be a slow leak
		// of whatever a future cancel does.
		if n != 1 {
			t.Errorf("cancel ran %d times across two StopWatchers calls, want 1", n)
		}
	})
}
