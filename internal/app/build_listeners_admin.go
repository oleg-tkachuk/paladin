package app

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/wire"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// BuildAdminListener materialises the admin-plane Connect listener. The
// admin plane carries privileged operations (tenant CRUD, policy authoring,
// backend management, audit reads) and is expected to run with stricter
// network controls than the data/iam planes — typically behind a separate
// ingress with mTLS and tighter NetworkPolicy.
//
// Returns an independent *health.Handler. When the admin plane is co-
// hosted with api in `serve all` (the legacy collapsed mode), wire it up
// so a single shutdown flips both at once. Post-Phase-2 the admin pod is
// separate and gets its own readiness lifecycle.
func BuildAdminListener(ctx context.Context, deps *SharedDeps, meta BuildMeta) (HTTPListener, *health.Handler, error) {
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
		return HTTPListener{}, nil, fmt.Errorf("init protovalidate: %w", err)
	}
	verifierAdmin, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceAdmin, l)
	if err != nil {
		return HTTPListener{}, nil, err
	}
	adminOpts := connect.WithInterceptors(
		auth.Interceptor(verifierAdmin),
		auth.RequireAudience(auth.AudienceAdmin),
		connect.UnaryInterceptorFunc(validateInterceptor),
		middleware.Audit(repos.Audit, auth.AudienceAdmin, false),
	)

	healthH := NewHealthHandler(deps.DB, cfg.Server, l)

	mux := http.NewServeMux()
	healthH.Register(mux)
	mux.Handle(paladinadminv1connect.NewBackendServiceHandler(admin.NewBackendServer(backendH), adminOpts))
	mux.Handle(paladinadminv1connect.NewBucketServiceHandler(admin.NewBucketServer(bucketV2H), adminOpts))
	mux.Handle(paladinadminv1connect.NewTenantServiceHandler(admin.NewTenantServer(tenantH), adminOpts))
	mux.Handle(paladinadminv1connect.NewObjectKeyServiceHandler(admin.NewObjectKeyServer(objectKeyH), adminOpts))
	mux.Handle(paladinadminv1connect.NewPolicyServiceHandler(admin.NewPolicyServer(policyH), adminOpts))
	mux.Handle(paladinadminv1connect.NewOperationServiceHandler(admin.NewOperationServer(opH), adminOpts))
	mux.Handle(paladinadminv1connect.NewQuotaServiceHandler(admin.NewQuotaServer(quotaH), adminOpts))
	mux.Handle(paladinadminv1connect.NewAuditLogServiceHandler(admin.NewAuditServer(auditH), adminOpts))
	mux.Handle(paladinadminv1connect.NewEventSubscriptionServiceHandler(admin.NewEventSubscriptionServer(eventSubH), adminOpts))
	// SystemService.GetConfig surfaces the running config (with secrets
	// redacted) so the UI's /config page renders the live YAML rather than
	// pointing operators at kubectl. platform.admin only; the role check
	// lives inside the shim.
	mux.Handle(paladinadminv1connect.NewSystemServiceHandler(
		admin.NewSystemServer(systemh.New(cfg, meta.ConfigPath)),
		adminOpts,
	))

	listener := HTTPListener{
		Plane:  "admin",
		Server: BuildHTTPServer(cfg.Server.AdminHTTP, mux, l),
		TLS:    cfg.Server.AdminHTTP.TLS,
	}
	return listener, healthH, nil
}

// eventSubStoreAdapter exposes admindomain.EventSubscriptionRepository under
// the worker.SubscriptionStore interface (List-only). Lives here because
// the admin plane wires it; workers pick it up via the same admindomain
// repo when running in dispatch mode.
type eventSubStoreAdapter struct {
	r admindomain.EventSubscriptionRepository
}

func (a eventSubStoreAdapter) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return a.r.List(ctx, args)
}
