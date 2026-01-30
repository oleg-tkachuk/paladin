package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	grpcapi "paladin/internal/api/grpc"
	httpapi "paladin/internal/api/http"
	"paladin/internal/breaker"
	"paladin/internal/config"
	"paladin/internal/logger"
	"paladin/internal/middleware"
	"paladin/internal/observability"
	"paladin/internal/service"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"
	"paladin/internal/utils"
	"paladin/internal/worker"

	"github.com/cenkalti/backoff/v4"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

type App struct {
	Version   string
	Commit    string
	BuildTime string

	Cfg    config.Config
	Logger *zap.Logger

	httpSrv *http.Server
	grpcSrv *grpc.Server

	db           *postgres.DB
	otelShutdown observability.ShutdownFunc
	started      atomic.Bool

	reaper       *worker.Reaper
	reaperCtx    context.Context
	reaperCancel context.CancelFunc
}

func New(version, commit, buildTime, configPath string) (*App, error) {
	boot := logger.NewBootstrapLogger()

	// If config file doesn't exist, allow env-only mode (useful for containers)
	cfg := config.Config{}
	cfg.Env = utils.GetEnvOrDefault("ENV", "local")
	cfg.PodName = utils.GetEnvOrDefault("POD_NAME", "paladin")

	// Load YAML if present
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return nil, fmt.Errorf("config file stat: %w", err)
		}
		var err error
		cfg, err = config.Load(configPath, boot)
		if err != nil {
			return nil, fmt.Errorf("config load: %w", err)
		}
		cfg.Env = utils.GetEnvOrDefault("ENV", cfg.Env)
		cfg.PodName = utils.GetEnvOrDefault("POD_NAME", cfg.PodName)
	}

	log, err := logger.New(cfg.Logger, map[string]string{
		"service":    cfg.Server.Name,
		"pod":        cfg.PodName,
		"env":        cfg.Env,
		"version":    version,
		"commit":     commit,
		"build_time": buildTime,
	})
	if err != nil {
		return nil, fmt.Errorf("logger init: %w", err)
	}
	logger.ReplaceGlobals(log)

	log.Info("Service metadata",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("build_time", buildTime),
		zap.String("pod", cfg.PodName),
		zap.String("env", cfg.Env),
	)

	ctx := context.Background()

	otelShutdown, err := observability.InitOTel(ctx, cfg.OTel)
	if err != nil {
		return nil, err
	}

	// Environment overrides for technical fields (DB/S3)
	if v := utils.GetEnvOrDefault("DB_DSN", ""); v != "" {
		cfg.Postgres.DSN = v
	}

	if v := utils.GetEnvOrDefault("S3_BUCKET", ""); v != "" {
		cfg.S3.Bucket = v
	}
	if v := utils.GetEnvOrDefault("S3_REGION", ""); v != "" {
		cfg.S3.Region = v
	}
	if v := utils.GetEnvOrDefault("S3_ENDPOINT", ""); v != "" {
		cfg.S3.Endpoint = v
	}
	if v := utils.GetEnvOrDefault("S3_PUBLIC_ENDPOINT", ""); v != "" {
		cfg.S3.PublicEndpoint = v
	}
	if v := utils.GetEnvOrDefault("S3_ACCESS_KEY", ""); v != "" {
		cfg.S3.AccessKey = v
	}
	if v := utils.GetEnvOrDefault("S3_SECRET_KEY", ""); v != "" {
		cfg.S3.SecretKey = v
	}
	if v := utils.GetEnvOrDefault("S3_FORCE_PATH_STYLE", ""); v != "" {
		cfg.S3.ForcePathStyle = v == "true" || v == "1"
	}
	if v := utils.GetEnvOrDefault("S3_PRESIGN_TTL", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.S3.PresignTTL = d
		}
	}
	if v := utils.GetEnvOrDefault("S3_PART_SIZE", ""); v != "" {
		cfg.S3.PartSizeRaw = v
		if n, err := utils.ParseSizeString(v); err == nil {
			cfg.S3.PartSizeBytes = n
		}
	}

	if v := utils.GetEnvOrDefault("POLICY_ALLOWED_CONTENT_TYPES", ""); v != "" {
		parts := strings.Split(v, ",")
		var cleaned []string
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				cleaned = append(cleaned, t)
			}
		}
		if len(cleaned) > 0 {
			cfg.Policy.AllowedContentTypes = cleaned
		}
	}

	// DB connect with retries (production-friendly)
	var db *postgres.DB

	op := func() error {
		log.Debug("Connecting to PostgreSQL", zap.String("dsn", cfg.Postgres.DSN))
		d, err := postgres.New(ctx, cfg.Postgres, log)
		if err != nil {
			return err
		}
		// quick ping
		if err := d.Pool.Ping(ctx); err != nil {
			d.Close()

			return err
		}

		db = d

		return nil
	}
	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 30 * time.Second

	if err := backoff.Retry(op, b); err != nil {
		return nil, err
	}

	if err := db.RunMigrations(ctx, "/app/migrations"); err != nil {
		// In local mode we ship migrations into container; in dev you can mount.
		log.Warn("Migrations failed (check volume mount if running locally)", zap.Error(err))
	}

	s3c, err := s3.New(ctx, cfg.S3, log)
	if err != nil {
		return nil, err
	}

	_ = s3c.EnsureBucket(ctx)

	policy := service.NewPolicy(cfg.Policy)
	objRepo := postgres.NewObjectsRepo(db)
	mpRepo := postgres.NewMultipartRepo(db)
	brk := breaker.NewFactory(cfg)

	svc := service.NewObjectsService(policy, s3c, objRepo, mpRepo, brk, cfg.S3.PartSizeBytes)
	hs := service.NewHealthService(db, s3c, brk)

	app := &App{
		Version: version, Commit: commit, BuildTime: buildTime,
		Cfg: cfg, Logger: log,
		db:           db,
		otelShutdown: otelShutdown,
	}

	// Import middleware for gRPC interceptors
	// Note: We need to import middleware package.
	// Since we can't add imports easily without knowing current imports block position,
	// we will assume we added the import or add it in a separate step if needed.
	// But ReplaceFileContent doesn't support adding imports easily unless we replace the whole imports block.
	// Wait, I can just use paladin/internal/middleware if it's already imported?
	// It is NOT imported in app.go yet.

	httpSrv := httpapi.NewServer(&cfg, log, svc, version, commit, buildTime, hs, &app.started)

	// HTTP server
	app.httpSrv = &http.Server{
		Addr:              cfg.Server.HTTP.Addr,
		Handler:           httpSrv.Handler(),
		ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.HTTP.ReadTimeout,
		WriteTimeout:      cfg.Server.HTTP.WriteTimeout,
		IdleTimeout:       cfg.Server.HTTP.IdleTimeout,
	}

	// gRPC server
	// Setup interceptor chain
	// We need 'paladin/internal/middleware' imported.
	// I will add the import in a separate tool call to be safe, or just use full path if I could (but Go doesn't allow that).
	// I'll assume I'll add the import first.

	// For now, let's fix the httpSrv call and add the interceptor logic, assuming imports will be fixed.
	interceptors := middleware.SetupGRPCInterceptors(&cfg, log)

	app.grpcSrv = grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(interceptors...),
	)
	grpcapi.RegisterPaladinServer(app.grpcSrv, grpcapi.NewServer(log, svc))
	reflection.Register(app.grpcSrv)

	// Reaper
	rpr := worker.NewReaper(cfg.Housekeeping, objRepo, mpRepo, s3c, log)
	// Context for reaper
	rCtx, rCancel := context.WithCancel(context.Background())

	app.reaper = rpr
	app.reaperCtx = rCtx
	app.reaperCancel = rCancel

	return app, nil
}

func (a *App) Run() error {
	a.Logger.Info("Starting gRPC server", zap.String("addr", a.Cfg.Server.GRPC.Addr))

	errCh := make(chan error, 2)

	go func() {
		lc := net.ListenConfig{}
		ln, err := lc.Listen(context.Background(), "tcp", a.Cfg.Server.GRPC.Addr)
		if err != nil {
			errCh <- err

			return
		}
		errCh <- a.grpcSrv.Serve(ln)
	}()

	go func() {
		if a.Cfg.Server.HTTP.TLS.Enabled {
			a.Logger.Info("Starting HTTPS server with TLS",
				zap.String("addr", a.httpSrv.Addr),
				zap.String("cert_path", a.Cfg.Server.HTTP.TLS.CertPath),
			)
			if err := a.httpSrv.ListenAndServeTLS(a.Cfg.Server.HTTP.TLS.CertPath, a.Cfg.Server.HTTP.TLS.KeyPath); err != nil && err != http.ErrServerClosed {
				a.Logger.Fatal("HTTPS server listen failed", zap.Error(err))
			}
		} else {
			a.Logger.Info("Starting HTTP server", zap.String("addr", a.httpSrv.Addr))
			if err := a.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				a.Logger.Fatal("HTTP server listen failed", zap.Error(err))
			}
		}
	}()

	// Start Reaper
	if a.reaper != nil {
		go a.reaper.Start(a.reaperCtx)
	}

	a.started.Store(true)

	return <-errCh
}

func (a *App) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
	defer cancel()

	a.Logger.Info("Shutting down...")

	if a.grpcSrv != nil {
		a.grpcSrv.GracefulStop()
	}

	if a.httpSrv != nil {
		_ = a.httpSrv.Shutdown(ctx)
	}

	if a.db != nil {
		a.db.Close()
	}

	if a.otelShutdown != nil {
		_ = a.otelShutdown(ctx)
	}

	if a.reaperCancel != nil {
		a.reaperCancel()
	}

	_ = a.Logger.Sync()
}
