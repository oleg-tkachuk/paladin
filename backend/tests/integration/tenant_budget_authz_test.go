//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/genproto/googleapis/type/money"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// budgetCallTimeout bounds every call the budget test makes through the mux.
const budgetCallTimeout = 20 * time.Second

// budgetTokenTTL is how long a token the budget test mints lives.
const budgetTokenTTL = 15 * time.Minute

// budgetCeilingUnits is the ceiling the platform admin sets, in whole units.
const budgetCeilingUnits = 100

// firstBudgetVersion is the resource_version a Set sends for a tenant with no
// budget yet, as the console does.
const firstBudgetVersion = "0"

// budgetPlane is the admin plane as `serve admin` builds it, with the
// capability subsystem on, and an issuer minting its sessions.
type budgetPlane struct {
	client paladinadminv1connect.TenantBudgetServiceClient
	iss    *issuer.Issuer
}

func newBudgetPlane(t *testing.T, h *pgharness.Harness) budgetPlane {
	t.Helper()
	mux, keys := assembleAdminMux(t, h, func(c *config.Config) {
		c.Capability.Enabled = true
		c.Capability.IssuerName = "budget-authz"
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := wiringConfig(t, h.MigrateDSN)
	iss, err := issuer.New(issuer.Config{Issuer: cfg.Auth.Issuer, Keys: keys, AccessTokenTTL: budgetTokenTTL})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return budgetPlane{
		client: paladinadminv1connect.NewTenantBudgetServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL))),
		iss:    iss,
	}
}

// as is a context calling as a user of tenant holding role.
func (p budgetPlane) as(t *testing.T, tenant uuid.UUID, role string) context.Context {
	t.Helper()
	token, _, err := p.iss.MintAccess(issuer.AccessClaims{
		Subject: role + "@budget", TenantID: tenant, Audience: auth.AudienceAdmin,
		Roles: []string{role}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budgetCallTimeout)
	t.Cleanup(cancel)
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	info.RequestHeader().Set(paladin.HeaderIdempotencyKey, uuid.New().String())
	return ctx
}

func wantCode(t *testing.T, what string, err error, want connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); err == nil || got != want {
		t.Errorf("%s: err = %v, want %s", what, err, want)
	}
}

func wantOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v", what, err)
	}
}

// Through the admin plane as it is assembled: the platform admin sets and
// reads any tenant's budget; a tenant admin reads its own tenant's and nothing
// else; a role with no budget grant reads none.
func TestTenantBudgetService_AuthorizesThroughTheAdminPlane(t *testing.T) {
	h := pgharness.Setup(t)
	own := mustCreateTenant(t, h.PoolMigrate, "budget-own")
	other := mustCreateTenant(t, h.PoolMigrate, "budget-other")
	plane := newBudgetPlane(t, h)
	set := func(ctx context.Context, tenant uuid.UUID) error {
		_, err := plane.client.Set(ctx, &adminv1.TenantBudgetServiceSetRequest{
			TenantId:        tenant.String(),
			ResourceVersion: firstBudgetVersion,
			BudgetCeiling: &adminv1.TenantBudgetServiceSetRequest_MaxBudget{
				MaxBudget: &money.Money{CurrencyCode: "USD", Units: budgetCeilingUnits},
			},
		})
		return err
	}
	get := func(ctx context.Context, tenant uuid.UUID) error {
		_, err := plane.client.Get(ctx, &adminv1.TenantBudgetServiceGetRequest{TenantId: tenant.String()})
		return err
	}
	holds := func(ctx context.Context, tenant uuid.UUID) error {
		_, err := plane.client.ListReservations(ctx, &adminv1.TenantBudgetServiceListReservationsRequest{TenantId: tenant.String()})
		return err
	}
	summarize := func(ctx context.Context) error {
		_, err := plane.client.Summarize(ctx, &adminv1.TenantBudgetServiceSummarizeRequest{})
		return err
	}

	root := uuid.New()
	wantOK(t, "platform admin sets another tenant's budget", set(plane.as(t, root, apiutil.RolePlatformAdmin), own))
	wantOK(t, "platform admin reads it", get(plane.as(t, root, apiutil.RolePlatformAdmin), own))
	wantOK(t, "platform admin lists its holds", holds(plane.as(t, root, apiutil.RolePlatformAdmin), own))
	wantOK(t, "platform admin summarizes", summarize(plane.as(t, root, apiutil.RolePlatformAdmin)))

	wantOK(t, "tenant admin reads its own budget", get(plane.as(t, own, apiutil.RoleTenantAdmin), own))
	wantOK(t, "tenant admin lists its own holds", holds(plane.as(t, own, apiutil.RoleTenantAdmin), own))
	wantCode(t, "tenant admin sets its own budget", set(plane.as(t, own, apiutil.RoleTenantAdmin), own), connect.CodePermissionDenied)
	wantCode(t, "tenant admin reads another tenant's", get(plane.as(t, own, apiutil.RoleTenantAdmin), other), connect.CodePermissionDenied)
	wantCode(t, "tenant admin lists another tenant's holds", holds(plane.as(t, own, apiutil.RoleTenantAdmin), other), connect.CodePermissionDenied)
	wantCode(t, "tenant admin summarizes", summarize(plane.as(t, own, apiutil.RoleTenantAdmin)), connect.CodePermissionDenied)

	for _, role := range []string{apiutil.RoleBucketAdmin, apiutil.RoleIAMAdmin, apiutil.RoleTenantProvisioner, apiutil.RoleCapabilityIssuer} {
		wantCode(t, role+" reads a budget", get(plane.as(t, own, role), own), connect.CodePermissionDenied)
		wantCode(t, role+" sets a budget", set(plane.as(t, own, role), own), connect.CodePermissionDenied)
	}
}
