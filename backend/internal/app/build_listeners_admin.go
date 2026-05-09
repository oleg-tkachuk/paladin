package app

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/apitokenh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/capabilityh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/celh"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/mcpinspecth"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/capability"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/wire"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// AssembleAdminMux builds the admin Connect mux plus its *health.Handler.
// Pure mux assembly — no http.Server, no port binding. Both
// `serve admin` (TCP listener) and `serve mcp --embedded` (inline
// transport) consume this.
func AssembleAdminMux(ctx context.Context, deps *SharedDeps, meta BuildMeta) (*http.ServeMux, *health.Handler, error) {
	cfg := deps.Cfg
	l := deps.Logger
	repos := deps.Repos
	storage := deps.Storage
	polEngine := deps.PolEngine

	// ─── Admin handlers ──────────────────────────────────────────────────
	tenantH := wire.ProvideTenantHandler(repos, polEngine)
	objectKeyH := wire.ProvideObjectKeyHandler(repos, polEngine, cfg)
	opH := wire.ProvideOperationHandler(repos, polEngine)
	policyH := wire.ProvidePolicyHandler(polEngine, deps.PolStore)
	backendH := wire.ProvideBackendV2Handler(repos, polEngine)
	bucketV2H := wire.ProvideBucketV2Handler(repos, storage, polEngine)
	quotaH := wire.ProvideQuotaHandler(repos, polEngine)
	auditH := wire.ProvideAuditHandler(repos, polEngine)
	eventSubH := wire.ProvideEventSubHandler(repos, polEngine)
	dispatcher := &worker.Dispatcher{
		Store:       eventSubStoreAdapter{r: repos.EventSub},
		Logger:      l.Named("event-dispatcher"),
		MaxAttempts: 3,
	}
	eventSubH.SetDispatcher(dispatcher)

	// ─── Interceptor stack ───────────────────────────────────────────────
	validateInterceptor, err := middleware.ProtoValidate()
	if err != nil {
		return nil, nil, fmt.Errorf("init protovalidate: %w", err)
	}
	verifierAdmin, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceAdmin, l)
	if err != nil {
		return nil, nil, err
	}
	// Capability + API-token interceptors — additive, run after JWT
	// verify so a missing token falls through to JWT auth and an
	// invalid token fails loud. Most admin RPCs gate on roles
	// (platform.admin); the agent / service paths read the typed
	// principal via auth.CapabilityFromContext / APITokenFromContext.
	var capAdmin, apiTokAdmin connect.Interceptor
	if deps.Capability != nil {
		capAdmin = auth.CapabilityInterceptor(
			deps.Capability.Verifier,
			capability.AudiencePlaneAdmin,
			deps.Capability.Usage,
			cfg.Admin.Server.RealIPHeader,
			cfg.Capability.ChargePerRequestAmount,
			cfg.Capability.ChargePerRequestUnit,
		)
	} else {
		capAdmin = auth.CapabilityInterceptor(nil, "", nil, "", 0, "")
	}
	if deps.APIToken != nil {
		apiTokAdmin = auth.APITokenInterceptorWithLimiter(deps.APIToken.Verifier, deps.APIToken.Limiter, "admin")
	} else {
		apiTokAdmin = auth.APITokenInterceptor(nil, "")
	}

	adminOpts := connect.WithInterceptors(
		auth.Interceptor(verifierAdmin),
		auth.RequireAudience(auth.AudienceAdmin),
		capAdmin,
		apiTokAdmin,
		connect.UnaryInterceptorFunc(validateInterceptor),
		middleware.Audit(repos.Audit, auth.AudienceAdmin, false),
	)

	healthH := NewHealthHandler(deps.DB, cfg.Runtime, l).WithRole("admin")
	// Mirror the api builder's subsystem rows so the admin probe surfaces
	// the same capability + api_token components — operators viewing the
	// admin pod alone (e.g. via /system/health.json) shouldn't have to
	// cross-reference the api pod to learn that capability is disabled.
	if deps.Capability != nil && deps.Capability.Issuer != nil {
		AddSubsystemCheck(healthH, "capability", true, func(ctx context.Context) error {
			if deps.Capability.Issuer == nil {
				return fmt.Errorf("capability issuer not initialised")
			}
			return nil
		})
	} else {
		AddDisabledSubsystem(healthH, "capability")
	}
	if deps.APIToken != nil {
		AddSubsystemCheck(healthH, "api_token", false, func(ctx context.Context) error {
			return nil
		})
	} else {
		AddDisabledSubsystem(healthH, "api_token")
	}

	mux := http.NewServeMux()
	healthH.Register(mux)
	mux.Handle(paladinadminv1connect.NewBackendServiceHandler(admin.NewBackendServer(backendH), adminOpts))
	mux.Handle(paladinadminv1connect.NewBucketServiceHandler(admin.NewBucketServer(bucketV2H), adminOpts))
	mux.Handle(paladinadminv1connect.NewTenantServiceHandler(admin.NewTenantServer(tenantH), adminOpts))
	mux.Handle(paladinadminv1connect.NewObjectKeyServiceHandler(admin.NewObjectKeyServer(objectKeyH), adminOpts))
	mux.Handle(paladinadminv1connect.NewPolicyServiceHandler(admin.NewPolicyServer(policyH), adminOpts))
	// CELService — stateless validator for CEL filter / match expressions
	// the admin UI surfaces inline (lifecycle.match, eventsub.filter,
	// list-RPC query strings). Same trust posture as PolicyService.Validate:
	// admin-audience JWT only, no DB, no audit, no Cedar gate.
	mux.Handle(paladinadminv1connect.NewCELServiceHandler(celh.NewHandler(), adminOpts))
	mux.Handle(paladinadminv1connect.NewOperationServiceHandler(admin.NewOperationServer(opH), adminOpts))
	mux.Handle(paladinadminv1connect.NewQuotaServiceHandler(admin.NewQuotaServer(quotaH), adminOpts))
	{
		var usageStore capability.UsageStore
		if deps.Capability != nil {
			usageStore = deps.Capability.Usage
		}
		mux.Handle(paladinadminv1connect.NewTenantBudgetServiceHandler(
			admin.NewTenantBudgetServer(usageStore), adminOpts,
		))
	}
	mux.Handle(paladinadminv1connect.NewAuditLogServiceHandler(admin.NewAuditServer(auditH), adminOpts))
	mux.Handle(paladinadminv1connect.NewEventSubscriptionServiceHandler(admin.NewEventSubscriptionServer(eventSubH), adminOpts))
	// MCPInspectService — read-only operator visibility into the MCP
	// bridge configuration (profiles, deny-list, tool catalog,
	// upstreams, transport state). Always mounted; the admin's Cedar
	// gate keeps it platform-admin only.
	mux.Handle(paladinadminv1connect.NewMCPInspectServiceHandler(
		mcpinspecth.NewHandler(cfg.MCP, polEngine),
		adminOpts,
	))
	mux.Handle(paladinadminv1connect.NewSystemServiceHandler(
		admin.NewSystemServer(systemh.New(cfg, meta.ConfigPath)),
		adminOpts,
	))

	// JWKS endpoint — public, unauthenticated. Verifiers in other pods
	// fetch and cache the issuer's public key set so capability checks
	// stay local. Mounted only when the capability subsystem is on; a
	// disabled deploy returns 404 from the default mux instead of an
	// empty {"keys": []} that leaks "we have a JWKS endpoint but no
	// keys yet" to scanners.
	if deps.Capability != nil {
		mux.Handle("/.well-known/jwks.json", JWKSHandler(deps.Capability.PublicKeys))

		// CapabilityService — Issue / Delegate / Revoke / List. The
		// admin interceptor stack already enforces audience + JWT
		// authentication; the handler does its own Cedar gate per RPC.
		capH := capabilityh.NewHandler(deps.Capability.Issuer, deps.Capability.Store, deps.Capability.Usage, polEngine)
		mux.Handle(paladinadminv1connect.NewCapabilityServiceHandler(capH, adminOpts))
	} else {
		// Subsystem disabled — mount the disabled-subsystem stub so
		// callers receive Connect CodeUnimplemented (HTTP 501) plus
		// X-Paladin-Reason: subsystem_disabled / X-Paladin-Subsystem: capability
		// headers and a message naming the config flag to flip. Beats
		// the default-mux 404 ("path not found") and the bare
		// Unimplemented from the generated stub ("not implemented")
		// which can't distinguish "binary lacks this RPC" from "the
		// operator turned this subsystem off". See disabled_subsystem.go
		// for the full contract.
		mux.Handle(paladinadminv1connect.NewCapabilityServiceHandler(
			disabledCapabilityServiceHandler{}, adminOpts,
		))
	}

	// APITokenService — Create / Revoke / List / GetSelf. GetSelf is
	// gated only by the interceptor (caller must hold a valid token);
	// the rest are platform-admin via Cedar. Mounted unconditionally —
	// when the api_token subsystem is off we mount the Unimplemented
	// stub so methods return 501 (see CapabilityService rationale).
	if deps.APIToken != nil {
		apiTokH := apitokenh.NewHandler(deps.APIToken.Issuer, deps.APIToken.Store, deps.APIToken.Limiter, polEngine)
		mux.Handle(paladinadminv1connect.NewAPITokenServiceHandler(apiTokH, adminOpts))
	} else {
		// See disabledCapabilityServiceHandler rationale.
		mux.Handle(paladinadminv1connect.NewAPITokenServiceHandler(
			disabledAPITokenServiceHandler{}, adminOpts,
		))
	}

	return mux, healthH, nil
}

// BuildAdminListener wraps AssembleAdminMux in an h2c http.Server bound to
// cfg.Admin.Server. Sized for low replica counts (1–2) and a separate
// ingress with mTLS / tighter NetworkPolicy than data/iam.
func BuildAdminListener(ctx context.Context, deps *SharedDeps, meta BuildMeta) (HTTPListener, *health.Handler, error) {
	mux, healthH, err := AssembleAdminMux(ctx, deps, meta)
	if err != nil {
		return HTTPListener{}, nil, err
	}
	cfg := deps.Cfg
	l := deps.Logger
	listener := HTTPListener{
		Plane:  "admin",
		Server: BuildHTTPServer(cfg.Admin.Server, mux, l),
		TLS:    cfg.Admin.Server.TLS,
	}
	return listener, healthH, nil
}

// eventSubStoreAdapter exposes admindomain.EventSubscriptionRepository under
// the worker.SubscriptionStore interface (List-only).
type eventSubStoreAdapter struct {
	r admindomain.EventSubscriptionRepository
}

func (a eventSubStoreAdapter) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return a.r.List(ctx, args)
}
