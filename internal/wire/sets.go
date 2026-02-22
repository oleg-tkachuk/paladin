package wire

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	grpcapi "github.com/oleg-tkachuk/paladin/internal/api/grpc"
	httpapi "github.com/oleg-tkachuk/paladin/internal/api/http"
	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/middleware"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/service"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/utils"
	"github.com/oleg-tkachuk/paladin/internal/worker"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/wire"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type Version string
type Commit string
type BuildTime string
type ConfigPath string

type AppMetadata struct {
	Version   string
	Commit    string
	BuildTime string
}

// BootstrapLogger is a type alias to help Wire distinguish between early and late loggers.
type BootstrapLogger *zap.Logger

var ProviderSet = wire.NewSet(
	ProvideConfig,
	ProvideLogger,
	ProvideOTel,
	ProvideDB,
	ProvideS3,
	ProvidePolicy,
	ProvideObjectsRepo,
	ProvideMultipartRepo,
	ProvideIdempotencyRepo,
	ProvideCategoryRepo,
	ProvideAuditRepo,
	ProvideBreakerFactory,
	ProvideObjectsService,
	ProvideHealthService,
	ProvideHTTPServer,
	ProvideGRPCServer,
	ProvideReaper,
	ProvideApp,
)

func ProvideConfig(path ConfigPath, log BootstrapLogger) (config.Config, error) {
	cfg := config.Config{}
	cfg.Env = utils.GetEnvOrDefault("ENV", "local")
	cfg.PodName = utils.GetEnvOrDefault("POD_NAME", "paladin")

	if path != "" {
		c, err := config.Load(string(path), (*zap.Logger)(log))
		if err != nil {
			return config.Config{}, err
		}
		cfg = c
		cfg.Env = utils.GetEnvOrDefault("ENV", cfg.Env)
		cfg.PodName = utils.GetEnvOrDefault("POD_NAME", cfg.PodName)
	}

	return cfg, nil
}

func ProvideLogger(cfg config.Config) (*zap.Logger, error) {
	l, err := logger.New(cfg.Logger)
	if err != nil {
		return nil, err
	}
	logger.ReplaceGlobals(l)
	return l, nil
}

func ProvideOTel(ctx context.Context, cfg config.Config) (observability.ShutdownFunc, error) {
	return observability.InitOTel(ctx, cfg.OTel)
}

func ProvideDB(ctx context.Context, cfg config.Config, l *zap.Logger) (*postgres.DB, func(), error) {
	var db *postgres.DB
	op := func() error {
		d, err := postgres.New(ctx, cfg.Datastores.Postgres, l)
		if err != nil {
			return err
		}
		if err := d.Ping(ctx); err != nil {
			d.Close()
			return err
		}
		db = d
		return nil
	}
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 30 * time.Second

	if err := backoff.Retry(op, b); err != nil {
		return nil, nil, err
	}

	if err := db.RunMigrations(ctx, "/app/migrations"); err != nil {
		l.Warn("Migrations failed", zap.Error(err))
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup, nil
}

func ProvideS3(ctx context.Context, cfg config.Config, l *zap.Logger) (*s3.Client, error) {
	s3c, err := s3.New(ctx, cfg.Datastores.S3, l)
	if err != nil {
		return nil, err
	}
	_ = s3c.EnsureBucket(ctx)
	return s3c, nil
}

func ProvidePolicy(cfg config.Config) domain.Policy {
	return service.NewPolicy(cfg.Policy)
}

func ProvideObjectsRepo(db *postgres.DB) domain.ObjectsRepository {
	return postgres.NewObjectsRepo(db)
}

func ProvideMultipartRepo(db *postgres.DB) domain.MultipartRepository {
	return postgres.NewMultipartRepo(db)
}

func ProvideIdempotencyRepo(db *postgres.DB) domain.IdempotencyRepository {
	return postgres.NewIdempotencyRepo(db)
}

func ProvideCategoryRepo(db *postgres.DB) domain.CategoryRepository {
	return postgres.NewCategoryRepo(db)
}

func ProvideAuditRepo(db *postgres.DB) domain.AuditLogRepository {
	return postgres.NewAuditLogRepo(db)
}

func ProvideBreakerFactory(cfg config.Config) breaker.Factory {
	return breaker.NewFactory(cfg)
}

func ProvideObjectsService(
	objRepo domain.ObjectsRepository,
	mpRepo domain.MultipartRepository,
	s3c *s3.Client,
	policy domain.Policy,
	idemRepo domain.IdempotencyRepository,
	catRepo domain.CategoryRepository,
	cfg config.Config,
) domain.ObjectsService {
	return service.NewObjectsService(
		objRepo,
		mpRepo,
		s3c,
		policy,
		idemRepo,
		catRepo,
		cfg.Datastores.S3.PartSizeBytes,
		cfg.Timeouts.FastOperation,
		cfg.Timeouts.DefaultOperation,
		cfg.Timeouts.S3Operation,
		cfg.Timeouts.LongOperation,
		cfg.Idempotency.TTL,
	)
}

func ProvideHealthService(db *postgres.DB, s3c *s3.Client, brk breaker.Factory) *service.HealthService {
	return service.NewHealthService(db, s3c, brk)
}

func ProvideHTTPServer(
	cfg config.Config,
	l *zap.Logger,
	svc domain.ObjectsService,
	auditRepo domain.AuditLogRepository,
	hs *service.HealthService,
	appStarted *atomic.Bool,
	meta AppMetadata,
) *httpapi.Server {
	return httpapi.NewServer(&cfg, l, svc, auditRepo, meta.Version, meta.Commit, meta.BuildTime, hs, appStarted)
}

func ProvideGRPCServer(cfg config.Config, l *zap.Logger, svc domain.ObjectsService) *grpc.Server {
	interceptors := middleware.SetupGRPCInterceptors(&cfg, l)
	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(interceptors...),
	)
	grpcapi.RegisterPaladinServer(srv, grpcapi.NewServer(l, svc))
	reflection.Register(srv)
	return srv
}

func ProvideReaper(cfg config.Config, objRepo domain.ObjectsRepository, mpRepo domain.MultipartRepository, auditRepo domain.AuditLogRepository, s3c *s3.Client, l *zap.Logger) *worker.Reaper {
	return worker.NewReaper(cfg.Housekeeping, objRepo, mpRepo, auditRepo, s3c, l)
}

func ProvideApp(
	meta AppMetadata,
	cfg config.Config,
	l *zap.Logger,
	grpcSrv *grpc.Server,
	httpapiSrv *httpapi.Server,
	db *postgres.DB,
	otelShutdown observability.ShutdownFunc,
	reaper *worker.Reaper,
	started *atomic.Bool,
) (*app.App, func()) {
	httpSrv := &http.Server{
		Addr:              cfg.Server.HTTP.Addr,
		Handler:           httpapiSrv.Handler(),
		ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.HTTP.ReadTimeout,
		WriteTimeout:      cfg.Server.HTTP.WriteTimeout,
		IdleTimeout:       cfg.Server.HTTP.IdleTimeout,
	}

	a := app.NewContainer(
		meta.Version, meta.Commit, meta.BuildTime,
		cfg, l, httpSrv, grpcSrv, db, otelShutdown, reaper, started,
	)

	cleanup := func() {
		a.Shutdown()
	}

	return a, cleanup
}
