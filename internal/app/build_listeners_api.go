package app

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	connectdata "github.com/oleg-tkachuk/paladin/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/wire"
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

// BuildAPIListeners materialises the data and iam Connect listeners.
// They share one health.Handler so a SIGTERM flips both /readyz to 503
// at the same instant, giving kube-proxy a single window to drain
// in-flight traffic before listeners go away.
//
// Returns the listener slice plus the shared *health.Handler so the
// caller can register it on the App container and call MarkShuttingDown
// during graceful shutdown.
func BuildAPIListeners(ctx context.Context, deps *SharedDeps, meta BuildMeta) ([]HTTPListener, *health.Handler, error) {
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
	quotaUpdater := adapters.NewQuotaRepoV2(deps.DB.Queries)
	objH.SetVersionHandler(versionH)
	objH.SetQuotaUpdater(quotaUpdater)
	mpH.SetVersionRecorder(&multipartVersionAdapter{v: versionH})
	mpH.SetQuotaUpdater(quotaUpdater)

	// ─── IAM-plane handlers ──────────────────────────────────────────────
	iss, err := wire.ProvideIssuer(cfg)
	if err != nil {
		return nil, nil, err
	}
	dec := wire.ProvideRefreshDecoder(cfg)
	authH := wire.ProvideAuthHandler(repos, iss, dec, polEngine)
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
		return nil, nil, fmt.Errorf("init protovalidate: %w", err)
	}
	verifierData, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceData, l)
	if err != nil {
		return nil, nil, err
	}
	verifierIAM, err := BuildVerifier(ctx, cfg.Auth, auth.AudienceIAM, l)
	if err != nil {
		return nil, nil, err
	}

	dataOpts := connect.WithInterceptors(
		auth.Interceptor(verifierData),
		auth.RequireAudience(auth.AudienceData),
		middleware.NewQuotaSoftCheck(repos.Quota),
		connect.UnaryInterceptorFunc(validateInterceptor),
	)
	// IAM mux has unauthenticated RPCs (Login, RefreshToken, ExchangeAudience);
	// PermissiveInterceptor passes through when no Authorization header is
	// present and gated RPCs reject at the role/audience layer.
	iamOpts := connect.WithInterceptors(
		auth.NewPermissiveInterceptor(verifierIAM,
			"Login",
			"RefreshToken",
			"ExchangeAudience",
		),
		middleware.NewLoginRateLimiter(),
		connect.UnaryInterceptorFunc(validateInterceptor),
	)

	// ─── Shared health handler (data + iam) ──────────────────────────────
	healthH := NewHealthHandler(deps.DB, cfg.Server, l)

	dataMux := http.NewServeMux()
	healthH.Register(dataMux)
	dataMux.Handle(paladindatav1connect.NewObjectServiceHandler(connectdata.NewObjectServer(objH, versionH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(connectdata.NewMultipartServer(mpH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewPresignServiceHandler(connectdata.NewPresignServer(presignH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewObjectTagServiceHandler(connectdata.NewObjectTagServer(objH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewBatchServiceHandler(connectdata.NewBatchServer(batchH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewOperationServiceHandler(connectdata.NewOperationServer(opH), dataOpts))

	iamMux := http.NewServeMux()
	healthH.Register(iamMux)
	iamMux.Handle(paladiniamv1connect.NewAuthServiceHandler(connectiam.NewAuthServer(authH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewUserServiceHandler(connectiam.NewUserServer(userH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewApiKeyServiceHandler(connectiam.NewApiKeyServer(apikH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewSystemServiceHandler(
		connectiam.NewSystemServer(meta.Version, meta.Commit, ParseBuildTime(meta.BuildTime), healthH),
		iamOpts,
	))
	iamMux.Handle(paladiniamv1connect.NewUserSettingsServiceHandler(
		connectiam.NewUserSettingsServer(userSettingsH),
		iamOpts,
	))

	listeners := []HTTPListener{
		{Plane: "data", Server: BuildHTTPServer(cfg.Server.DataHTTP, dataMux, l), TLS: cfg.Server.DataHTTP.TLS},
		{Plane: "iam", Server: BuildHTTPServer(cfg.Server.IAMHTTP, iamMux, l), TLS: cfg.Server.IAMHTTP.TLS},
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
