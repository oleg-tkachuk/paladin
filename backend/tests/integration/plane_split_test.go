//go:build integration

package integration

// The data and IAM planes as the cluster runs them: two roles, two processes
// (ADR-0047). AssembleAPIMuxes co-hosts them, and there the data plane
// verifies with the key the IAM plane loaded in memory — so the tests built on
// it never exercise the path production takes, where `serve api` holds no key
// and verifies by the JWKS `serve iam` publishes.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	"github.com/oleg-tkachuk/paladin/backend/tests/sessiontest"
	pbdata "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// splitCallTimeout bounds one RPC, a JWKS fetch included.
const splitCallTimeout = 20 * time.Second

// splitDeps builds the shared dependencies for one role's process, with the
// parts that role builds.
func splitDeps(t *testing.T, cfg config.Config, role config.Role) *app.SharedDeps {
	t.Helper()
	ctx := context.Background()
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, zap.NewNop())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	deps, err := app.BuildSharedDeps(ctx, cfg, db, zap.NewNop(), role.Builds())
	if err != nil {
		t.Fatalf("BuildSharedDeps: %v", err)
	}
	// Before db.Close: cleanups run last-in-first-out (see app_wiring_test.go).
	t.Cleanup(deps.StopWatchers)
	return deps
}

// iamRole serves the IAM plane alone, as `serve iam` does, and returns its
// config and URL.
func iamRole(t *testing.T, h *pgharness.Harness) (config.Config, string) {
	t.Helper()
	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.Auth.OAuth.Enabled = true
	mux, _, err := app.AssembleIAMMux(context.Background(), splitDeps(t, cfg, config.RoleIAM), app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleIAMMux: %v", err)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return cfg, srv.URL
}

// dataRoleConfig is `serve api`'s config: no session key, the IAM role's JWKS.
func dataRoleConfig(t *testing.T, h *pgharness.Harness, jwksURL string) config.Config {
	t.Helper()
	cfg := wiringConfig(t, h.MigrateDSN)
	cfg.Auth.SessionKeyPath = ""
	cfg.Auth.JWKSURL = jwksURL
	return cfg
}

func listObjectsWith(t *testing.T, dataURL, token, tenant string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), splitCallTimeout)
	defer cancel()
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	client := paladindatav1connect.NewObjectServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, dataURL)))
	_, err := client.ListObjects(ctx, &pbdata.ListObjectsRequest{Parent: "tenants/" + tenant + "/collections/docs"})
	return err
}

func mintData(t *testing.T, cfg config.Config, keys *sessionkey.Set, tenant uuid.UUID) string {
	t.Helper()
	iss, err := issuer.New(issuer.Config{Issuer: cfg.Auth.Issuer, Keys: keys, AccessTokenTTL: 15 * time.Minute})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "split@test", TenantID: tenant, Audience: auth.AudienceData,
		Roles: []string{"platform.admin"}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return token
}

// A session the IAM role mints is accepted by a data role that holds no key,
// through the JWKS alone; one signed by any other key is not.
func TestPlaneSplit_DataVerifiesByTheIAMRolesJWKS(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "plane-split")
	mustCreateCollection(t, h.PoolMigrate, tenant, "docs")

	iamCfg, iamURL := iamRole(t, h)
	dataMux, _, err := app.AssembleDataMux(context.Background(), splitDeps(t, dataRoleConfig(t, h, iamURL+sessionkey.WellKnownPath), config.RoleAPI))
	if err != nil {
		t.Fatalf("AssembleDataMux: %v", err)
	}
	data := httptest.NewServer(dataMux)
	t.Cleanup(data.Close)

	if err := listObjectsWith(t, data.URL, mintData(t, iamCfg, wiringKeys(t, iamCfg), tenant), tenant.String()); err != nil {
		t.Fatalf("a session the iam role minted, on the data role: %v", err)
	}

	otherPath, _ := sessiontest.KeyFile(t)
	other, err := sessionkey.Load(otherPath, nil)
	if err != nil {
		t.Fatalf("other key: %v", err)
	}
	err = listObjectsWith(t, data.URL, mintData(t, iamCfg, other, tenant), tenant.String())
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("a session signed by a key the iam role does not publish: %v, want unauthenticated", err)
	}
}

// Without the key and without a JWKS the data role cannot verify anyone, and
// says so at startup rather than refusing every caller.
func TestPlaneSplit_DataRefusesToStartWithoutAKeySource(t *testing.T) {
	h := pgharness.Setup(t)
	_, _, err := app.AssembleDataMux(context.Background(), splitDeps(t, dataRoleConfig(t, h, ""), config.RoleAPI))
	if err == nil || !strings.Contains(err.Error(), "auth.jwks_url") {
		t.Fatalf("AssembleDataMux with no key source: %v, want an error naming auth.jwks_url", err)
	}
}

// Each role serves its own plane and nothing of the other's: login, OAuth
// and the JWKS are the iam role's, objects the api role's.
func TestPlaneSplit_EachRoleMountsOnlyItsPlane(t *testing.T) {
	h := pgharness.Setup(t)
	ctx := context.Background()

	iamCfg := wiringConfig(t, h.MigrateDSN)
	iamCfg.Auth.OAuth.Enabled = true
	iamMux, iamHealth, err := app.AssembleIAMMux(ctx, splitDeps(t, iamCfg, config.RoleIAM), app.BuildMeta{Version: "test"})
	if err != nil {
		t.Fatalf("AssembleIAMMux: %v", err)
	}
	dataCfg := dataRoleConfig(t, h, "http://127.0.0.1:1"+sessionkey.WellKnownPath)
	dataCfg.Auth.OAuth.Enabled = true
	dataMux, _, err := app.AssembleDataMux(ctx, splitDeps(t, dataCfg, config.RoleAPI))
	if err != nil {
		t.Fatalf("AssembleDataMux: %v", err)
	}

	iamOnly := []string{sessionkey.WellKnownPath, "/oauth/token", oauth.WellKnownAuthorizationServer}
	for _, svc := range servicesInProtoPackage(t, "paladin.iam.v1") {
		iamOnly = append(iamOnly, "/"+svc+"/AnyMethod")
	}
	for _, path := range iamOnly {
		if routed(t, dataMux, path) {
			t.Errorf("%s is served by the api role; it is the iam role's", path)
		}
	}
	for _, svc := range servicesInProtoPackage(t, "paladin.data.v1") {
		if routed(t, iamMux, "/"+svc+"/AnyMethod") {
			t.Errorf("%s is served by the iam role; it is the api role's", svc)
		}
	}
	if iamHealth == nil {
		t.Fatal("AssembleIAMMux returned no health handler")
	}
	if got := snapshotRole(t, iamMux); got != string(config.RoleIAM) {
		t.Errorf("the iam role's health snapshot names role %q, want %q", got, config.RoleIAM)
	}
}

// snapshotRole is the role a mux's /system/health.json names.
func snapshotRole(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/system/health.json", nil))
	var snap struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("health snapshot (%d): %v", rec.Code, errors.Join(err, errors.New(rec.Body.String())))
	}
	return snap.Role
}
