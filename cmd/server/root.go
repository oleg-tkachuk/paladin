package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/api/connectshim/admin"
	connectdata "github.com/oleg-tkachuk/paladin/internal/api/connectshim/data"
	connectiam "github.com/oleg-tkachuk/paladin/internal/api/connectshim/iam"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/multipart"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/app"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/wire"
	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/oleg-tkachuk/paladin/migrations"
)

const (
	defaultConfigPath    = "/app/configs/config.yaml"
	defaultShutdownGrace = 15 * time.Second
)

var (
	configPath string
	//nolint:unused
	version = "dev"
	//nolint:unused
	commit = "none"
	//nolint:unused
	buildTime = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "paladin",
	Short: "Start the Paladin service",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		bootstrap, err := logger.NewBootstrapLogger()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build bootstrap logger: %v\n", err)
			os.Exit(1)
		}

		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}

		cfg, err := config.Load(configPath, bootstrap)
		if err != nil {
			bootstrap.Fatal("failed to load config", zap.Error(err))
		}

		l, err := logger.New(cfg.Logger)
		if err != nil {
			bootstrap.Fatal("failed to build logger", zap.Error(err))
		}
		logger.ReplaceGlobals(l)

		db, err := postgres.New(ctx, cfg.Datastores.Postgres, l.Named("postgres"))
		if err != nil {
			l.Fatal("failed to connect to database", zap.Error(err))
		}
		defer db.Close()

		if err := db.Ping(ctx); err != nil {
			l.Fatal("failed to ping database", zap.Error(err))
		}

		// All migrations now run in one phase — v2 schema is self-contained.
		if err := db.RunMigrations(ctx, migrations.FS); err != nil && !errors.Is(err, context.Canceled) {
			l.Fatal("failed to apply migrations", zap.Error(err))
		}

		listeners, err := buildListeners(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("failed to assemble server", zap.Error(err))
		}

		started := &atomic.Bool{}
		jobs := buildBackgroundJobs(cfg, db, l)
		container := app.NewContainer(version, commit, buildTime, cfg, l, listeners, db, nil, jobs, started)

		l.Info("starting",
			zap.String("version", version),
			zap.String("commit", commit),
			zap.String("build_time", buildTime),
		)

		runErr := make(chan error, 1)
		go func() { runErr <- container.Run() }()

		select {
		case <-ctx.Done():
			l.Info("shutdown signal received")
		case err := <-runErr:
			l.Error("http server failed", zap.Error(err))
		}

		container.Shutdown()
	},
}

// buildListeners assembles repositories, storage, policy engine, handlers,
// and three Connect mux'es (data / admin / iam) into independent HTTP
// servers. Each plane gets its own audience verifier + interceptor stack.
func buildListeners(ctx context.Context, cfg config.Config, db *postgres.DB, l *zap.Logger) ([]app.HTTPListener, error) {
	pool, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		return nil, errors.New("DB.Pool is not *pgxpool.Pool")
	}

	if cfg.Auth.SigningKey == "" {
		return nil, errors.New("auth.signing_key (or signing_key_secret) is required")
	}

	defaultName := cfg.Storage.DefaultBackend
	if defaultName == "" {
		return nil, errors.New("storage.default_backend not set")
	}
	backend, ok := cfg.Storage.Backends[defaultName]
	if !ok {
		return nil, fmt.Errorf("storage.backends.%s not configured", defaultName)
	}
	s3c, err := s3adapter.New(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("s3 adapter: %w", err)
	}

	// ─── Repositories (legacy v1 + v2 IAM/admin) ─────────────────────────
	repos := wire.Repos{
		Object:        adapters.NewObjectRepo(db.Queries, pool),
		ObjectKey:     adapters.NewObjectKeyRepo(db.Queries, pool),
		Bucket:        adapters.NewBucketRepo(db.Queries),
		Tenant:        adapters.NewTenantRepo(db.Queries, pool),
		ObjectTag:     adapters.NewObjectTagRepo(db.Queries),
		Presign:       adapters.NewPresignRepo(db.Queries, pool),
		Multipart:     adapters.NewMultipartRepo(db.Queries, pool),
		Operation:     adapters.NewOperationRepo(db.Queries),
		BackendV2:     adapters.NewBackendRepoV2(db.Queries),
		BucketV2:      adapters.NewBucketRepoV2(db.Queries),
		Audit:         adapters.NewAuditRepoV2(db.Queries),
		Quota:         adapters.NewQuotaRepoV2(db.Queries),
		EventSub:      adapters.NewEventSubscriptionRepoV2(db.Queries),
		IAMUser:       adapters.NewUserRepo(db.Queries),
		IAMApiKey:     adapters.NewApiKeyRepo(db.Queries),
		IAMRefresh:    adapters.NewRefreshTokenRepo(db.Queries),
		ObjectVersion: adapters.NewObjectVersionRepo(db.Queries),
	}
	storage := wire.Storage{
		Object:      s3c,
		Multipart:   s3c,
		Presign:     s3c.Presign(),
		Stream:      s3c,
		Provisioner: s3c,
	}

	// ─── Cedar engine + state machine + CEL ──────────────────────────────
	polStore := policy.NewPostgresStore(pool)
	polEngine := policy.NewEngine(polStore, cfg.Cedar.PolicyCacheTTL)
	if err := polEngine.Start(ctx); err != nil {
		return nil, fmt.Errorf("policy engine start: %w", err)
	}
	sm := statemachine.New(pool)
	celEval := cel.NewEvaluator()

	// ─── Handlers ────────────────────────────────────────────────────────
	objH := wire.ProvideObjectHandler(repos, storage, polEngine, celEval, sm, cfg)
	objectKeyH := wire.ProvideObjectKeyHandler(repos, polEngine, cfg)
	tenantH := wire.ProvideTenantHandler(repos, polEngine)
	opH := wire.ProvideOperationHandler(repos)
	batchH := wire.ProvideBatchHandler(opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, sm)
	policyH := wire.ProvidePolicyHandler(polEngine, polStore)
	versionH := wire.ProvideVersionHandler(repos)
	quotaUpdater := adapters.NewQuotaRepoV2(db.Queries)
	objH.SetVersionHandler(versionH)
	objH.SetQuotaUpdater(quotaUpdater)
	mpH.SetVersionRecorder(&multipartVersionAdapter{v: versionH})
	mpH.SetQuotaUpdater(quotaUpdater)

	iss, err := wire.ProvideIssuer(cfg)
	if err != nil {
		return nil, err
	}
	dec := wire.ProvideRefreshDecoder(cfg)
	authH := wire.ProvideAuthHandler(repos, iss, dec, polEngine)
	userH := wire.ProvideUserHandler(repos, polEngine)
	apikH := wire.ProvideApiKeyHandler(repos, iss, polEngine)

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

	// ─── Per-plane interceptor stacks ────────────────────────────────────
	validateInterceptor, err := middleware.ProtoValidate()
	if err != nil {
		return nil, fmt.Errorf("init protovalidate: %w", err)
	}
	verifierData, err := buildVerifier(ctx, cfg.Auth, auth.AudienceData, l)
	if err != nil {
		return nil, err
	}
	verifierAdmin, err := buildVerifier(ctx, cfg.Auth, auth.AudienceAdmin, l)
	if err != nil {
		return nil, err
	}
	verifierIAM, err := buildVerifier(ctx, cfg.Auth, auth.AudienceIAM, l)
	if err != nil {
		return nil, err
	}

	// Audit middleware — uses repos.Audit. Skipped on data plane (volume).
	auditMW := middleware.Audit(repos.Audit, "", false) // audience set per-plane below

	dataOpts := connect.WithInterceptors(
		auth.Interceptor(verifierData),
		auth.RequireAudience(auth.AudienceData),
		middleware.NewQuotaSoftCheck(repos.Quota),
		connect.UnaryInterceptorFunc(validateInterceptor),
	)
	adminOpts := connect.WithInterceptors(
		auth.Interceptor(verifierAdmin),
		auth.RequireAudience(auth.AudienceAdmin),
		connect.UnaryInterceptorFunc(validateInterceptor),
		middleware.Audit(repos.Audit, auth.AudienceAdmin, false),
	)
	// IAM mux has unauthenticated RPCs (Login, RefreshToken) and gated ones
	// (WhoAmI, UserService.*, ApiKeyService.*). PermissiveInterceptor passes
	// through when no Authorization header is present — gated RPCs reject at
	// the role/audience layer because PrincipalFromContext returns no user.
	iamOpts := connect.WithInterceptors(
		auth.NewPermissiveInterceptor(verifierIAM,
			"Login",
			"RefreshToken",
		),
		middleware.NewLoginRateLimiter(),
		connect.UnaryInterceptorFunc(validateInterceptor),
	)
	_ = auditMW // appended in adminOpts directly

	// ─── Mux assembly ────────────────────────────────────────────────────
	dataMux := http.NewServeMux()
	addProbes(dataMux, db)
	dataMux.Handle(paladindatav1connect.NewObjectServiceHandler(connectdata.NewObjectServer(objH, versionH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewMultipartUploadServiceHandler(connectdata.NewMultipartServer(mpH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewPresignServiceHandler(connectdata.NewPresignServer(presignH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewObjectTagServiceHandler(connectdata.NewObjectTagServer(objH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewBatchServiceHandler(connectdata.NewBatchServer(batchH), dataOpts))
	dataMux.Handle(paladindatav1connect.NewOperationServiceHandler(connectdata.NewOperationServer(opH), dataOpts))

	adminMux := http.NewServeMux()
	addProbes(adminMux, db)
	adminMux.Handle(paladinadminv1connect.NewBackendServiceHandler(admin.NewBackendServer(backendH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewBucketServiceHandler(admin.NewBucketServer(bucketV2H), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewTenantServiceHandler(admin.NewTenantServer(tenantH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewObjectKeyServiceHandler(admin.NewObjectKeyServer(objectKeyH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewPolicyServiceHandler(admin.NewPolicyServer(policyH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewOperationServiceHandler(admin.NewOperationServer(opH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewQuotaServiceHandler(admin.NewQuotaServer(quotaH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewAuditLogServiceHandler(admin.NewAuditServer(auditH), adminOpts))
	adminMux.Handle(paladinadminv1connect.NewEventSubscriptionServiceHandler(admin.NewEventSubscriptionServer(eventSubH), adminOpts))

	iamMux := http.NewServeMux()
	addProbes(iamMux, db)
	iamMux.Handle(paladiniamv1connect.NewAuthServiceHandler(connectiam.NewAuthServer(authH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewUserServiceHandler(connectiam.NewUserServer(userH), iamOpts))
	iamMux.Handle(paladiniamv1connect.NewApiKeyServiceHandler(connectiam.NewApiKeyServer(apikH), iamOpts))

	// ─── HTTP servers ────────────────────────────────────────────────────
	return []app.HTTPListener{
		{Plane: "data", Server: buildHTTPServer(cfg.Server.DataHTTP, dataMux, l), TLS: cfg.Server.DataHTTP.TLS},
		{Plane: "admin", Server: buildHTTPServer(cfg.Server.AdminHTTP, adminMux, l), TLS: cfg.Server.AdminHTTP.TLS},
		{Plane: "iam", Server: buildHTTPServer(cfg.Server.IAMHTTP, iamMux, l), TLS: cfg.Server.IAMHTTP.TLS},
	}, nil
}

// addProbes attaches /livez, /readyz, /startupz to a mux. All three planes
// expose the same probes so K8s liveness/readiness can target each.
func addProbes(mux *http.ServeMux, db *postgres.DB) {
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /startupz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func buildHTTPServer(c config.HTTPServer, mux http.Handler, l *zap.Logger) *http.Server {
	handler := h2c.NewHandler(mux, &http2.Server{})
	return &http.Server{
		Addr:              c.Addr,
		Handler:           handler,
		ReadHeaderTimeout: c.ReadHeaderTimeout,
		ReadTimeout:       c.ReadTimeout,
		WriteTimeout:      c.WriteTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    c.MaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return logger.WithContext(context.Background(), l)
		},
	}
}

// buildVerifier produces a TokenVerifier pinned to a specific audience.
// JWKS-mode wins when `auth.jwks_url` is set (federated IdP); otherwise
// HMAC-mode against `auth.signing_key`.
func buildVerifier(ctx context.Context, a config.Auth, audience string, l *zap.Logger) (auth.TokenVerifier, error) {
	if a.JWKSURL != "" {
		v := auth.NewJWKSVerifier(a.JWKSURL)
		v.ExpectedIssuer = a.Issuer
		v.ExpectedAudience = audience
		v.Leeway = a.Leeway
		if err := v.Start(ctx); err != nil {
			return nil, fmt.Errorf("jwks(%s): %w", audience, err)
		}
		l.Info("using jwks verifier", zap.String("audience", audience), zap.String("url", a.JWKSURL))
		return v, nil
	}
	return &auth.JWTVerifier{
		Key:              []byte(a.SigningKey),
		ExpectedIssuer:   a.Issuer,
		ExpectedAudience: audience,
		Leeway:           a.Leeway,
	}, nil
}

func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Path to config YAML file")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// buildBackgroundJobs assembles the worker fan: refresh-token purger,
// api-key expirer, and audit log retention. Reconciler is intentionally not
// started here yet — slice 11 will revive its wiring once the StorageProbe
// adapter lands. Returns an empty slice in dev when the housekeeping config
// is missing.
func buildBackgroundJobs(cfg config.Config, db *postgres.DB, l *zap.Logger) []app.BackgroundJob {
	out := []app.BackgroundJob{
		&worker.RefreshTokenPurger{
			Repo:     adapters.NewRefreshTokenRepo(db.Queries),
			Interval: cfg.Workers.RefreshTokenReap.Interval,
			Logger:   l.Named("refresh-purger"),
		},
		&worker.ApiKeyExpirer{
			Repo:     &apiKeyExpirerAdapter{r: adapters.NewApiKeyRepo(db.Queries)},
			Interval: cfg.Workers.ApiKeyReap.Interval,
			Logger:   l.Named("api-key-expirer"),
		},
	}
	if cfg.Workers.Lifecycle.Enabled {
		out = append(out, &worker.LifecycleWorker{
			Buckets:      adapters.NewLifecycleSource(db.Queries),
			Objects:      adapters.NewLifecycleObjectIter(db.Queries),
			SoftDeleter:  statemachine.New(db.Pool.(*pgxpool.Pool)),
			CELEvaluator: cel.NewEvaluator(),
			Interval:     cfg.Workers.Lifecycle.Interval,
			Logger:       l.Named("lifecycle"),
		})
	}

	// Replication worker — dry-run until the StorageReplicator implementation
	// lands. Walks objects in replicated buckets and logs intent without
	// actually copying. Operators can flip to live mode by injecting a real
	// replicator from internal/storage in slice 16.
	if cfg.Workers.Replication.Enabled {
		out = append(out, &worker.ReplicationWorker{
			Buckets:        adapters.NewLifecycleSource(db.Queries),
			Objects:        adapters.NewLifecycleObjectIter(db.Queries),
			Replicator:     nil, // dry-run
			Watermarks:     adapters.NewReplicationWatermarkRepo(db.Queries),
			Interval:       cfg.Workers.Replication.Interval,
			LookbackWindow: cfg.Workers.Replication.LookbackWindow,
			Logger:         l.Named("replication"),
		})
	}

	if cfg.Workers.Reconciler.Interval > 0 {
		s3c, err := s3adapter.New(context.Background(), cfg.Storage.Backends[cfg.Storage.DefaultBackend])
		if err == nil {
			out = append(out, worker.NewReconcilerV2(
				statemachine.New(db.Pool.(*pgxpool.Pool)),
				adapters.NewReconcilerProbe(db.Queries, s3c),
				worker.ReconcilerV2Config{
					PollInterval:    cfg.Workers.Reconciler.Interval,
					PendingGraceTTL: cfg.Workers.Reconciler.MinObjectAge,
					BatchSize:       cfg.Workers.Reconciler.BatchSize,
				},
				l.Named("reconciler"),
			))
		} else {
			l.Warn("skipping reconciler", zap.String("reason", "s3 adapter init failed"), zap.Error(err))
		}
	}
	if cfg.Workers.Housekeeping.AuditLogTTL > 0 {
		out = append(out, &worker.AuditLogPurger{
			Purger:   adapters.NewAuditRepoV2(db.Queries),
			TTL:      cfg.Workers.Housekeeping.AuditLogTTL,
			Interval: cfg.Workers.Housekeeping.Interval,
			Logger:   l.Named("audit-purger"),
		})
	}
	return out
}

// apiKeyExpirerAdapter narrows *adapters.ApiKeyRepo down to the
// worker.ApiKeyExpirerRepo two-method seam. Keeps the worker package free
// of a heavyweight import.
type apiKeyExpirerAdapter struct {
	r *adapters.ApiKeyRepo
}

func (a *apiKeyExpirerAdapter) ListExpired(ctx context.Context, at time.Time, limit int32) ([]authstore.ApiKey, error) {
	return a.r.ListExpired(ctx, at, limit)
}

func (a *apiKeyExpirerAdapter) Revoke(ctx context.Context, id uuid.UUID) error {
	return a.r.Revoke(ctx, id)
}

// multipartVersionAdapter bridges multipart.VersionRecorder onto the object
// VersionHandler. Mirrors the per-package indirection pattern (eventSub →
// worker.SubscriptionStore) used elsewhere in this file.
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

// eventSubStoreAdapter exposes admindomain.EventSubscriptionRepository under
// the worker.SubscriptionStore interface (List-only).
type eventSubStoreAdapter struct {
	r admindomain.EventSubscriptionRepository
}

func (a eventSubStoreAdapter) List(ctx context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	return a.r.List(ctx, args)
}

// silence unused
var _ = issuer.New
var _ = defaultShutdownGrace
