package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/codec"
	connectdata "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/storagebootstraph"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/wire"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
	"github.com/oleg-tkachuk/paladin/capability"
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

// AssembleAPIMuxes builds the data and iam Connect mux'es plus their
// shared *health.Handler. Pure mux assembly — no http.Server wrapping,
// no port binding. Both `serve api` (which wraps these in TCP listeners)
// and `serve mcp --embedded` (which hands them to the inline transport)
// reuse this function.
//
// One health handler is shared between data and iam so a SIGTERM flips
// both /readyz to 503 simultaneously when the planes co-host.
func AssembleAPIMuxes(ctx context.Context, deps *SharedDeps, meta BuildMeta) (dataMux, iamMux *http.ServeMux, healthH *health.Handler, err error) {
	cfg := deps.Cfg
	l := deps.Logger
	repos := deps.Repos
	storage := deps.Storage
	polEngine := deps.PolEngine

	// ─── Data-plane handlers ─────────────────────────────────────────────
	objH := wire.ProvideObjectHandler(repos, storage, polEngine, deps.CELEval, deps.SM, cfg)
	opH := wire.ProvideOperationHandler(repos, polEngine)
	batchH := wire.ProvideBatchHandler(repos, opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, deps.SM)
	versionH := wire.ProvideVersionHandler(repos, polEngine)
	lockH := wire.ProvideLockHandler(repos, polEngine)
	quotaUpdater := adapters.NewQuotaRepoV2(deps.DB.Queries, deps.Pool)
	objH.SetVersionHandler(versionH)
	objH.SetQuotaUpdater(quotaUpdater)

	// Object lifecycle producer wiring — fans CompleteObject /
	// DeleteObject / RestoreObject / UpdateObject / CopyObject events
	// into event_deliveries. Same shape as the admin builder's
	// dispatcher; we construct a fresh one here because each plane
	// has its own listener scope (the admin builder's dispatcher is
	// not exported across packages, and crossing it would weld the
	// admin and api builders together unnecessarily). NATS pool is
	// lazy — sockets only open if a NATS sink is actually configured
	// for an object event the api plane fires.
	apiDispatcher := &worker.Dispatcher{
		Store:       worker.NewRepoSubscriptionStore(deps.Repos.EventSub),
		Outbox:      worker.PgxOutboxWriter{Pool: deps.Pool},
		NATS:        worker.NewNatsConnPool(l.Named("api-nats-pool")),
		Logger:      l.Named("api-event-dispatcher"),
		MaxAttempts: 3,
	}
	objH.SetEventProducer(apiDispatcher)
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
	bucketBootstrapH.SetEventProducer(apiDispatcher)
	bucketBootstrapH.SetLogger(l.Named("bucket-events"))
	collectionBootstrapH := wire.ProvideCollectionHandler(repos, polEngine, cfg)
	collectionBootstrapH.SetEventProducer(apiDispatcher)
	collectionBootstrapH.SetLogger(l.Named("collection-events"))
	storageBootstrapH := storagebootstraph.NewHandler(bucketBootstrapH, collectionBootstrapH, repos.BucketV2, polEngine)

	// ─── IAM-plane handlers ──────────────────────────────────────────────
	iss, err := wire.ProvideIssuer(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	dec := wire.ProvideRefreshDecoder(cfg)
	authH := wire.ProvideAuthHandler(repos, iss, dec, polEngine).
		WithReuseAudit(repos.Audit, l).
		WithCollectionRoutes(wire.ProvideCollectionRouteLister(repos, polEngine, cfg))
	userH := wire.ProvideUserHandler(repos, polEngine)
	userSettingsH := usersettingsh.NewHandler(
		adapters.NewUserSettingsRepo(deps.DB.Queries),
		repos.IAMUser,
		polEngine,
	)

	// ─── Per-plane interceptor stacks ────────────────────────────────────
	validateInterceptor, err := middleware.ProtoValidate()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("init protovalidate: %w", err)
	}
	verifierData, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceData, l)
	if err != nil {
		return nil, nil, nil, err
	}
	verifierIAM, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceIAM, l)
	if err != nil {
		return nil, nil, nil, err
	}

	// Capability interceptor on the data plane — additive. When the
	// capability subsystem is off (deps.Capability == nil) the helper
	// returns a no-op, so existing JWT-only deploys are unaffected.
	// When a capability token IS supplied, the interceptor stamps
	// *capability.Capability on the context and handlers branch via
	// auth.CapabilityFromContext.
	//
	// IAM plane intentionally has NO capability interceptor: capabilities
	// don't have an "iam" audience (the audiences are data / admin /
	// mcp). User-authn flows that go through iam are not the agent path.
	var capData connect.Interceptor
	if deps.Capability != nil {
		// Same opt-in event-emitter pattern as the admin pod —
		// the api dispatcher fan-out into event_deliveries when
		// cfg.Dispatcher.ChargeEventsEnabled is on. Object lifecycle
		// is the highest-cardinality producer, capability charges
		// are the second; subscribers must filter by event.kind to
		// keep their queue depth bounded.
		var chargeEm auth.ChargeEventEmitter
		if cfg.Dispatcher.ChargeEventsEnabled {
			chargeEm = newChargeEmitter(apiDispatcher, l.Named("charge-events"))
		}
		// Establishing on the data plane: a capability names its tenant and
		// the verifier has checked signature, expiry and revocation, so it is
		// enough to authenticate ITS tenant's object operations. That is what
		// lets a consumer reach a tenant without holding a long-lived
		// credential for it. A capability presented alongside a JWT or API
		// token stays additive — the existing principal wins.
		capData = auth.CapabilityEstablishingInterceptor(
			deps.Capability.Verifier,
			capability.AudiencePlaneData,
			deps.Capability.Usage,
			cfg.Capability.ChargePerRequestAmount,
			cfg.Capability.ChargePerRequestUnit,
			chargeEm,
		)
	} else {
		capData = auth.CapabilityInterceptor(nil, "", nil, 0, "")
	}

	// APIToken interceptor — additive, parallel to capability and JWT.
	// Mounted on data + iam (the public-facing planes); admin gets its
	// own in build_listeners_admin.go. Audience-pinned per plane.
	// Limiter wired so per-token rate caps apply on the data + iam
	// planes (high-QPS surfaces); admin uses the same limiter.
	var apiTokData, apiTokIAM connect.Interceptor
	if deps.APIToken != nil {
		// Data plane: the API-token interceptor ESTABLISHES the principal, so a service
		// can authenticate uploads with a long-lived `paladin_pat_…` key alone
		// (paired with auth.InterceptorSkipAPITokens below). IAM plane stays additive.
		apiTokData = auth.APITokenAuthInterceptor(deps.APIToken.Verifier, deps.APIToken.Limiter, "data")
		apiTokIAM = auth.APITokenInterceptorWithLimiter(deps.APIToken.Verifier, deps.APIToken.Limiter, "iam")
	} else {
		apiTokData = auth.APITokenInterceptor(nil, "")
		apiTokIAM = auth.APITokenInterceptor(nil, "")
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
	otelInt, err := otelconnect.NewInterceptor()
	if err != nil {
		l.Fatal("otelconnect interceptor", zap.Error(err))
	}

	// Per-tenant request rate limit (middleware.rate_limit). The config block
	// has existed since the initial schema with enabled=true; nothing ever read
	// it, so the only limits in force were on login and on api tokens, and a
	// JWT-authenticated client had no ceiling at all. Disabled yields a
	// pass-through rather than an absent interceptor, so the chains below stay
	// the same shape either way.
	tenantRLCfg := middleware.TenantRateLimitConfig{
		Store: adapters.NewTenantRateStore(deps.DB.Queries),
	}
	if cfg.Middleware.RateLimit.Enabled {
		tenantRLCfg.RPS = cfg.Middleware.RateLimit.RequestsPerSecond
	}
	tenantRL := middleware.NewTenantRateLimitInterceptor(tenantRLCfg)

	// Every plane decodes JSON with the strict codec: an unknown request field
	// is a 400, not a silent discard. See internal/api/codec for why the
	// forward-compatibility the default buys is not worth its cost here.
	dataOpts := connect.WithOptions(
		connect.WithCodec(codec.StrictJSON{}),
		connect.WithInterceptors(
			otelInt,
			// Outermost after tracing, and BEFORE auth on purpose: connect
			// applies the first-listed interceptor outermost, so anything
			// installed after auth cannot see auth's own rejections — and a
			// wave of failed authentications leaving no log line was the
			// widest part of this gap. Failures only; successes are otel's job.
			middleware.LogOutcome(l),
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
			// After auth so the tenant is known, and before the quota and
			// idempotency work so a throttled caller is turned away before it
			// costs a database round-trip.
			tenantRL,
			// After otel (span exists) and after auth (principal known), so the
			// request logger carries trace_id, span_id, request_id and tenant_id
			// for every line the handlers write through logger.FromContext.
			middleware.LogContextStreaming(l),
			// Bucket-scoped enforcement is wired here: repos.Object.LookupBucket
			// resolves the upload's Collection to its (backend, bucket) so a
			// bucket quota row can be found. Without WithBucketScope those rows
			// are maintained by the reconciler and shown on /stats but reject
			// nothing. Both scopes cost a point lookup on the upload path —
			// collections by PK, then quotas by its unique index.
			middleware.NewQuotaSoftCheck(repos.Quota).WithBucketScope(repos.Object),
			connect.UnaryInterceptorFunc(validateInterceptor),
			idempotencyInterceptor,
		),
	)
	// Every plane decodes JSON with the strict codec: an unknown request field
	// is a 400, not a silent discard. See internal/api/codec for why the
	// forward-compatibility the default buys is not worth its cost here.
	iamOpts := connect.WithOptions(
		connect.WithCodec(codec.StrictJSON{}),
		connect.WithInterceptors(
			otelInt,
			// Outermost after tracing, and BEFORE auth on purpose: connect
			// applies the first-listed interceptor outermost, so anything
			// installed after auth cannot see auth's own rejections — and a
			// wave of failed authentications leaving no log line was the
			// widest part of this gap. Failures only; successes are otel's job.
			middleware.LogOutcome(l),
			auth.NewPermissiveInterceptor(verifierIAM,
				"Login",
				"RefreshToken",
				"ExchangeAudience",
			),
			apiTokIAM,
			middleware.NewLoginRateLimiter(
				cfg.Auth.LoginRateLimitPerSubjectPerMinute,
				cfg.Auth.LoginRateLimitPerIPPerMinute,
			),
			// Audit IAM mutations (Login, CreateUser, RefreshToken, …).
			// Placed after the permissive interceptor so
			// anonymous/failed Login attempts are still recorded — a
			// credential-misuse breach must leave a server-side trail
			// (SOC 2 / ISO 27001 / PCI). No dispatcher mirror on this plane.
			// Synchronous + crash-durable (ADR-0004): the row commits before
			// the RPC returns, so a kill can't drop a credential-misuse trail.
			middleware.AuditWithMirror(repos.Audit, auth.AudienceIAM, false, nil),
			// Tenant-scoped IAM calls (memberships, password change) are charged
			// to their tenant; Login and RefreshToken carry no tenant yet and are
			// covered by the login limiter above.
			tenantRL,
			// See the data plane: after otel and after auth. On IAM the principal
			// is often absent (Login, RefreshToken are permissive), so these lines
			// carry trace and request id without a tenant — which is correct, not
			// a gap. An anonymous failed login is exactly the line worth finding.
			middleware.LogContextStreaming(l),
			connect.UnaryInterceptorFunc(validateInterceptor),
			idempotencyInterceptor,
		),
	)

	healthH = NewHealthHandler(deps.DB, cfg.Runtime, l).WithRole("api")

	// Capability subsystem — when enabled, verify the issuer signer
	// has a key loaded. Cheap: no I/O, just nil-check on the
	// in-memory signer. Critical=true because the request path
	// short-circuits to "no capability" if the issuer fails, which
	// is a real privilege gap operators need to see.
	if deps.Capability != nil && deps.Capability.Issuer != nil {
		AddSubsystemCheck(healthH, "capability", true, func(ctx context.Context) error {
			if deps.Capability.Issuer == nil {
				return fmt.Errorf("capability issuer not initialised")
			}
			return nil
		})
	} else {
		// Subsystem is off-by-config — register an informational row so
		// the /health page surfaces "capability: disabled" instead of
		// omitting the component entirely. Operators consistently
		// misread an absent row as "missing/broken" rather than "off".
		AddDisabledSubsystem(healthH, "capability")
	}

	// APIToken subsystem mirrors capability: probe when on, surface
	// "disabled" when off so the /health page shows the row.
	if deps.APIToken != nil {
		AddSubsystemCheck(healthH, "api_token", false, func(ctx context.Context) error {
			return nil
		})
	} else {
		AddDisabledSubsystem(healthH, "api_token")
	}

	// The api role serves two listeners — data and iam — and Kubernetes
	// allows one readiness probe per container, which points at data. So iam
	// could stop accepting while the pod stayed Ready, kept its Service
	// endpoint, and failed every login and token exchange behind it: the whole
	// console, since the token exchange rides iam.
	//
	// This closes that by making the readiness the probe DOES reach depend on
	// the listener it does not. A TCP dial is the right depth: it proves the
	// listener is accepting, which is precisely the failure a shared process
	// can have — if the process itself were wedged, this handler would not be
	// answering either.
	//
	// Critical=true: a pod that cannot authenticate anyone has no business in
	// the Service's endpoint list.
	if addr := cfg.API.Server.IAM.Addr; addr != "" {
		iamTLS := cfg.API.Server.IAM.TLS.Enabled
		AddSubsystemCheck(healthH, "iam_listener", true, func(ctx context.Context) error {
			return dialLocalListener(ctx, addr, iamTLS)
		})
	}

	// Storage-backend reachability probes are tracked in BACKLOG —
	// the existing S3 adapter doesn't expose a HEAD/ListBuckets
	// method suited to a sub-second probe. Adding one is a separate
	// PR; until then, storage failures surface as RPC-level errors
	// on PutObject / Presign rather than as /readyz failures.

	dataMux = http.NewServeMux()
	// A procedure this release lacks is a Connect Unimplemented, which
	// carries the release header (see middleware.ServerVersion).
	dataMux.Handle(middleware.UnknownProcedurePattern, middleware.UnknownProcedure())
	healthH.Register(dataMux)
	dataMux.Handle(paladindatav1connect.NewObjectServiceHandler(
		connectdata.NewObjectServer(objH, versionH).WithLocks(lockH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(connectdata.NewMultipartServer(mpH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewPresignServiceHandler(connectdata.NewPresignServer(presignH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewObjectTagServiceHandler(connectdata.NewObjectTagServer(objH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewBatchServiceHandler(connectdata.NewBatchServer(batchH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewOperationServiceHandler(connectdata.NewOperationServer(opH), dataOpts))
	// StorageBootstrapService shares the data-plane interceptor stack
	// (dataOpts) so the api_token principal + RequireAudience(data) apply — an
	// aud=data PAT can reach EnsureTenantStorage.
	dataMux.Handle(paladindatav1connect.NewStorageBootstrapServiceHandler(connectdata.NewStorageBootstrapServer(storageBootstrapH), dataOpts))

	iamMux = http.NewServeMux()
	iamMux.Handle(middleware.UnknownProcedurePattern, middleware.UnknownProcedure())
	healthH.Register(iamMux)
	iamMux.Handle(paladiniamv1connect.NewAuthServiceHandler(connectiam.NewAuthServer(authH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewUserServiceHandler(connectiam.NewUserServer(userH, repos.Tenant), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewHealthServiceHandler(
		connectiam.NewSystemServer(meta.Version, meta.Commit, ParseBuildTime(meta.BuildTime), "api", healthH),
		iamOpts,
	))
	iamMux.Handle(paladiniamv1connect.NewUserSettingsServiceHandler(
		connectiam.NewUserSettingsServer(userSettingsH),
		iamOpts,
	))

	// OAuth 2.1 Authorization Server (ADR-0009): mount the raw-HTTP /oauth/*
	// endpoints on the IAM mux and seed first-party clients. Reuses the same
	// issuer + refresh decoder the Connect AuthService uses, so OAuth-minted
	// bearers are indistinguishable from login-minted ones downstream.
	if cfg.Auth.OAuth.Enabled {
		oauthStore := oauth.NewPgxStore(deps.Pool)
		skipConsent := map[string]bool{}
		for _, sc := range cfg.Auth.OAuth.SeedClients {
			if serr := oauthStore.UpsertClient(ctx, oauth.Client{
				ClientID:         sc.ClientID,
				RedirectURIs:     sc.RedirectURIs,
				AllowedScopes:    sc.AllowedScopes,
				AllowedAudiences: sc.AllowedAudiences,
				Public:           sc.Public,
			}); serr != nil {
				return nil, nil, nil, fmt.Errorf("oauth seed client %q: %w", sc.ClientID, serr)
			}
			if sc.SkipConsent {
				skipConsent[sc.ClientID] = true
			}
		}
		oauth.NewHandler(cfg.Auth.OAuth, oauthStore, repos.IAMUser, repos.IAMRefresh, iss, dec, l).
			WithAuthorizer(polEngine).
			WithAudit(repos.Audit).
			WithSkipConsent(skipConsent).
			Mount(iamMux)
		l.Info("oauth authorization server enabled", zap.Int("seed_clients", len(cfg.Auth.OAuth.SeedClients)))
	}

	return dataMux, iamMux, healthH, nil
}

// BuildAPIListeners materialises the data and iam Connect listeners.
// Wraps the muxes returned by AssembleAPIMuxes in h2c-enabled http.Servers
// bound to cfg.API.Server.Data / IAMHTTP.
func BuildAPIListeners(ctx context.Context, deps *SharedDeps, meta BuildMeta) ([]HTTPListener, *health.Handler, error) {
	dataMux, iamMux, healthH, err := AssembleAPIMuxes(ctx, deps, meta)
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
	iamSrv, err := BuildHTTPServer(cfg.API.Server.IAM, middleware.ServerVersion(meta.Version, iamMux), deps.Logger)
	if err != nil {
		return nil, nil, err
	}
	listeners := []HTTPListener{
		{Plane: "data", Server: dataSrv, TLS: cfg.API.Server.Data.TLS},
		{Plane: "iam", Server: iamSrv, TLS: cfg.API.Server.IAM.TLS},
	}
	return listeners, healthH, nil
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
		ObjectID:     vo.ObjectID,
		TenantID:     vo.TenantID,
		Collection:   vo.Collection,
		Key:          vo.Key,
		ContentType:  vo.ContentType,
		SizeBytes:    vo.SizeBytes,
		ETag:         vo.ETag,
		ChecksumAlgo: vo.ChecksumAlgo,
		Checksum:     vo.Checksum,
		Metadata:     vo.Metadata,
		Tags:         vo.Tags,
	})
}

// dialLocalListener reports whether this process is accepting connections on
// addr's port.
//
// Dials the loopback rather than the address as configured: "0.0.0.0:8085" is
// a bind address, not a destination, and the question is whether this process
// accepts — not how it advertises itself.
//
// A connection is the right depth. It catches a listener that failed to start
// or died while the process lived, which is the failure a second listener in a
// shared process can have on its own; an HTTP round trip would re-test what
// the health handler answering this check has already proved.
//
// On a TLS listener the connection completes the handshake rather than
// hanging up on it. A bare TCP dial is enough to prove the port accepts, but
// closing mid-handshake is indistinguishable from a client that gave up, so
// the server logged "TLS handshake error ... EOF" on every readiness probe —
// six warnings a minute, in the very log where an unexplained handshake error
// was being investigated. The certificate is not verified: this dials itself,
// so identity is not the question and a probe must not start failing on the
// day the cert's SANs change.
func dialLocalListener(ctx context.Context, addr string, useTLS bool) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listener address %q: %w", addr, err)
	}
	target := net.JoinHostPort("127.0.0.1", port)
	d := net.Dialer{Timeout: 2 * time.Second}

	if useTLS {
		td := &tls.Dialer{NetDialer: &d, Config: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- dialling ourselves; identity is not what this probes
		conn, err := td.DialContext(ctx, "tcp", target)
		if err != nil {
			return fmt.Errorf("not accepting TLS on port %s: %w", port, err)
		}
		return conn.Close()
	}

	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return fmt.Errorf("not accepting on port %s: %w", port, err)
	}
	return conn.Close()
}
