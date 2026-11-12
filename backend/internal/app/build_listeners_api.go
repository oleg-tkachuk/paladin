package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/pgerr"

	"google.golang.org/protobuf/reflect/protoregistry"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"go.uber.org/zap"

	connectdata "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/storagebootstraph"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/sessionkey"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/wire"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// BuildMeta carries link-time identity surfaces SystemService needs.
// Threaded through builders rather than read from package globals so each
// subcommand can pin its own values (e.g. `paladin serve mcp` reports a
// different role label than `paladin serve api`).
type BuildMeta struct {
	Version    string
	Commit     string
	BuildTime  string
	ConfigPath string
}

// planeShared is what the data and IAM interceptor stacks share, built once
// per process: two planes co-hosted (embedded mode, tests) then share one
// tenant-state watcher and one health handler, so a SIGTERM flips both
// /readyz to 503 at once.
type planeShared struct {
	validate     connect.ServerInterceptor
	idempotency  connect.ServerInterceptor
	otel         connect.ServerInterceptor
	tenantGate   connect.ServerInterceptor
	tenantFreeze connect.ServerInterceptor
	tenantRL     *middleware.TenantRateLimitInterceptor
	health       *health.Handler
	// role labels the health handler and the HealthService answers.
	role config.Role
}

// AssembleAPIMuxes builds the data and iam Connect mux'es in one process with
// their shared *health.Handler: the embedded mode (`serve mcp --embedded`)
// and the in-process tests. Pure mux assembly — no http.Server wrapping, no
// port binding. In a cluster each plane is its own role (ADR-0047): `serve
// iam` builds AssembleIAMMux, `serve api` AssembleDataMux.
//
// IAM first: it loads the session key, which the data plane then verifies
// with in memory rather than through the JWKS.
func AssembleAPIMuxes(ctx context.Context, deps *SharedDeps, meta BuildMeta) (dataMux, iamMux *http.ServeMux, healthH *health.Handler, err error) {
	shared, err := newPlaneShared(deps, config.RoleAPI)
	if err != nil {
		return nil, nil, nil, err
	}
	if iamMux, err = assembleIAMMux(ctx, deps, meta, shared); err != nil {
		return nil, nil, nil, err
	}
	if dataMux, err = assembleDataMux(ctx, deps, shared); err != nil {
		return nil, nil, nil, err
	}
	return dataMux, iamMux, shared.health, nil
}

// AssembleDataMux builds the data plane alone, for `serve api`. Sessions are
// verified by the IAM role's JWKS (auth.jwks_url): this role holds no key.
func AssembleDataMux(ctx context.Context, deps *SharedDeps) (*http.ServeMux, *health.Handler, error) {
	shared, err := newPlaneShared(deps, config.RoleAPI)
	if err != nil {
		return nil, nil, err
	}
	mux, err := assembleDataMux(ctx, deps, shared)
	if err != nil {
		return nil, nil, err
	}
	return mux, shared.health, nil
}

// AssembleIAMMux builds the IAM plane alone, for `serve iam`: login and the
// token RPCs, the OAuth authorization server and the session JWKS.
func AssembleIAMMux(ctx context.Context, deps *SharedDeps, meta BuildMeta) (*http.ServeMux, *health.Handler, error) {
	shared, err := newPlaneShared(deps, config.RoleIAM)
	if err != nil {
		return nil, nil, err
	}
	mux, err := assembleIAMMux(ctx, deps, meta, shared)
	if err != nil {
		return nil, nil, err
	}
	return mux, shared.health, nil
}

// newPlaneShared builds the interceptors both planes mount and the health
// handler they answer readiness with.
func newPlaneShared(deps *SharedDeps, role config.Role) (planeShared, error) {
	if err := requireTokenAuth(deps); err != nil {
		return planeShared{}, err
	}
	cfg := deps.Cfg
	l := deps.Logger
	repos := deps.Repos

	validateInterceptor, err := middleware.ProtoValidate()
	if err != nil {
		return planeShared{}, fmt.Errorf("init protovalidate: %w", err)
	}

	// Shared Idempotency-Key gate (see internal/middleware/idempotency.go).
	//
	// Two behaviours, and only one of them keys off the method name:
	//
	//   - ENFORCEMENT. RequireOnCreate=true rejects a Create*/Issue* RPC that
	//     omits the header. On the iam plane that is CreateUser — an operator
	//     double-click must not duplicate a user. On the DATA plane it is
	//     nothing at all: this package has no Create*/Issue* RPC, so the flag
	//     is inert here. An earlier version of this comment claimed it covered
	//     UploadObject and CompleteObject; the matcher never looked at those
	//     names and never has.
	//
	//   - MEMOIZE / REPLAY. Any RPC, whatever its name, whose request carries
	//     an Idempotency-Key. This is what actually serves UploadObject: a
	//     retry after a dropped connection replays the first response — the
	//     same object id, the same presigned URL — instead of colliding on the
	//     (collection, path) unique index and answering AlreadyExists, which
	//     is what the caller gets today when the client sends no key.
	//
	// So the header is worth sending on every mutation, not only on the ones
	// the server insists on, and the console now does exactly that.
	idempotencyInterceptor := middleware.NewIdempotencyInterceptor(repos.Idempotency, middleware.IdempotencyConfig{
		RequireOnCreate: true,
		// No SkipMethods here on purpose. Credential minting is skipped by the
		// constructor itself, for every caller — a line in this literal was the
		// only thing protecting RefreshToken from replay, and its removal would
		// have broken nothing visible. See NewIdempotencyInterceptor.
	})

	// OTel: one span per RPC, named from the procedure, plus RED metrics
	// (rpc duration / count) on every call. First in each chain so the
	// span wraps auth + the handler. No-op spans when OTel is disabled
	// (global providers are noops). Only errors on invalid options, which
	// we don't pass — a failure here is a wiring bug, so fail fast.
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
		return planeShared{}, err
	}

	// Per-tenant request rate limit (middleware.rate_limit). The config block
	// has existed since the initial schema with enabled=true; nothing ever read
	// it, so the only limits in force were on login and on api tokens, and a
	// JWT-authenticated client had no ceiling at all. Disabled yields a
	// pass-through rather than an absent interceptor, so the chains below stay
	// the same shape either way.
	tenantRLCfg := middleware.TenantRateLimitConfig{
		Store: ratecount.NewTenantStore(deps.RateLimits().Counter),
	}
	if cfg.Middleware.RateLimit.Enabled {
		tenantRLCfg.RPS = cfg.Middleware.RateLimit.RequestsPerSecond
	}
	tenantRL := middleware.NewTenantRateLimitInterceptor(tenantRLCfg)

	healthH := NewHealthHandler(deps.DB, cfg.Runtime, l).WithRole(string(role))
	// Each subsystem is listed whether on or off: the page says which.
	AddComponent(healthH, capabilityComponent(deps))
	AddComponent(healthH, capabilityRevocationsComponent(deps))
	AddComponent(healthH, apiTokenComponent(deps))
	AddComponent(healthH, valkeyComponent(cfg.Datastores.Valkey.Addr, deps.RateLimits().Shared))

	return planeShared{
		validate:     validateInterceptor,
		idempotency:  idempotencyInterceptor,
		otel:         otelInt,
		tenantGate:   tenantGate,
		tenantFreeze: tenantFreeze,
		tenantRL:     tenantRL,
		health:       healthH,
		role:         role,
	}, nil
}

// assembleDataMux builds the data plane on the shared interceptors.
func assembleDataMux(ctx context.Context, deps *SharedDeps, shared planeShared) (*http.ServeMux, error) {
	cfg := deps.Cfg
	l := deps.Logger
	repos := deps.Repos
	storage := deps.Storage
	polEngine := deps.PolEngine

	// ─── Data-plane handlers ─────────────────────────────────────────────
	objH := wire.ProvideObjectHandler(repos, storage, polEngine, deps.CELEval, deps.SM, cfg)
	opH := wire.ProvideOperationHandler(repos, polEngine, cfg)
	batchH := wire.ProvideBatchHandler(repos, opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, deps.SM, cfg)
	versionH := wire.ProvideVersionHandler(repos, polEngine)
	lockH := wire.ProvideLockHandler(repos, polEngine)
	taintH := wire.ProvideTaintHandler(repos, polEngine)
	quotaUpdater := adapters.NewQuotaRepoV2(deps.DB.Queries, deps.Pool)
	objH.SetVersionHandler(versionH)
	objH.SetQuotaUpdater(quotaUpdater)

	// Object lifecycle producer wiring — CompleteObject / DeleteObject /
	// RestoreObject / UpdateObject / CopyObject record their events on their
	// own transactions; the dispatcher pod fans them out (ADR-0044).
	apiEvents := worker.NewEventRecorder(l.Named("api-event-dispatcher"))
	objH.SetEventProducer(apiEvents)
	objH.SetLogger(l.Named("object-events"))
	mpH.SetVersionRecorder(&multipartVersionAdapter{v: versionH})
	mpH.SetQuotaUpdater(quotaUpdater)

	// Storage self-provisioning (StorageBootstrapService) — a tenant ensures
	// its own bucket + collections with its data-plane PAT (aud=data), no
	// admin credential. Reuses the admin bucket-create path (bucketh) and the
	// collection create path (collection), each wired to the api dispatcher so
	// a self-provision emits the same paladin.bucket.created / paladin.collection.created
	// lifecycle events an admin create would. repos.BucketV2 doubles as the
	// backend-existence checker (a tenant may not create backends).
	bucketBootstrapH := wire.ProvideBucketV2Handler(repos, storage, polEngine, cfg)
	bucketBootstrapH.SetEventProducer(apiEvents)
	bucketBootstrapH.SetLogger(l.Named("bucket-events"))
	collectionBootstrapH := wire.ProvideCollectionHandler(repos, polEngine, cfg)
	collectionBootstrapH.SetEventProducer(apiEvents)
	collectionBootstrapH.SetLogger(l.Named("collection-events"))
	storageBootstrapH := storagebootstraph.NewHandler(bucketBootstrapH, collectionBootstrapH, repos.BucketV2, polEngine)

	// The session key is in memory only where IAM is co-hosted; otherwise
	// BuildVerifier verifies by the IAM role's JWKS.
	verifierData, err := BuildVerifier(ctx, cfg.Auth, deps.SessionKeys, auth.AudienceData, l)
	if err != nil {
		return nil, err
	}

	// Capability interceptor on the data plane — additive. When the
	// capability subsystem is off (deps.Capability == nil) the helper
	// returns a no-op, so existing JWT-only deploys are unaffected.
	// When a capability token IS supplied, the interceptor stamps
	// *limes.Capability on the context and handlers branch via
	// auth.CapabilityFromContext.
	//
	// IAM plane intentionally has NO capability interceptor: capabilities
	// don't have an "iam" audience (the audiences are data / admin /
	// mcp). User-authn flows that go through iam are not the agent path.
	var capData connect.ServerInterceptor
	if deps.Capability != nil {
		// Same opt-in event-emitter pattern as the admin pod —
		// the api dispatcher fan-out into event_deliveries when
		// cfg.Dispatcher.ChargeEventsEnabled is on. Object lifecycle
		// is the highest-cardinality producer, capability charges
		// are the second; subscribers must filter on `type` (or the
		// coarser `kind`) to keep their queue depth bounded.
		var chargeEm auth.ChargeEventEmitter
		if cfg.Dispatcher.ChargeEventsEnabled {
			chargeEm = NewChargeEmitter(apiEvents, l.Named("charge-events"))
		}
		// Establishing on the data plane: a capability names its tenant and
		// the verifier has checked signature, expiry and revocation, so it is
		// enough to authenticate ITS tenant's object operations. That is what
		// lets a consumer reach a tenant without holding a long-lived
		// credential for it. A capability presented alongside a JWT or API
		// token stays additive — the existing principal wins.
		// The taint lookup is what gives AllowTaintedRead something to act
		// on: a capability without it is refused reads of flagged objects.
		// A capability the MCP server exchanged acts for its user (ADR-0041).
		capOpts := []auth.CapabilityOption{delegationOption(deps.Capability, repos.IAMUser)}
		if taintH != nil {
			capOpts = append(capOpts, auth.WithTaintLookup(taintH.Tainted))
		}
		capData = auth.CapabilityEstablishingInterceptor(
			deps.Capability.Auth.Verifier,
			auth.TokenPlaneData,
			deps.Capability.Usage,
			cfg.Capability.ChargePerRequestAmount,
			chargeEm,
			capOpts...,
		)
	} else {
		capData = auth.CapabilityInterceptor(nil, "", nil, 0)
	}

	// API tokens ESTABLISH the principal here, so a service can authenticate
	// uploads with a long-lived `paladin_pat_…` key alone (paired with
	// auth.InterceptorSkipAPITokens below). Per-token rate caps apply.
	apiTokData := auth.APITokenInterceptor(nil, "")
	if deps.APIToken != nil {
		apiTokData = auth.APITokenAuthInterceptor(deps.APIToken.Auth.Verifier, deps.RateLimits().Tokens, auth.TokenPlaneData)
	}

	// The plane's interceptors; the codec, size limits and compression it
	// shares with the others are rpcMountOptions.
	// The readers the audit log takes each change's before and after with,
	// filled once the servers they read through exist (dataStateServers).
	dataStateReaders := middleware.StateReaders{}
	dataServer := connect.NewServer(
		// Outermost of all: a panic anywhere below is answered as an
		// internal error and logged with its stack.
		middleware.Recover(l),
		// Outermost of all: tracing and the failure log inside it see an
		// internal error as it happened; the caller sees its code and a
		// request id, not a driver's message.
		middleware.ScrubInternal(),
		shared.otel,
		// Outermost after tracing, and BEFORE auth on purpose: connect
		// applies the first-listed interceptor outermost, so anything
		// installed after auth cannot see auth's own rejections — and a
		// wave of failed authentications leaving no log line was the
		// widest part of this gap. Failures only; successes are otel's job.
		middleware.LogOutcome(l),
		// Inside the failure log: a call the caller cancelled, or that found
		// the database unreachable, is logged and answered as that, not as
		// the server's own fault.
		middleware.ClassifyTransient(pgerr.IsUnavailable),
		// Ahead of every interceptor that reads a credential: a second
		// Authorization header or DPoP proof is refused, not left to
		// whichever reader takes the first.
		auth.SingleCredentials(),
		// Auth: a JWT (OIDC/issuer) OR an `paladin_pat_…` API key. The JWT gate verifies
		// non-PAT bearers and lets a PAT through; apiTokData then verifies the PAT and
		// establishes the principal. Both paths land a principal before RequireAudience.
		// Also steps aside for a capability: since ADR-0010 one may be the whole
		// credential, and it rides in its own header — so a capability-only
		// request has no Authorization at all and this gate used to refuse it
		// as "missing Authorization header" before it could be authenticated.
		auth.InterceptorSkipTokensAndCapabilities(verifierData),
		apiTokData,
		// capData BEFORE RequireAudience for the same reason apiTokData is: the
		// audience check reads the principal, so a credential that establishes
		// one has to run first.
		capData,
		auth.RequireAudience(auth.AudienceData),
		// After the principal, which carries the OAuth grant (ADR-0035).
		auth.RequireOAuthWriteScope(),
		// Refuses a trashed or purged tenant's credentials; after every
		// interceptor that establishes a principal, before validation and
		// idempotency, so a refused call is never answered from memo.
		shared.tenantGate,
		// Refuses a change to a tenant in the trash, whoever asks.
		shared.tenantFreeze,
		// A platform admin's request acts on the tenant it names; everything
		// below keys on that tenant, so it is set before any of them.
		middleware.ActOnNamedTenant(),
		// After auth so the tenant is known, and before the quota and
		// idempotency work so a throttled caller is turned away before it
		// costs a database round-trip.
		shared.tenantRL.Intercept,
		// After otel (span exists) and after auth (principal known), so the
		// request logger carries trace_id, span_id, request_id and tenant_id
		// for every line the handlers write through logger.FromContext.
		middleware.LogContext(l),
		// Bucket-scoped enforcement is wired here: repos.Object.LookupBucket
		// resolves the upload's Collection to its (backend, bucket) so a
		// bucket quota row can be found. Without WithBucketScope those rows
		// are maintained by the reconciler and shown on /stats but reject
		// nothing. Both scopes cost a point lookup on the upload path —
		// collections by PK, then quotas by its unique index.
		middleware.NewQuotaSoftCheck(repos.Quota).WithBucketScope(repos.Object).Interceptor(),
		shared.validate,
		shared.idempotency,
		// Every write, whoever makes it: a tenant's own agents and users, and
		// a platform admin acting in the tenant it named. After idempotency,
		// so a replayed response writes no second row. No mirror: an object
		// write already emits its own paladin.object.* event.
		middleware.AuditWithState(repos.Audit, auth.AudienceData, false, nil, dataStateReaders),
	)

	// Storage-backend reachability probes are tracked in BACKLOG —
	// the existing S3 adapter doesn't expose a HEAD/ListBuckets
	// method suited to a sub-second probe. Adding one is a separate
	// PR; until then, storage failures surface as RPC-level errors
	// on PutObject / Presign rather than as /readyz failures.

	dataMux := http.NewServeMux()
	// A procedure this release lacks is a Connect Unimplemented, which
	// carries the release header (see middleware.ServerVersion).
	dataMux.Handle(middleware.UnknownProcedurePattern, middleware.UnknownProcedure())
	shared.health.Register(dataMux)
	dataServices := servicesIn(protoregistry.GlobalFiles, dataPackage)
	mountGRPCStandards(dataMux, shared.health.GRPCChecker(dataServices...), dataServices)
	dataState := dataStateServers{
		object: connectdata.NewObjectServer(objH, versionH).WithLocks(lockH).WithTaints(taintH),
		tags:   connectdata.NewObjectTagServer(objH),
	}
	dataState.fill(dataStateReaders)
	paladindatav1connect.RegisterObjectServiceHandler(dataServer, dataState.object)
	paladindatav1connect.RegisterMultipartUploadServiceHandler(dataServer, connectdata.NewMultipartServer(mpH))
	paladindatav1connect.RegisterPresignServiceHandler(dataServer, connectdata.NewPresignServer(presignH))
	paladindatav1connect.RegisterObjectTagServiceHandler(dataServer, dataState.tags)
	paladindatav1connect.RegisterBatchServiceHandler(dataServer, connectdata.NewBatchServer(batchH))
	paladindatav1connect.RegisterOperationServiceHandler(dataServer, connectdata.NewOperationServer(opH))
	// StorageBootstrapService shares the data-plane interceptor stack
	// (dataServer) so the api_token principal + RequireAudience(data) apply — an
	// aud=data PAT can reach EnsureTenantStorage.
	paladindatav1connect.RegisterStorageBootstrapServiceHandler(dataServer, connectdata.NewStorageBootstrapServer(storageBootstrapH))
	connecthttp.Mount(dataMux, dataServer, rpcMountOptions()...)
	return dataMux, nil
}

// assembleIAMMux builds the IAM plane on the shared interceptors. It mints
// sessions, so it alone loads the private key, and leaves the key set on deps
// for a co-hosted plane to verify with (ADR-0047).
func assembleIAMMux(ctx context.Context, deps *SharedDeps, meta BuildMeta, shared planeShared) (*http.ServeMux, error) {
	cfg := deps.Cfg
	l := deps.Logger
	repos := deps.Repos
	polEngine := deps.PolEngine

	keys, err := sessionkey.Load(cfg.Auth.SessionKeyPath, cfg.Auth.PreviousSessionPublicKeyPaths)
	if err != nil {
		return nil, fmt.Errorf("auth.session_key_path: %w", err)
	}
	deps.SessionKeys = keys
	l.Info("session key loaded", zap.String("kid", keys.KeyID()), zap.Int("published", keys.Public().Len()))
	iss, err := wire.ProvideIssuer(cfg, keys)
	if err != nil {
		return nil, err
	}
	dec := wire.ProvideRefreshDecoder(cfg, keys)
	authH := wire.ProvideAuthHandler(repos, iss, dec, polEngine).
		WithReuseAudit(repos.Audit, l).
		WithCollectionRoutes(wire.ProvideCollectionRouteLister(repos, polEngine, cfg))
	userH := wire.ProvideUserHandler(repos, polEngine)
	if deps.Capability != nil {
		userH.WithSessionRevoker(deps.Capability.Auth.BulkRevocation).
			WithCapabilities(deps.Capability.Records)
	}
	userSettingsH := usersettingsh.NewHandler(
		adapters.NewUserSettingsRepo(deps.DB.Queries),
		repos.IAMUser,
		polEngine,
	)
	verifierIAM, err := BuildVerifier(ctx, cfg.Auth, keys, auth.AudienceIAM, l)
	if err != nil {
		return nil, err
	}

	// The IAM plane serves Login and the token RPCs without a credential;
	// with the capability subsystem on, a capability-only call passes the
	// JWT gate to capIAM, which accepts the delegation issuer's alone.
	iamGate := auth.NewPermissiveInterceptor(verifierIAM,
		"Login",
		"RefreshToken",
		"ExchangeAudience",
	)
	capIAM := auth.CapabilityInterceptor(nil, "", nil, 0)
	if deps.Capability != nil {
		iamGate.SkipCapabilities = true
		capIAM = auth.CapabilityInterceptor(
			deps.Capability.Auth.Verifier,
			auth.TokenPlaneIAM,
			deps.Capability.Usage,
			cfg.Capability.ChargePerRequestAmount,
			delegationOption(deps.Capability, repos.IAMUser),
		)
	}

	// API tokens are additive on this plane, audience-pinned, rate-capped.
	apiTokIAM := auth.APITokenInterceptor(nil, "")
	if deps.APIToken != nil {
		apiTokIAM = auth.APITokenInterceptorWithLimiter(deps.APIToken.Auth.Verifier, deps.RateLimits().Tokens, auth.TokenPlaneIAM)
	}

	// The readers the audit log takes each change's before and after with,
	// filled once the servers they read through exist (iamStateServers).
	iamStateReaders := middleware.StateReaders{}

	// The plane's interceptors; the codec, size limits and compression it
	// shares with the others are rpcMountOptions.
	iamServer := connect.NewServer(
		// Outermost of all: a panic anywhere below is answered as an
		// internal error and logged with its stack.
		middleware.Recover(l),
		// Outermost of all: tracing and the failure log inside it see an
		// internal error as it happened; the caller sees its code and a
		// request id, not a driver's message.
		middleware.ScrubInternal(),
		shared.otel,
		// Outermost after tracing, and BEFORE auth on purpose: connect
		// applies the first-listed interceptor outermost, so anything
		// installed after auth cannot see auth's own rejections — and a
		// wave of failed authentications leaving no log line was the
		// widest part of this gap. Failures only; successes are otel's job.
		middleware.LogOutcome(l),
		// Inside the failure log: a call the caller cancelled, or that found
		// the database unreachable, is logged and answered as that, not as
		// the server's own fault.
		middleware.ClassifyTransient(pgerr.IsUnavailable),
		// Ahead of every interceptor that reads a credential: a second
		// Authorization header or DPoP proof is refused, not left to
		// whichever reader takes the first.
		auth.SingleCredentials(),
		iamGate.Intercept,
		apiTokIAM,
		// A capability the MCP server exchanged acts for its user (ADR-0041);
		// any other establishes nothing on this plane.
		capIAM,
		// After the principal, which carries the OAuth grant (ADR-0035). A
		// permissive RPC with no principal passes it.
		auth.RequireOAuthWriteScope(),
		// Refuses a trashed or purged tenant's credentials; after every
		// interceptor that establishes a principal, before validation and
		// idempotency, so a refused call is never answered from memo.
		shared.tenantGate,
		// Refuses a change to a tenant in the trash, whoever asks.
		shared.tenantFreeze,
		middleware.NewLoginRateLimiter(
			deps.RateLimits().Keys, l.Named("login-ratelimit"),
			cfg.Auth.LoginRateLimitPerSubjectPerMinute,
			cfg.Auth.LoginRateLimitPerIPPerMinute,
		).Interceptor(),
		// Audit IAM mutations (Login, CreateUser, RefreshToken, …).
		// Placed after the permissive interceptor so
		// anonymous/failed Login attempts are still recorded — a
		// credential-misuse breach must leave a server-side trail
		// (SOC 2 / ISO 27001 / PCI). No dispatcher mirror on this plane.
		// Synchronous + crash-durable (ADR-0004): the row commits before
		// the RPC returns, so a kill can't drop a credential-misuse trail.
		middleware.AuditWithState(repos.Audit, auth.AudienceIAM, false, nil, iamStateReaders),
		// Tenant-scoped IAM calls (memberships, password change) are charged
		// to their tenant; Login and RefreshToken carry no tenant yet and are
		// covered by the login limiter above.
		shared.tenantRL.Intercept,
		// See the data plane: after otel and after auth. On IAM the principal
		// is often absent (Login, RefreshToken are permissive), so these lines
		// carry trace and request id without a tenant — which is correct, not
		// a gap. An anonymous failed login is exactly the line worth finding.
		middleware.LogContext(l),
		shared.validate,
		shared.idempotency,
	)

	iamMux := http.NewServeMux()
	iamMux.Handle(middleware.UnknownProcedurePattern, middleware.UnknownProcedure())
	shared.health.Register(iamMux)
	iamMux.Handle(sessionkey.WellKnownPath, keys.Handler())
	iamServices := servicesIn(protoregistry.GlobalFiles, iamPackage)
	mountGRPCStandards(iamMux, shared.health.GRPCChecker(iamServices...), iamServices)
	paladiniamv1connect.RegisterAuthServiceHandler(iamServer, connectiam.NewAuthServer(authH))
	iamState := iamStateServers{
		user:     connectiam.NewUserServer(userH, repos.Tenant),
		settings: connectiam.NewUserSettingsServer(userSettingsH),
	}
	iamState.fill(iamStateReaders)
	paladiniamv1connect.RegisterUserServiceHandler(iamServer, iamState.user)
	paladiniamv1connect.RegisterHealthServiceHandler(iamServer, connectiam.NewSystemServer(meta.Version, meta.Commit, ParseBuildTime(meta.BuildTime), string(shared.role), shared.health))

	paladiniamv1connect.RegisterUserSettingsServiceHandler(iamServer, iamState.settings)
	connecthttp.Mount(iamMux, iamServer, rpcMountOptions()...)

	// OAuth 2.1 Authorization Server (ADR-0009): mount the raw-HTTP /oauth/*
	// endpoints on the IAM mux and seed first-party clients. Reuses the same
	// issuer + refresh decoder the Connect AuthService uses, so OAuth-minted
	// bearers are indistinguishable from login-minted ones downstream.
	if cfg.Auth.OAuth.Enabled {
		oauthStore := oauth.NewPgxStore(deps.Pool)
		skipConsent := map[string]bool{}
		exchangers := map[string]bool{}
		for _, sc := range cfg.Auth.OAuth.SeedClients {
			client, serr := oauth.SeedClient(sc)
			if serr != nil {
				return nil, serr
			}
			if serr := oauthStore.UpsertClient(ctx, client); serr != nil {
				return nil, fmt.Errorf("oauth seed client %q: %w", sc.ClientID, serr)
			}
			if sc.SkipConsent {
				skipConsent[sc.ClientID] = true
			}
			if sc.TokenExchange {
				exchangers[sc.ClientID] = true
			}
		}
		as := oauth.NewHandler(cfg.Auth.OAuth, oauthStore, repos.IAMUser, repos.IAMRefresh, iss, dec, deps.RateLimits().Keys, l).
			WithAuthorizer(polEngine).
			WithAudit(repos.Audit).
			WithSkipConsent(skipConsent).
			WithMCPResource(cfg.MCP.OAuth.ResourceURL)
		// RFC 8693 exchange for the MCP server (ADR-0041): only when a seed
		// client may call it. It trades a paladin-mcp token for the capability
		// that acts for its user, so it needs the delegation issuer.
		if len(exchangers) > 0 {
			var xerr error
			if as, xerr = withDelegationExchange(ctx, as, deps, exchangers, repos.IAMUser); xerr != nil {
				return nil, xerr
			}
		}
		as.Mount(iamMux)
		// The metadata at the AS's own origin, where RFC 8414 puts it; the MCP
		// server serves the same document for a same-origin deployment.
		if asMeta := cfg.MCP.OAuth.AuthorizationServer; asMeta.Issuer != "" {
			iamMux.Handle(oauth.WellKnownAuthorizationServer, oauth.MetadataHandler(asMeta, cfg.Auth.OAuth.TokenExchangeEnabled()))
		}
		l.Info("oauth authorization server enabled", zap.Int("seed_clients", len(cfg.Auth.OAuth.SeedClients)))
	}

	return iamMux, nil
}

// BuildAPIListeners materialises the data Connect listener of `serve api`,
// an h2c-enabled http.Server bound to cfg.API.Server.Data.
func BuildAPIListeners(ctx context.Context, deps *SharedDeps, meta BuildMeta) ([]HTTPListener, *health.Handler, error) {
	dataMux, healthH, err := AssembleDataMux(ctx, deps)
	if err != nil {
		return nil, nil, err
	}
	cfg := deps.Cfg
	// Every response names the release, a 404 for a procedure this release
	// lacks included: that is how a client tells a contract skew.
	dataSrv, err := BuildHTTPServer(cfg.API.Server.Data, middleware.ServerVersion(meta.Version, dataMux), deps.Logger)
	if err != nil {
		return nil, nil, err
	}
	return []HTTPListener{{Plane: "data", Server: dataSrv, TLS: cfg.API.Server.Data.TLS}}, healthH, nil
}

// BuildIAMListeners materialises the IAM Connect listener of `serve iam`,
// bound to cfg.API.Server.IAM.
func BuildIAMListeners(ctx context.Context, deps *SharedDeps, meta BuildMeta) ([]HTTPListener, *health.Handler, error) {
	iamMux, healthH, err := AssembleIAMMux(ctx, deps, meta)
	if err != nil {
		return nil, nil, err
	}
	cfg := deps.Cfg
	iamSrv, err := BuildHTTPServer(cfg.API.Server.IAM, middleware.ServerVersion(meta.Version, iamMux), deps.Logger)
	if err != nil {
		return nil, nil, err
	}
	return []HTTPListener{{Plane: "iam", Server: iamSrv, TLS: cfg.API.Server.IAM.TLS}}, healthH, nil
}

// multipartVersionAdapter bridges multipart.VersionRecorder onto the object
// VersionHandler so the multipart handler can record promotion events
// without a circular dependency on the object package.
type multipartVersionAdapter struct {
	v *objecth.VersionHandler
}

func (a *multipartVersionAdapter) OnPromote(ctx context.Context, vo multiparth.VersionedObject) error {
	if a.v == nil {
		return nil
	}
	return a.v.OnPromote(ctx, objecth.Object{
		ObjectID:              vo.ObjectID,
		TenantID:              vo.TenantID,
		Collection:            vo.Collection,
		Key:                   vo.Key,
		ContentType:           vo.ContentType,
		SizeBytes:             vo.SizeBytes,
		ETag:                  vo.ETag,
		ChecksumAlgo:          vo.ChecksumAlgo,
		Checksum:              vo.Checksum,
		ChecksumPartSizeBytes: vo.ChecksumPartSizeBytes,
		Metadata:              vo.Metadata,
		Tags:                  vo.Tags,
	})
}
