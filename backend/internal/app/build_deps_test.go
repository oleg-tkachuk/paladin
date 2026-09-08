package app

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
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
