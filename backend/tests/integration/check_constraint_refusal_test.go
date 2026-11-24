//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/oleg-tkachuk/limes"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
	"github.com/oleg-tkachuk/paladin/backend/tests/integration/pgharness"
	pbadmin "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
	pbiam "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Every write RPC a client can carry a value to a CHECK constraint through
// answers a value the CHECK refuses as the caller's mistake — InvalidArgument,
// or the FailedPrecondition every public-collection rule is answered with — in
// words that name nothing of the server: never the Internal a check_violation
// reaching the adapters' fallback would be. Each case names the constraint it
// aims at.
//
// The CHECKs no request field reaches are not here: object_locks'
// (retention_needs_mode: SetObjectRetention writes a mode and a time, the
// legal-hold write neither), refresh_tokens' (oauth_grant_whole: the adapter
// writes a grant whole or not at all), storage_backend_features' (support:
// the prober writes its own constants), and the limes counters the metering
// path derives (depth, spent, refunded, reserved, request counts, bump refs).

// checkCallTimeout bounds each call of the CHECK refusal tests.
const checkCallTimeout = 20 * time.Second

// checkTokenTTL is how long a token the CHECK refusal tests mint lives.
const checkTokenTTL = 15 * time.Minute

// checkIssuerName names the capability issuer the admin plane is built with.
const checkIssuerName = "check-refusal"

// checkUnitCode is the currency the budget case names.
const checkUnitCode = "USD"

// Column limits the CHECKs hold rows to, one past which each case sends.
const (
	tenantDisplayNameMaxChars = 255
	localeMaxChars            = 35
)

// localeSubtag is a well-formed BCP-47 subtag the too-long locale repeats.
const localeSubtag = "-abcdefgh"

// retiredTheme is a theme an earlier user_settings_theme_check allowed and the
// current one refuses.
const retiredTheme = "ember"

// checkRefusal is one write a CHECK refuses: the constraint it aims at, and
// the call.
type checkRefusal struct {
	constraint string
	call       func(context.Context) error
}

// wantCheckRefusal fails t unless each case is answered with want, in words
// fuzzassert accepts.
func wantCheckRefusal(t *testing.T, ctx func() context.Context, want connect.Code, cases []checkRefusal) {
	t.Helper()
	for _, c := range cases {
		err := c.call(ctx())
		if got := connect.CodeOf(err); err == nil || got != want {
			t.Errorf("%s: err = %v (code %v), want %v", c.constraint, err, got, want)
			continue
		}
		t.Logf("%s: %v", c.constraint, err)
		if verr := fuzzassert.Check(err, []connect.Code{want}); verr != nil {
			t.Errorf("%s: %v", c.constraint, verr)
		}
	}
}

// checkAdminPlane is the admin plane as `serve admin` builds it, every
// subsystem on, and a platform admin's token for it.
type checkAdminPlane struct {
	conn  *connect.Client
	token string
}

func newCheckAdminPlane(t *testing.T, h *pgharness.Harness, tenant uuid.UUID) checkAdminPlane {
	t.Helper()
	mux, keys := assembleAdminMux(t, h, func(c *config.Config) {
		c.Capability.Enabled = true
		c.Capability.IssuerName = checkIssuerName
		c.APIToken.Enabled = true
		c.APIToken.HMACKey = wiringAPITokenPepper
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	iss, err := issuer.New(issuer.Config{Issuer: wiringConfig(t, h.MigrateDSN).Auth.Issuer, Keys: keys, AccessTokenTTL: checkTokenTTL})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: "check@test", TenantID: tenant, Audience: auth.AudienceAdmin,
		Roles: []string{apiutil.RolePlatformAdmin}, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return checkAdminPlane{conn: connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL)), token: token}
}

// ctx is a context calling as the platform admin, with an idempotency key of
// its own.
func (p checkAdminPlane) ctx(t *testing.T) func() context.Context {
	t.Helper()
	return func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), checkCallTimeout)
		t.Cleanup(cancel)
		ctx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer "+p.token)
		info.RequestHeader().Set(paladin.HeaderIdempotencyKey, uuid.New().String())
		return ctx
	}
}

func TestCheckRefusal_TenantService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-tenant")
	plane := newCheckAdminPlane(t, h, tenant)
	ctx := plane.ctx(t)
	client := paladinadminv1connect.NewTenantServiceClient(plane.conn)
	current, err := client.GetTenant(ctx(), &pbadmin.GetTenantRequest{Name: "tenants/" + tenant.String()})
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	create := func(slug, displayName string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.CreateTenant(ctx, &pbadmin.CreateTenantRequest{
				Tenant: &pbadmin.Tenant{Slug: slug, DisplayName: displayName},
			})
			return err
		}
	}
	rename := func(displayName string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.UpdateTenant(ctx, &pbadmin.UpdateTenantRequest{
				Name:            current.GetName(),
				ResourceVersion: current.GetResourceVersion(),
				UpdateMask:      &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
				Tenant:          &pbadmin.Tenant{DisplayName: displayName},
			})
			return err
		}
	}
	tooLong := strings.Repeat("d", tenantDisplayNameMaxChars+1)
	wantCheckRefusal(t, ctx, connect.CodeInvalidArgument, []checkRefusal{
		{"CreateTenant tenants_slug_format (too short)", create("ab", "")},
		{"CreateTenant tenants_slug_format (upper case)", create("Acme", "")},
		{"CreateTenant tenants_slug_format (trailing hyphen)", create("acme-", "")},
		{"CreateTenant tenants_display_name_format (too long)", create("check-long", tooLong)},
		{"UpdateTenant tenants_display_name_format (empty)", rename("")},
		{"UpdateTenant tenants_display_name_format (blank)", rename(" ")},
		{"UpdateTenant tenants_display_name_format (too long)", rename(tooLong)},
	})
}

func TestCheckRefusal_CollectionService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-collection")
	const existing = "docs"
	mustCreateCollection(t, h.PoolMigrate, tenant, existing)
	plane := newCheckAdminPlane(t, h, tenant)
	ctx := plane.ctx(t)
	client := paladinadminv1connect.NewCollectionServiceClient(plane.conn)
	parent := "tenants/" + tenant.String()
	current, err := client.GetCollection(ctx(), &pbadmin.GetCollectionRequest{Name: parent + "/collections/" + existing})
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	// Bound to the bucket the existing collection is, so a create reaches
	// the name and cache_control rather than stopping at a missing binding.
	create := func(name, cacheControl string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.CreateCollection(ctx, &pbadmin.CreateCollectionRequest{
				Parent: parent, Collection: name,
				CollectionResource: &pbadmin.Collection{Bucket: current.GetBucket(), CacheControl: cacheControl},
			})
			return err
		}
	}
	update := func(ctx context.Context) error {
		_, err := client.UpdateCollection(ctx, &pbadmin.UpdateCollectionRequest{
			Name: current.GetName(), ResourceVersion: current.GetResourceVersion(),
			UpdateMask:         &fieldmaskpb.FieldMask{Paths: []string{"cache_control"}},
			CollectionResource: &pbadmin.Collection{CacheControl: "max-age=60"},
		})
		return err
	}
	wantCheckRefusal(t, ctx, connect.CodeInvalidArgument, []checkRefusal{
		{"CreateCollection collections_name_format (upper case)", create("Bad_Name", "")},
		{"CreateCollection collections_name_format (empty segment)", create("a//b", "")},
		{"CreateCollection collections_name_format (trailing slash)", create("a/", "")},
		{"UpdateCollection collections_cache_control_public_only (not an updatable path)", update},
	})
	// A public-collection rule, answered as every one of them is
	// (publicread.ErrRule), with the PUBLIC_COLLECTION_RULE reason.
	wantCheckRefusal(t, ctx, connect.CodeFailedPrecondition, []checkRefusal{
		{"CreateCollection collections_cache_control_public_only", create("cached", "max-age=60")},
	})
}

func TestCheckRefusal_APITokenService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-token")
	plane := newCheckAdminPlane(t, h, tenant)
	client := paladinadminv1connect.NewAPITokenServiceClient(plane.conn)
	wantCheckRefusal(t, plane.ctx(t), connect.CodeInvalidArgument, []checkRefusal{
		{"Create api_tokens_rate_limit_rpm_check", func(ctx context.Context) error {
			_, err := client.Create(ctx, &pbadmin.APITokenServiceCreateRequest{
				Parent: "tenants/" + tenant.String(), DisplayName: "check",
				Audience:  []string{auth.AudienceData},
				RateLimit: &pbadmin.APITokenServiceCreateRequest_RateLimitRpm{RateLimitRpm: -1},
			})
			return err
		}},
	})
}

func TestCheckRefusal_TenantBudgetService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-budget")
	plane := newCheckAdminPlane(t, h, tenant)
	client := paladinadminv1connect.NewTenantBudgetServiceClient(plane.conn)
	set := func(units int64, nanos int32) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.Set(ctx, &pbadmin.TenantBudgetServiceSetRequest{
				TenantId: tenant.String(), ResourceVersion: firstBudgetVersion,
				BudgetCeiling: &pbadmin.TenantBudgetServiceSetRequest_MaxBudget{
					MaxBudget: &money.Money{CurrencyCode: checkUnitCode, Units: units, Nanos: nanos},
				},
			})
			return err
		}
	}
	wantCheckRefusal(t, plane.ctx(t), connect.CodeInvalidArgument, []checkRefusal{
		{"Set limes.tenant_budgets_ceiling_max_check (negative units)", set(-1, 0)},
		{"Set limes.tenant_budgets_ceiling_max_check (negative nanos)", set(0, -1)},
	})
}

func TestCheckRefusal_CapabilityService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-capability")
	plane := newCheckAdminPlane(t, h, tenant)
	ctx := plane.ctx(t)
	client := paladinadminv1connect.NewCapabilityServiceClient(plane.conn)
	issued, err := client.Issue(ctx(), &pbadmin.CapabilityServiceIssueRequest{
		Subject:  &pbadmin.CapabilityPrincipal{Kind: pbadmin.PrincipalKind_PRINCIPAL_KIND_AGENT, TenantId: tenant.String(), Subject: "agent:check"},
		Audience: paladin.PlaneNames(paladin.PlaneData),
		Caveats: &pbadmin.CapabilityCaveats{
			Ops:            []string{string(limes.OpList)},
			RequestCeiling: &pbadmin.CapabilityCaveats_UnlimitedRequests{UnlimitedRequests: true},
			BudgetCeiling:  &pbadmin.CapabilityCaveats_UnlimitedBudget{UnlimitedBudget: &pbadmin.UnlimitedBudget{CurrencyCode: checkUnitCode}},
		},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	lease := func(units int64, requests int64) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.Lease(ctx, &pbadmin.CapabilityServiceLeaseRequest{
				Id:       issued.GetCapability().GetId(),
				Amount:   &money.Money{CurrencyCode: checkUnitCode, Units: units},
				Requests: requests,
			})
			return err
		}
	}
	wantCheckRefusal(t, ctx, connect.CodeInvalidArgument, []checkRefusal{
		{"Lease limes.usage_lease_amount_check", lease(-1, 0)},
		{"Lease limes.usage_lease_requests_check", lease(0, -1)},
	})
}

func TestCheckRefusal_UserSettingsService(t *testing.T) {
	h := pgharness.Setup(t)
	tenant := mustCreateTenant(t, h.PoolMigrate, "check-settings")
	var cfg config.Config
	_, iamMux := assembleAPIMuxes(t, h, func(c *config.Config) { cfg = *c })
	srv := httptest.NewServer(iamMux)
	t.Cleanup(srv.Close)
	user := mustCreateCheckUser(t, h, tenant)
	iss, err := issuer.New(issuer.Config{Issuer: cfg.Auth.Issuer, Keys: wiringKeys(t, cfg), AccessTokenTTL: checkTokenTTL})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	token, _, err := iss.MintAccess(issuer.AccessClaims{
		Subject: user.String(), TenantID: tenant, Audience: auth.AudienceIAM, Kind: auth.PrincipalKindUser,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	client := paladiniamv1connect.NewUserSettingsServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, srv.URL)))
	ctx := func() context.Context {
		ctx, cancel := context.WithTimeout(context.Background(), checkCallTimeout)
		t.Cleanup(cancel)
		ctx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer "+token)
		info.RequestHeader().Set(paladin.HeaderIdempotencyKey, uuid.New().String())
		return ctx
	}
	update := func(req *pbiam.UpdateMineRequest) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := client.UpdateMine(ctx, req)
			return err
		}
	}
	locale := func(v string) *pbiam.UpdateMineRequest {
		return &pbiam.UpdateMineRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"locale"}}, Locale: v}
	}
	theme := func(v string) *pbiam.UpdateMineRequest {
		return &pbiam.UpdateMineRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"theme"}}, Theme: v}
	}
	wantCheckRefusal(t, ctx, connect.CodeInvalidArgument, []checkRefusal{
		{"UpdateMine user_settings_locale_format (too short)", update(locale("e"))},
		{"UpdateMine user_settings_locale_format (multi-byte rune)", update(locale("\u00e9"))},
		{"UpdateMine user_settings_locale_format (too long)", update(locale("en" + strings.Repeat(localeSubtag, localeMaxChars/len(localeSubtag)+1)))},
		{"UpdateMine user_settings_theme_check (retired)", update(theme(retiredTheme))},
		{"UpdateMine user_settings_theme_check (unknown)", update(theme("neon"))},
	})
}

// checkUserPassword is the password the settings case's user is created with;
// nothing logs in with it.
const checkUserPassword = "check-password"

// mustCreateCheckUser stores a user of tenant and returns its id.
func mustCreateCheckUser(t *testing.T, h *pgharness.Harness, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword(checkUserPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var id uuid.UUID
	if err := h.PoolMigrate.QueryRow(context.Background(),
		`INSERT INTO users (tenant_id, subject, password_hash) VALUES ($1, $2, $3) RETURNING id`,
		tenant, "settings@check", hash).Scan(&id); err != nil {
		t.Fatalf("create user: %v", err)
	}
	return id
}
