//go:build integration

package integration

// Does the app, assembled the way production assembles it, actually emit
// events?
//
// Every lifecycle handler takes its event producer through an OPTIONAL,
// nil-safe setter. That is the right shape — a role that emits nothing should
// not have to wire one — and it means a broken wiring line is invisible.
// Measured: removing `objH.SetEventProducer(apiDispatcher)` leaves `go build`
// clean, every unit test green, and every OTHER integration test in this
// repository green, while Paladin silently stops telling any subscriber that
// objects were uploaded, deleted or purged.
//
// internal/app/wiring_test.go catches a MISSING line statically. It cannot
// catch a wrong one: `SetEventProducer(nil)` passes it and breaks everything
// just as thoroughly. That is what this test is for, and both mutations are
// verified against it.
//
// The other lifecycle tests cannot substitute. They wire a producer onto a
// handler they construct themselves, so they prove the mechanism works once
// connected, never that the app connects it.
//
// Two pieces of setup are load-bearing and were both learned the hard way:
//
//   - deps.StopWatchers() must run before the pool closes. The policy
//     engine's LISTEN loop holds a pooled connection on its own background
//     context, so without it db.Close deadlocks — and the deadlock happens
//     during cleanup, AFTER the assertion, so a failing request looks like a
//     hung test rather than a failed one. That misreading cost an afternoon
//     and a wrong BACKLOG entry.
//   - the token is minted with the issuer from the config the app was built
//     with. Minting against any other is refused by the same verifier
//     production uses, which is correct and says so plainly.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pbdata "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
)

const wiringSigningKey = "wiring-test-signing-key-not-a-secret-000000000000"

func wiringConfig(t *testing.T, dsn string) config.Config {
	t.Helper()
	cfg, err := config.Load([]string{"../../configs/config.yaml"}, zap.NewNop())
	if err != nil {
		t.Fatalf("load the shipped config: %v", err)
	}
	cfg.Datastores.Postgres.DSN = dsn
	cfg.Datastores.Postgres.MigrateDSN = dsn
	cfg.Datastores.Postgres.ReaperDSN = dsn
	cfg.Auth.SigningKey = wiringSigningKey
	cfg.Storage.Backends = map[string]config.StorageBackend{
		"primary": {
			Kind: "s3-compatible", Endpoint: "http://127.0.0.1:1", Region: "us-east-1",
			Auth: config.StorageBackendAuth{Mode: "static_keys", AccessKey: "u", SecretKey: "u"},
		},
	}
	return cfg
}

func TestApp_AssembledMuxesEmitLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	h := pgharness.Setup(t)
	f := &dispatcherFixture{h: h}

	tenant := mustCreateTenant(t, h.PoolMigrate, "app-wiring")
	mustCreateCollection(t, h.PoolMigrate, tenant, "docs")
	objID := mustInsertAvailableObject(t, h.PoolMigrate, tenant, "docs", "wired.txt")
	_ = f.seedSubscription(t, tenant, subOpts{URL: "http://127.0.0.1:1/never"})

	cfg := wiringConfig(t, h.MigrateDSN)
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)

	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop())
	if err != nil {
		t.Fatalf("BuildSharedDeps: %v", err)
	}
	// The policy engine's LISTEN watcher runs on its own background context
	// and holds a pooled connection; without StopWatchers, db.Close blocks
	// forever and the deadlock masks whatever the request did. Registered
	// after db.Close so it runs BEFORE it — cleanups are last-in-first-out.
	t.Cleanup(deps.StopWatchers)
	dataMux, _, _, err := app.AssembleAPIMuxes(ctx, deps, app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleAPIMuxes: %v", err)
	}
	srv := httptest.NewServer(dataMux)
	t.Cleanup(srv.Close)

	// Issuer and key both come from the config the app was built with —
	// minting against anything else is refused, correctly, by the same
	// verifier production uses.
	iss, err := issuer.New(issuer.Config{
		Issuer: cfg.Auth.Issuer, SigningKey: []byte(wiringSigningKey),
		AccessTokenTTL: 15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "wiring@test", TenantID: tenant, Audience: auth.AudienceData,
		Roles: []string{"platform.admin"}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	client := paladindatav1connect.NewObjectServiceClient(http.DefaultClient, srv.URL)
	req := connect.NewRequest(&pbdata.UpdateObjectRequest{
		Name:            "tenants/" + tenant.String() + "/collections/docs/objects/" + objID.String(),
		ResourceVersion: "1",
		UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
		Tags:            map[string]string{"env": "wiring"},
	})
	req.Header().Set("Authorization", "Bearer "+token)

	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := client.UpdateObject(callCtx, req); err != nil {
		t.Fatalf("UpdateObject through the assembled mux: %v", err)
	}

	if rows := f.allDeliveryRows(t, tenant); len(rows) != 1 {
		t.Fatalf("event_deliveries rows = %d, want 1", len(rows))
	}
}
