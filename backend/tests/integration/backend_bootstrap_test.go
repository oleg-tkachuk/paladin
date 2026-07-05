//go:build integration

// US5 / FR-012 / SC-005: an operator-set disable must survive a platform
// restart. The startup config-mirror (EnsureBackends) reconciles static
// YAML into the DB but MUST NOT touch the operator-managed `enabled`
// column — neither the convergence-skip path nor the upsert path may
// re-enable a backend the operator disabled.
package integration

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/bootstrap"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/tests/integration/pgharness"
)

func TestBootstrapPreservesDisabledState(t *testing.T) {
	h := pgharness.Setup(t)
	q := sqlc.New(h.PoolMigrate)
	be := adapters.NewBackendRepoV2(q, h.PoolMigrate)
	ctx := context.Background()

	cfg := config.Storage{
		Backends: map[string]config.StorageBackend{
			"primary": {Kind: "s3-compatible", Endpoint: "http://localhost", Region: "us-east-1"},
		},
	}
	deps := bootstrap.BackendDeps{Backends: be}

	// First boot: mirror creates the row (enabled by default).
	if err := bootstrap.EnsureBackends(ctx, cfg, deps); err != nil {
		t.Fatalf("first EnsureBackends: %v", err)
	}

	// Operator disables it.
	cur, err := be.Get(ctx, "primary")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := be.SetEnabled(ctx, "primary", false, cur.ResourceVersion); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled, _ := be.Get(ctx, "primary")
	rvAfterDisable := disabled.ResourceVersion

	// Simulate a restart: run EnsureBackends again with the SAME config.
	// The convergence-skip path must leave the row untouched.
	if err := bootstrap.EnsureBackends(ctx, cfg, deps); err != nil {
		t.Fatalf("second EnsureBackends (converged): %v", err)
	}
	got, _ := be.Get(ctx, "primary")
	if got.Enabled {
		t.Error("converged restart re-enabled a disabled backend")
	}
	if got.ResourceVersion != rvAfterDisable {
		t.Errorf("converged restart bumped resource_version: %d → %d", rvAfterDisable, got.ResourceVersion)
	}

	// Now change a config field so the upsert path runs (not just skip),
	// and confirm the upsert STILL preserves enabled.
	cfg.Backends["primary"] = config.StorageBackend{
		Kind: "s3-compatible", Endpoint: "http://changed:9000", Region: "us-east-1",
	}
	if err := bootstrap.EnsureBackends(ctx, cfg, deps); err != nil {
		t.Fatalf("third EnsureBackends (upsert): %v", err)
	}
	got, _ = be.Get(ctx, "primary")
	if got.Enabled {
		t.Error("upsert path re-enabled a disabled backend")
	}
	if got.Endpoint != "http://changed:9000" {
		t.Errorf("upsert did not apply config change: endpoint=%q", got.Endpoint)
	}
}
