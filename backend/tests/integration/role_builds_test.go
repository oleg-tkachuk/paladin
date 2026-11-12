//go:build integration

package integration

// Each role builds the shared dependencies it uses and no more
// (config.Role.Builds, ADR-0047): the token keys reach the roles that verify
// tokens, the storage credentials the roles that move objects. Booted here
// against a real database, as each role's process does.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

// roleBuildsEnv is an environment that refuses a generated signing key or
// DPoP nonce key, so a role that builds token auth needs both configured.
const roleBuildsEnv = "prod"

// purgeInterval turns the token and capability purgers on.
const purgeInterval = time.Minute

// buildAs builds the shared dependencies over cfg as role does.
func buildAs(t *testing.T, cfg config.Config, role config.Role) (*app.SharedDeps, error) {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop(), role.Builds())
	if deps != nil {
		// Before db.Close: cleanups run last-in-first-out.
		t.Cleanup(deps.StopWatchers)
	}
	return deps, err
}

// tokensWithoutKeys is a config with API tokens, capabilities and DPoP
// nonces on, in an environment that refuses generated keys, with none of the
// keys present: no pepper, no nonce key, and a signing key path no file is
// at.
func tokensWithoutKeys(t *testing.T, h *pgharness.Harness) config.Config {
	t.Helper()
	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.App.Env = roleBuildsEnv
	cfg.APIToken.Enabled = true
	cfg.APIToken.HMACKey = ""
	cfg.Capability.Enabled = true
	cfg.Capability.IssuerName = "role-builds"
	// A path the worker's pod does not mount: the shared config names it
	// for every role, and only the roles that sign load it.
	cfg.Capability.SigningKeyPath = filepath.Join(t.TempDir(), "not-mounted.pem")
	cfg.Capability.DPoPNonce.Enabled = true
	cfg.Capability.DPoPNonce.Key = ""
	cfg.Worker.Jobs.Capability.Interval = purgeInterval
	cfg.Worker.Jobs.APIToken.Interval = purgeInterval
	return cfg
}

// The worker purges API tokens and capabilities over their stores, with none
// of the keys that verify them; the data plane, given the same config,
// refuses to start.
func TestRoleBuilds_TheWorkerPurgesWithoutTokenKeys(t *testing.T) {
	h := pgharness.Setup(t)
	cfg := tokensWithoutKeys(t, h)

	deps, err := buildAs(t, cfg, config.RoleWorker)
	if err != nil {
		t.Fatalf("the worker needed a token key: %v", err)
	}
	if deps.Capability == nil || deps.Capability.Auth != nil || deps.APIToken == nil || deps.APIToken.Auth != nil {
		t.Fatalf("the worker's bundles: capability %+v, api token %+v; want stores without auth",
			deps.Capability, deps.APIToken)
	}
	deps.ReaperPool = deps.Pool
	names := make([]string, 0)
	for _, j := range app.BuildBackgroundJobs(deps) {
		names = append(names, fmt.Sprintf("%T", j))
	}
	for _, want := range []string{"*worker.CapabilityPurger", "*worker.APITokenPurger"} {
		if !slices.Contains(names, want) {
			t.Errorf("the worker built no %s; built %v", want, names)
		}
	}

	if _, err := buildAs(t, cfg, config.RoleAPI); err == nil {
		t.Error("the data plane started without the token keys")
	}
}

// The IAM plane builds no storage client: a backend no client can be built
// for does not stop it, its mux assembles over the registry it holds, and it
// reports ready.
func TestRoleBuilds_IAMHoldsNoStorage(t *testing.T) {
	h := pgharness.Setup(t)
	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.Storage.Backends = map[string]config.StorageBackend{"primary": {Kind: "no-such-kind"}}

	deps, err := buildAs(t, cfg, config.RoleIAM)
	if err != nil {
		t.Fatalf("iam built storage: %v", err)
	}
	_, healthH, err := app.AssembleIAMMux(context.Background(), deps, app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleIAMMux without storage: %v", err)
	}
	mux := http.NewServeMux()
	healthH.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, health.ReadyPath, nil))
	if rec.Code != http.StatusOK {
		t.Errorf("iam without storage: %s = %d %s, want 200", health.ReadyPath, rec.Code, rec.Body)
	}

	if _, err := buildAs(t, cfg, config.RoleAPI); err == nil {
		t.Error("the data plane started over a backend no client can be built for")
	}
}
