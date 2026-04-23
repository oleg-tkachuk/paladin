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
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/oleg-tkachuk/paladin/internal/api/connectshim"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/wire"
	"github.com/oleg-tkachuk/paladin/migrations"
)

const (
	defaultConfigPath    = "/app/configs/config.yaml"
	defaultShutdownGrace = 15 * time.Second
)

var (
	configPath string
	//nolint:unused // retained for ldflags injection
	version = "dev"
	//nolint:unused // retained for ldflags injection
	commit = "none"
	//nolint:unused // retained for ldflags injection
	buildTime = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "paladin",
	Short: "Start the Paladin service",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		bootstrap := logger.NewBootstrapLogger()

		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}

		cfg, err := config.Load(configPath, bootstrap)
		if err != nil {
			bootstrap.Fatal("Failed to load config", zap.Error(err))
		}

		l, err := logger.New(cfg.Logger)
		if err != nil {
			bootstrap.Fatal("Failed to build logger", zap.Error(err))
		}
		logger.ReplaceGlobals(l)

		db, err := postgres.New(ctx, cfg.Datastores.Postgres, l.Named("postgres"))
		if err != nil {
			l.Fatal("Failed to connect to database", zap.Error(err))
		}
		defer db.Close()

		if err := db.Ping(ctx); err != nil {
			l.Fatal("Database ping failed", zap.Error(err))
		}

		if err := db.RunMigrations(ctx, migrations.FS); err != nil && !errors.Is(err, context.Canceled) {
			l.Warn("Migrations failed", zap.Error(err))
		}

		srv, err := buildServer(ctx, cfg, db, l)
		if err != nil {
			l.Fatal("Failed to assemble server", zap.Error(err))
		}

		l.Info("Paladin starting",
			zap.String("addr", cfg.Server.HTTP.Addr),
			zap.String("version", version),
			zap.String("commit", commit),
			zap.String("build_time", buildTime),
		)

		serveErr := make(chan error, 1)
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- err
			}
			close(serveErr)
		}()

		select {
		case <-ctx.Done():
			l.Info("Shutdown signal received")
		case err := <-serveErr:
			l.Error("HTTP server failed", zap.Error(err))
		}

		grace := cfg.Server.ShutdownTimeout
		if grace <= 0 {
			grace = defaultShutdownGrace
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			l.Warn("Graceful shutdown failed", zap.Error(err))
		}
		_ = l.Sync()
	},
}

// buildServer assembles repositories, storage, policy engine, handlers, and
// Connect shims into an h2c-capable HTTP server. It does not call Wire's
// generated injector — the DI shape is small enough to compose directly.
func buildServer(ctx context.Context, cfg config.Config, db *postgres.DB, l *zap.Logger) (*http.Server, error) {
	pool, ok := db.Pool.(*pgxpool.Pool)
	if !ok {
		return nil, errors.New("DB.Pool is not *pgxpool.Pool")
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

	repos := wire.Repos{
		Object:    adapters.NewObjectRepo(db.Queries, pool),
		Bucket:    adapters.NewBucketRepo(db.Queries, pool),
		Tenant:    adapters.NewTenantRepo(db.Queries),
		Presign:   adapters.NewPresignRepo(db.Queries, pool),
		Multipart: adapters.NewMultipartRepo(db.Queries, pool),
		Operation: adapters.NewOperationRepo(db.Queries),
	}
	storage := wire.Storage{
		Object:    s3c,
		Multipart: s3c,
		Presign:   s3c.Presign(),
		Stream:    s3c,
	}

	polStore := policy.NewPostgresStore(pool)
	polEngine := policy.NewEngine(polStore, cfg.Cedar.PolicyCacheTTL)
	if err := polEngine.Start(ctx); err != nil {
		return nil, fmt.Errorf("policy engine start: %w", err)
	}

	sm := statemachine.New(pool)
	celEval := cel.NewEvaluator()

	objH := wire.ProvideObjectHandler(repos, storage, polEngine, celEval, sm, cfg)
	bucketH := wire.ProvideBucketHandler(repos, polEngine)
	tenantH := wire.ProvideTenantHandler(repos)
	opH := wire.ProvideOperationHandler(repos)
	batchH := wire.ProvideBatchHandler(opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, sm)

	verifier, err := buildVerifier(cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: %w", err)
	}
	opts := connect.WithInterceptors(auth.Interceptor(verifier))

	mux := http.NewServeMux()
	mux.Handle(paladinv1connect.NewTenantServiceHandler(connectshim.NewTenantServer(tenantH), opts))
	mux.Handle(paladinv1connect.NewBucketServiceHandler(connectshim.NewBucketServer(bucketH), opts))
	mux.Handle(paladinv1connect.NewObjectServiceHandler(connectshim.NewObjectServer(objH), opts))
	mux.Handle(paladinv1connect.NewPresignServiceHandler(connectshim.NewPresignServer(presignH), opts))
	mux.Handle(paladinv1connect.NewMultipartUploadServiceHandler(connectshim.NewMultipartServer(mpH), opts))
	mux.Handle(paladinv1connect.NewOperationServiceHandler(connectshim.NewOperationServer(opH), opts))
	mux.Handle(paladinv1connect.NewBatchServiceHandler(connectshim.NewBatchServer(batchH), opts))

	handler := h2c.NewHandler(mux, &http2.Server{})

	return &http.Server{
		Addr:              cfg.Server.HTTP.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.HTTP.ReadTimeout,
		WriteTimeout:      cfg.Server.HTTP.WriteTimeout,
		IdleTimeout:       cfg.Server.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.HTTP.MaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return logger.WithContext(context.Background(), l)
		},
	}, nil
}

// buildVerifier constructs a JWTVerifier from Auth config. JWKSURL takes
// precedence when set; otherwise HMACSecret is used for HS256 verification.
func buildVerifier(a config.Auth) (auth.TokenVerifier, error) {
	v := &auth.JWTVerifier{
		ExpectedIssuer:   a.Issuer,
		ExpectedAudience: a.Audience,
		Leeway:           a.Leeway,
	}
	switch {
	case a.JWKSURL != "":
		return nil, errors.New("auth.jwks_url not yet supported — set auth.hmac_secret")
	case a.HMACSecret != "":
		v.Key = []byte(a.HMACSecret)
	default:
		return nil, errors.New("auth: either jwks_url or hmac_secret must be set")
	}
	return v, nil
}

func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Path to config YAML file")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
