package app

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"go.uber.org/zap"

	connectdata "github.com/oleg-tkachuk/paladin/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/internal/capability"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/wire"
	"github.com/oleg-tkachuk/paladin/internal/worker"
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
	batchH := wire.ProvideBatchHandler(opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, deps.SM)
	versionH := wire.ProvideVersionHandler(repos)
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

	// ─── IAM-plane handlers ──────────────────────────────────────────────
	iss, err := wire.ProvideIssuer(cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	dec := wire.ProvideRefreshDecoder(cfg)
	authH := wire.ProvideAuthHandler(repos, iss, dec, polEngine).WithReuseAudit(repos.Audit, l)
	userH := wire.ProvideUserHandler(repos, polEngine)
	apikH := wire.ProvideApiKeyHandler(repos, iss, polEngine)
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
		capData = auth.CapabilityInterceptorWithEvents(
			deps.Capability.Verifier,
			capability.AudiencePlaneData,
			deps.Capability.Usage,
			cfg.API.Server.Data.RealIPHeader,
			cfg.Capability.ChargePerRequestAmount,
			cfg.Capability.ChargePerRequestUnit,
			chargeEm,
		)
	} else {
		capData = auth.CapabilityInterceptor(nil, "", nil, "", 0, "")
	}

	// APIToken interceptor — additive, parallel to capability and JWT.
	// Mounted on data + iam (the public-facing planes); admin gets its
	// own in build_listeners_admin.go. Audience-pinned per plane.
	// Limiter wired so per-token rate caps apply on the data + iam
	// planes (high-QPS surfaces); admin uses the same limiter.
	var apiTokData, apiTokIAM connect.Interceptor
	if deps.APIToken != nil {
		apiTokData = auth.APITokenInterceptorWithLimiter(deps.APIToken.Verifier, deps.APIToken.Limiter, "data")
		apiTokIAM = auth.APITokenInterceptorWithLimiter(deps.APIToken.Verifier, deps.APIToken.Limiter, "iam")
	} else {
		apiTokData = auth.APITokenInterceptor(nil, "")
		apiTokIAM = auth.APITokenInterceptor(nil, "")
	}

	// Shared Idempotency-Key gate (see internal/middleware/idempotency.go).
	// RequireOnCreate=true rejects any Create* RPC without an
	// `Idempotency-Key` header. Both data and iam planes opt in:
	//   - data plane: UploadObject/CompleteObject/... (in the future
	//     Create* MultipartUpload, etc.) — retries on a flaky network
	//     must collapse on the same object.
	//   - iam plane: CreateUser, CreateApiKey — operator double-click
	//     on the admin UI must not duplicate users / leak api keys.
	idempotencyInterceptor := middleware.NewIdempotencyInterceptor(repos.Idempotency, middleware.IdempotencyConfig{
		RequireOnCreate: true,
	})

	// OTel: one span per RPC, named from the procedure, plus RED metrics
	// (rpc duration / count) on every call. First in each chain so the
	// span wraps auth + the handler. No-op spans when OTel is disabled
	// (global providers are noops). Only errors on invalid options, which
	// we don't pass — a failure here is a wiring bug, so fail fast.
	otelInt, err := otelconnect.NewInterceptor()
	if err != nil {
		l.Fatal("otelconnect interceptor", zap.Error(err))
	}

	dataOpts := connect.WithInterceptors(
		otelInt,
		auth.Interceptor(verifierData),
		auth.RequireAudience(auth.AudienceData),
		capData,
		apiTokData,
		middleware.NewQuotaSoftCheck(repos.Quota),
		connect.UnaryInterceptorFunc(validateInterceptor),
		idempotencyInterceptor,
	)
	iamOpts := connect.WithInterceptors(
		otelInt,
		auth.NewPermissiveInterceptor(verifierIAM,
			"Login",
			"RefreshToken",
			"ExchangeAudience",
		),
		apiTokIAM,
		middleware.NewLoginRateLimiter(
			cfg.API.Server.IAM.RealIPHeader,
			cfg.Auth.LoginRateLimitPerSubjectPerMinute,
			cfg.Auth.LoginRateLimitPerIPPerMinute,
		),
		// Audit IAM mutations (Login, CreateUser, CreateApiKey,
		// RefreshToken, …). Placed after the permissive interceptor so
		// anonymous/failed Login attempts are still recorded — a
		// credential-misuse breach must leave a server-side trail
		// (SOC 2 / ISO 27001 / PCI). No dispatcher mirror on this plane.
		// Synchronous + crash-durable (ADR-0004): the row commits before
		// the RPC returns, so a kill can't drop a credential-misuse trail.
		middleware.AuditWithMirror(repos.Audit, auth.AudienceIAM, false, nil),
		connect.UnaryInterceptorFunc(validateInterceptor),
		idempotencyInterceptor,
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

	// Storage-backend reachability probes are tracked in BACKLOG —
	// the existing S3 adapter doesn't expose a HEAD/ListBuckets
	// method suited to a sub-second probe. Adding one is a separate
	// PR; until then, storage failures surface as RPC-level errors
	// on PutObject / Presign rather than as /readyz failures.

	dataMux = http.NewServeMux()
	healthH.Register(dataMux)
	dataMux.Handle(paladindatav1connect.NewObjectServiceHandler(connectdata.NewObjectServer(objH, versionH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(connectdata.NewMultipartServer(mpH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewPresignServiceHandler(connectdata.NewPresignServer(presignH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewObjectTagServiceHandler(connectdata.NewObjectTagServer(objH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewBatchServiceHandler(connectdata.NewBatchServer(batchH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewOperationServiceHandler(connectdata.NewOperationServer(opH), dataOpts))

	iamMux = http.NewServeMux()
	healthH.Register(iamMux)
	iamMux.Handle(paladiniamv1connect.NewAuthServiceHandler(connectiam.NewAuthServer(authH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewUserServiceHandler(connectiam.NewUserServer(userH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewApiKeyServiceHandler(connectiam.NewApiKeyServer(apikH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewSystemServiceHandler(
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
	l := deps.Logger
	listeners := []HTTPListener{
		{Plane: "data", Server: BuildHTTPServer(cfg.API.Server.Data, dataMux, l), TLS: cfg.API.Server.Data.TLS},
		{Plane: "iam", Server: BuildHTTPServer(cfg.API.Server.IAM, iamMux, l), TLS: cfg.API.Server.IAM.TLS},
	}
	return listeners, healthH, nil
}

// multipartVersionAdapter bridges multipart.VersionRecorder onto the object
// VersionHandler so the multipart handler can record promotion events
// without a circular dependency on the object package.
type multipartVersionAdapter struct {
	v *object.VersionHandler
}

func (a *multipartVersionAdapter) OnPromote(ctx context.Context, vo multipart.VersionedObject) error {
	if a.v == nil {
		return nil
	}
	return a.v.OnPromote(ctx, object.Object{
		ObjectID:     vo.ObjectID,
		TenantID:     vo.TenantID,
		ObjectKey:    vo.ObjectKey,
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
