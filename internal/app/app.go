package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	grpcapi "paladin/internal/api/grpc"
	httpapi "paladin/internal/api/http"
	"paladin/internal/breaker"
	"paladin/internal/config"
	"paladin/internal/logger"
	"paladin/internal/obs"
	"paladin/internal/service"
	"paladin/internal/storage/s3"
	"paladin/internal/store/postgres"
	"paladin/internal/utils"

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
	otelShutdown obs.ShutdownFunc
	started      atomic.Bool
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

	log.Info("Service metadata",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("build_time", buildTime),
		zap.String("pod", cfg.PodName),
		zap.String("env", cfg.Env),
	)

	ctx := context.Background()

	otelShutdown, err := obs.InitOTel(ctx, cfg.OTel)
	if err != nil {
		return nil, err
	}

	// DB connect with retries (production-friendly)
	var db *postgres.DB

	op := func() error {
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

	s3cfg := cfg.S3
	// Allow env overrides in container-centric setup
	if v := utils.GetEnvOrDefault("S3_BUCKET", ""); v != "" {
		s3cfg.Bucket = v
	}

	if v := utils.GetEnvOrDefault("S3_REGION", ""); v != "" {
		s3cfg.Region = v
	}

	if v := utils.GetEnvOrDefault("S3_ENDPOINT", ""); v != "" {
		s3cfg.Endpoint = v
	}

	if v := utils.GetEnvOrDefault("S3_ACCESS_KEY", ""); v != "" {
		s3cfg.AccessKey = v
	}

	if v := utils.GetEnvOrDefault("S3_SECRET_KEY", ""); v != "" {
		s3cfg.SecretKey = v
	}

	if v := utils.GetEnvOrDefault("S3_FORCE_PATH_STYLE", ""); v != "" {
		s3cfg.ForcePathStyle = v == "true" || v == "1"
	}

	if v := utils.GetEnvOrDefault("S3_PRESIGN_TTL", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			s3cfg.PresignTTL = d
		}
	}

	if v := utils.GetEnvOrDefault("S3_PART_SIZE", ""); v != "" {
		s3cfg.PartSizeRaw = v
		if n, err := utils.ParseSizeString(v); err == nil {
			s3cfg.PartSizeBytes = n
		}
	}

	s3c, err := s3.New(ctx, s3cfg, log)
	if err != nil {
		return nil, err
	}

	_ = s3c.EnsureBucket(ctx)

	policy := service.NewPolicy(cfg.Policy)
	objRepo := postgres.NewObjectsRepo(db)
	mpRepo := postgres.NewMultipartRepo(db)
	brk := breaker.NewFactory(cfg)

	svc := service.NewObjectsService(policy, s3c, objRepo, mpRepo, brk, s3cfg.PartSizeBytes)
	hs := service.NewHealthService(db, s3c, brk)

	app := &App{
		Version: version, Commit: commit, BuildTime: buildTime,
		Cfg: cfg, Logger: log,
		db:           db,
		otelShutdown: otelShutdown,
	}

	httpSrv := httpapi.NewServer(cfg.Server.Mode, log, svc, version, commit, buildTime, cfg.Server.LogProbes, hs, &app.started)

	// HTTP server
	app.httpSrv = &http.Server{
		Addr:              cfg.Server.HTTP.Addr,
		Handler:           httpSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// gRPC server
	app.grpcSrv = grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	grpcapi.RegisterPaladinServer(app.grpcSrv, grpcapi.NewServer(log, svc))
	reflection.Register(app.grpcSrv)

	return app, nil
}

func (a *App) Run() error {
	a.Logger.Info("Starting HTTP server", zap.String("addr", a.httpSrv.Addr))
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
		errCh <- a.httpSrv.ListenAndServe()
	}()

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

	_ = a.Logger.Sync()
}
