package app

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/jackc/pgx/v5"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/apitokenh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/billingh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/capabilityh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/celh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/mcpinspecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/systemh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/admin"
	"github.com/oleg-tkachuk/paladin/backend/internal/auditstream"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/wire"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
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
	tenantH := wire.ProvideTenantHandler(repos, polEngine, cfg)
	collectionH := wire.ProvideCollectionHandler(repos, polEngine, cfg)
	opH := wire.ProvideOperationHandler(repos, polEngine)
	policyH := wire.ProvidePolicyHandler(polEngine, deps.PolStore)
	backendH := wire.ProvideBackendV2Handler(repos, polEngine, cfg)
	// TestBackend connectivity probe over the runtime-configured S3 clients.
	backendProber, err := BuildBackendProber(ctx, cfg.Storage, l)
	if err != nil {
		return nil, nil, fmt.Errorf("build backend prober: %w", err)
	}
	backendH.SetProber(backendProber)
	bucketV2H := wire.ProvideBucketV2Handler(repos, storage, polEngine, cfg)
	quotaH := wire.ProvideQuotaHandler(repos, polEngine)
	auditH := wire.ProvideAuditHandler(repos, polEngine)
	eventSubH := wire.ProvideEventSubHandler(repos, polEngine, cfg.Dispatcher.Sinks)
	// Admin pod owns the producer side of the outbox — Dispatch writes
	// rows; the dispatcher pod (a separate Deployment, see
	// cmd/server/serve_dispatcher.go) consumes them. The same struct
	// retains DeliverOne for the synchronous TestSubscription RPC,
	// which intentionally bypasses the outbox: operator clicked
	// "Test delivery", they want the result now.
	//
	// Every broker pool is wired here too — TestSubscription runs
	// through THIS Dispatcher (not the dispatcher pod's), so without an
	// attached pool the matching deliver* rejects the row with
	// "dispatcher has no <kind> pool" and the operator's Test button is
	// dead for that sink kind. Every pool is lazy: the constructor
	// allocates no sockets and the per-target `get` only dials on first
	// use, so an admin pod that never runs a Test for a given kind pays
	// nothing. (The dispatcher pod closes these on shutdown; the admin
	// pod relies on process exit — same as the NATS pool always has.)
	dispatcher := &worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(repos.EventSub),
		Outbox:      worker.PgxOutboxWriter{Pool: deps.Pool},
		NATS:        worker.NewNatsConnPool(l.Named("nats-pool")),
		SQS:         worker.NewSQSClientPool(l.Named("sqs-pool")),
		RabbitMQ:    worker.NewRabbitMQConnPool(l.Named("rabbitmq-pool")),
		Kafka:       worker.NewKafkaWriterPool(l.Named("kafka-pool")),
		Sinks:       cfg.Dispatcher.Sinks,
		Secrets:     NewSinkSecretResolver(l.Named("sink-secrets")),
		Logger:      l.Named("event-dispatcher"),
		MaxAttempts: 3,
	}
	eventSubH.SetDispatcher(dispatcher)

	// Producer wiring — handler-level lifecycle events fan out into
	// event_deliveries on commit. Without this attach the outbox
	// stays empty in production traffic and only TestSubscription's
	// DeliverOne path lights up NATS / HTTP. Scope today: tenant
	// lifecycle (created / updated / deleted). Bucket / collection /
	// quota lifecycle and data-plane object events follow the same
	// pattern; tracked under the BACKLOG entry "Event dispatcher:
	// producer wiring".
	tenantH.SetEventProducer(dispatcher)
	tenantH.SetLogger(l.Named("tenant-events"))
	bucketV2H.SetEventProducer(dispatcher)
	bucketV2H.SetLogger(l.Named("bucket-events"))
	collectionH.SetEventProducer(dispatcher)
	collectionH.SetLogger(l.Named("collection-events"))
	quotaH.SetEventProducer(dispatcher)
	quotaH.SetLogger(l.Named("quota-events"))
	backendH.SetEventProducer(dispatcher)
	backendH.SetLogger(l.Named("backend-events"))

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
	var capAdmin, apiTokAdmin connect.ServerInterceptor
	if deps.Capability != nil {
		// Charge-event fan-out is opt-in — high-cardinality (every
		// chargeable RPC fires), default off in cfg. When enabled
		// the wiring builds an emitter wrapping the admin pod's
		// dispatcher; otherwise nil is safe (interceptor stamps
		// nil on ctx, ChargeCapability skips the dispatch).
		var chargeEm auth.ChargeEventEmitter
		if cfg.Dispatcher.ChargeEventsEnabled {
			chargeEm = newChargeEmitter(dispatcher, l.Named("charge-events"))
		}
		capAdmin = auth.CapabilityInterceptorWithEvents(
			deps.Capability.Verifier,
			limes.AudiencePlaneAdmin,
			deps.Capability.Usage,
			cfg.Capability.ChargePerRequestAmount,
			cfg.Capability.ChargePerRequestUnit,
			chargeEm,
			auth.WithDPoP(deps.Capability.DPoP),
		)
	} else {
		capAdmin = auth.CapabilityInterceptor(nil, "", nil, 0, "")
	}
	if deps.APIToken != nil {
		// Admin plane: a token carrying ROLES establishes the principal, so a
		// consumer holding platform.capability-issuer can mint capabilities for
		// the tenants it serves without a human session. A roleless service
		// token still falls through to JWT, as before — it would gain nothing
		// here and would newly reach any RPC gated on tenant alone.
		apiTokAdmin = auth.APITokenRoleAuthInterceptor(deps.APIToken.Verifier, deps.APIToken.Limiter, auth.TokenPlaneAdmin)
	} else {
		apiTokAdmin = auth.APITokenInterceptor(nil, "")
	}

	// No options, and deliberately: server peer attributes are what we do NOT
	// want, and since otelconnect v0.10.0 leaving them off is the default.
	// v0.9.0 tagged every server-side span and metric with net.peer.name and
	// net.peer.port — the CLIENT'S EPHEMERAL PORT — and took
	// WithoutServerPeerAttributes() to turn that off; v0.10.0 renamed the
	// attributes to network.peer.* and inverted the switch to
	// WithServerPeerAttributes().
	//
	// So do not add that option here. On traces the peer port is merely noisy;
	// on metrics it makes one time series per TCP connection, so
	// rpc_server_duration grows without bound and the cost lands on whoever
	// stores it.
	otelInt, err := otelconnect.NewServerInterceptor()
	if err != nil {
		l.Fatal("otelconnect interceptor", zap.Error(err))
	}
	tenantGate, tenantFreeze, err := buildTenantGate(deps)
	if err != nil {
		return nil, nil, err
	}

	// Per-tenant request rate limit — same block, same semantics as the data
	// plane (middleware.rate_limit). The admin plane needs it too: the console
	// and any provisioning integration drive it, and its handlers are the
	// expensive ones. Disabled yields a pass-through.
	tenantRLCfg := middleware.TenantRateLimitConfig{
		Store: adapters.NewTenantRateStore(deps.DB.Queries),
	}
	if cfg.Middleware.RateLimit.Enabled {
		tenantRLCfg.RPS = cfg.Middleware.RateLimit.RequestsPerSecond
	}
	tenantRL := middleware.NewTenantRateLimitInterceptor(tenantRLCfg)

	// The plane's interceptors; the codec, size limits and compression it
	// shares with the others are rpcMountOptions.
	adminServer := connect.NewServer(
		// Outermost of all: a panic anywhere below is answered as an
		// internal error and logged with its stack.
		middleware.Recover(l),
		// Outermost of all: tracing and the failure log inside it see an
		// internal error as it happened; the caller sees its code and a
		// request id, not a driver's message.
		middleware.ScrubInternal(),
		otelInt,
		// Outermost after tracing, and BEFORE auth on purpose: connect
		// applies the first-listed interceptor outermost, so anything
		// installed after auth cannot see auth's own rejections — and a
		// wave of failed authentications leaving no log line was the
		// widest part of this gap. Failures only; successes are otel's job.
		middleware.LogOutcome(l),
		// Skip the JWT gate for `paladin_pat_…` bearers so the role-bearing
		// API-token interceptor below can authenticate them. Without this the
		// JWT verifier rejects the bearer first with "jwt: malformed token" and
		// a token carrying platform.capability-issuer never reaches the RPC it
		// exists to call. Every other case is unchanged — a JWT is verified and
		// a missing or invalid non-PAT bearer is still rejected, so auth stays
		// mandatory; a roleless PAT gets no principal here and is denied
		// downstream exactly as before.
		auth.InterceptorSkipAPITokens(verifierAdmin),
		// apiTokAdmin BEFORE RequireAudience, as on the data plane: the audience
		// check reads the principal, so with the API-token interceptor after it
		// a PAT bearer was refused as "no authenticated principal" before it
		// could be authenticated at all. Both paths — JWT and role-bearing PAT —
		// must land a principal first.
		apiTokAdmin,
		auth.RequireAudience(auth.AudienceAdmin),
		// After auth so the tenant is known, and ahead of the audit and
		// idempotency writers so a throttled call costs no database work.
		tenantRL.Intercept,
		// See the data plane: after otel (span) and after auth (principal).
		middleware.LogContext(l),
		capAdmin,
		// Refuses a trashed or purged tenant's credentials; after every
		// interceptor that establishes a principal, before validation and
		// idempotency, so a refused call is never answered from memo.
		tenantGate,
		// Refuses a change to a tenant in the trash, whoever asks.
		tenantFreeze,
		validateInterceptor,
		// Idempotency-Key gate. RequireOnCreate=true means every
		// admin-plane Create*/Issue* RPC must carry an `Idempotency-Key`
		// header — the admin UI (frontend BFF) auto-injects a UUIDv7
		// per submit, so a double-click or auto-retry collapses on
		// the same key. The interceptor BOTH enforces the header AND
		// memoizes the response (replay on a repeat key); the memoize
		// store is scoped per (tenant, method, key).
		middleware.NewIdempotencyInterceptor(repos.Idempotency, middleware.IdempotencyConfig{
			RequireOnCreate: true,
		}),
		// Audit writer: synchronous + crash-durable (ADR-0004). The
		// interceptor inserts the row directly via repos.Audit before the
		// RPC returns, so a process kill can no longer drop a queued entry
		// (the compliance trail must survive a crash). Cost is one indexed
		// append on the response path of each mutating admin RPC.
		middleware.AuditWithMirror(repos.Audit, auth.AudienceAdmin, false,
			optionalAuditMirror(cfg.Dispatcher.AuditMirrorEnabled, dispatcher, l.Named("audit-mirror"))),
	)

	healthH := NewHealthHandler(deps.DB, cfg.Runtime, l).WithRole("admin")
	// Mirror the api builder's subsystem rows so the admin probe surfaces
	// the same capability + api_token components — operators viewing the
	// admin pod alone (e.g. via /system/health.json) shouldn't have to
	// cross-reference the api pod to learn that capability is disabled.
	AddComponent(healthH, capabilityComponent(deps))
	AddComponent(healthH, apiTokenComponent(deps))

	mux := http.NewServeMux()
	// See AssembleAPIMuxes.
	mux.Handle(middleware.UnknownProcedurePattern, middleware.UnknownProcedure())
	healthH.Register(mux)
	adminServices := servicesIn(protoregistry.GlobalFiles, adminPackage)
	mountGRPCStandards(mux, healthH.GRPCChecker(adminServices...), adminServices)
	paladinadminv1connect.RegisterBackendServiceHandler(adminServer, admin.NewBackendServer(backendH))
	paladinadminv1connect.RegisterBucketServiceHandler(adminServer, admin.NewBucketServer(bucketV2H, tenantH))
	paladinadminv1connect.RegisterTenantServiceHandler(adminServer, admin.NewTenantServer(tenantH))
	paladinadminv1connect.RegisterCollectionServiceHandler(adminServer, admin.NewCollectionServer(collectionH, repos.Tenant, tenantH))
	paladinadminv1connect.RegisterPolicyServiceHandler(adminServer, admin.NewPolicyServer(policyH))
	// CELService — stateless validator for CEL filter / match expressions
	// the admin UI surfaces inline (lifecycle.match, eventsub.filter,
	// list-RPC query strings). Same trust posture as PolicyService.Validate:
	// admin-audience JWT only, no DB, no audit, no Cedar gate.
	paladinadminv1connect.RegisterCELServiceHandler(adminServer, celh.NewHandler())
	paladinadminv1connect.RegisterPlatformOperationServiceHandler(adminServer, admin.NewOperationServer(opH))
	paladinadminv1connect.RegisterQuotaServiceHandler(adminServer, admin.NewQuotaServer(quotaH))

	{
		var usageStore limes.UsageStore[pgx.Tx]
		if deps.Capability != nil {
			usageStore = deps.Capability.Usage
		}
		paladinadminv1connect.RegisterTenantBudgetServiceHandler(adminServer, admin.NewTenantBudgetServer(usageStore))

		// BillingService — read-only aggregation over the charges
		// ledger (the schema baseline (001_initial_schema.sql)). Only mounted with a real handler
		// when the capability subsystem is wired (pool + usage).
		var billingHandler *billingh.Handler
		if deps.Capability != nil {
			billingHandler = billingh.NewHandler(deps.Pool, usageStore, polEngine)
		}
		paladinadminv1connect.RegisterBillingServiceHandler(adminServer, admin.NewBillingServer(billingHandler))
	}
	paladinadminv1connect.RegisterAuditLogServiceHandler(adminServer, admin.NewAuditServer(auditH))
	paladinadminv1connect.RegisterEventSubscriptionServiceHandler(adminServer, admin.NewEventSubscriptionServer(eventSubH, tenantH))
	// MCPInspectService — read-only operator visibility into the MCP
	// bridge configuration (profiles, deny-list, tool catalog,
	// upstreams, transport state). Always mounted; the admin's Cedar
	// gate keeps it platform-admin only.
	paladinadminv1connect.RegisterMCPInspectServiceHandler(adminServer, mcpinspecth.NewHandler(cfg.MCP, polEngine))

	paladinadminv1connect.RegisterSystemServiceHandler(adminServer, admin.NewSystemServer(systemh.New(cfg, meta.ConfigPath).WithPool(deps.Pool)))

	// Live audit feed — raw-HTTP SSE endpoint (EventSource can't speak
	// Connect). Auth: the same admin-audience bearer the console's RPCs
	// use, verified inline by the handler; the stream is scoped to the
	// JWT's tenant claim like ListAuditLog. One LISTEN connection per pod
	// (audit_log's trigger NOTIFYs on every insert — migrations/013). The hub
	// runs on a dedicated context registered with deps.StopWatchers — NOT the
	// passed `ctx` (context.Background() under fx) — so the held LISTEN
	// connection is released at shutdown before db.Close(); otherwise
	// pgxpool.Close() deadlocks the admin/embedded teardown until SIGKILL.
	hubCtx, hubCancel := context.WithCancel(context.Background())
	deps.RegisterWatcherStop(hubCancel)
	auditHub := auditstream.NewHub(l.Named("audit-stream"))
	go auditHub.Run(hubCtx, deps.Pool)
	mux.Handle("/audit/stream", auditHub.SSEHandler(verifierAdmin))

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
		capH := capabilityh.NewHandler(deps.Capability.Issuer, deps.Capability.Store, deps.Capability.Usage, polEngine).
			WithBiscuitCopies(deps.Capability.Verifier, deps.Capability.Copies).
			WithCopyUsage(deps.Capability.CopyUsage)
		paladinadminv1connect.RegisterCapabilityServiceHandler(adminServer, capH)
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
		paladinadminv1connect.RegisterCapabilityServiceHandler(adminServer, disabledCapabilityServiceHandler{})
	}

	// APITokenService — Create / Revoke / List / GetSelf. GetSelf is
	// gated only by the interceptor (caller must hold a valid token);
	// the rest are platform-admin via Cedar. Mounted unconditionally —
	// when the api_token subsystem is off we mount the Unimplemented
	// stub so methods return 501 (see CapabilityService rationale).
	if deps.APIToken != nil {
		apiTokH := apitokenh.NewHandler(deps.APIToken.Issuer, deps.APIToken.Store, deps.APIToken.Limiter, polEngine)
		paladinadminv1connect.RegisterAPITokenServiceHandler(adminServer, apiTokH)
	} else {
		// See disabledCapabilityServiceHandler rationale.
		paladinadminv1connect.RegisterAPITokenServiceHandler(adminServer, disabledAPITokenServiceHandler{})
	}

	connecthttp.Mount(mux, adminServer, rpcMountOptions()...)

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
	// See BuildAPIListeners: every response names the release.
	srv, err := BuildHTTPServer(cfg.Admin.Server, middleware.ServerVersion(meta.Version, mux), deps.Logger)
	if err != nil {
		return HTTPListener{}, nil, err
	}
	listener := HTTPListener{
		Plane:  "admin",
		Server: srv,
		TLS:    cfg.Admin.Server.TLS,
	}
	return listener, healthH, nil
}
