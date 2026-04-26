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
	policyh "github.com/oleg-tkachuk/paladin/internal/api/v1/policy"
	systemh "github.com/oleg-tkachuk/paladin/internal/api/v1/system"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/filter/cel"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
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

		// Two-phase migration: schema first, then seed + backfill, then the
		// constraints that depend on backfilled data. Migration 005 enforces
		// NOT NULL on object_keys.bucket_name and would fail on legacy DBs
		// without the backfill in between.
		if err := db.RunMigrationsTo(ctx, migrations.FS, 4); err != nil && !errors.Is(err, context.Canceled) {
			l.Fatal("Phase-1 migrations failed", zap.Error(err))
		}

		if err := seedStorageBackends(ctx, db.Queries, cfg.Storage); err != nil {
			l.Fatal("Failed to seed storage backends", zap.Error(err))
		}

		if err := seedDefaultBuckets(ctx, db.Pool, cfg.Storage); err != nil {
			l.Fatal("Failed to seed default buckets", zap.Error(err))
		}

		if n, err := backfillObjectKeyBuckets(ctx, db.Pool, cfg.Storage); err != nil {
			l.Fatal("Failed to backfill object_keys.bucket_name", zap.Error(err))
		} else if n > 0 {
			l.Info("Backfilled bucket_name for legacy object_keys",
				zap.Int64("rows", n),
			)
		}

		if err := db.RunMigrations(ctx, migrations.FS); err != nil && !errors.Is(err, context.Canceled) {
			l.Fatal("Phase-2 migrations failed", zap.Error(err))
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
		ObjectKey: adapters.NewObjectKeyRepo(db.Queries, pool),
		Bucket:    adapters.NewBucketRepo(db.Queries),
		Tenant:    adapters.NewTenantRepo(db.Queries, pool),
		ObjectTag: adapters.NewObjectTagRepo(db.Queries),
		Presign:   adapters.NewPresignRepo(db.Queries, pool),
		Multipart: adapters.NewMultipartRepo(db.Queries, pool),
		Operation: adapters.NewOperationRepo(db.Queries),
	}
	storage := wire.Storage{
		Object:      s3c,
		Multipart:   s3c,
		Presign:     s3c.Presign(),
		Stream:      s3c,
		Provisioner: s3c,
	}

	polStore := policy.NewPostgresStore(pool)
	polEngine := policy.NewEngine(polStore, cfg.Cedar.PolicyCacheTTL)
	if err := polEngine.Start(ctx); err != nil {
		return nil, fmt.Errorf("policy engine start: %w", err)
	}

	sm := statemachine.New(pool)
	celEval := cel.NewEvaluator()

	objH := wire.ProvideObjectHandler(repos, storage, polEngine, celEval, sm, cfg)
	objectKeyH := wire.ProvideObjectKeyHandler(repos, polEngine, cfg)
	bucketH := wire.ProvideBucketHandler(repos, storage, cfg)
	tenantH := wire.ProvideTenantHandler(repos)
	objectTagH := wire.ProvideObjectTagHandler(repos)
	opH := wire.ProvideOperationHandler(repos)
	batchH := wire.ProvideBatchHandler(opH, polEngine)
	presignH := wire.ProvidePresignHandler(repos, storage, polEngine, cfg)
	mpH := wire.ProvideMultipartHandler(repos, storage, polEngine, sm)
	policyH := policyh.NewHandler()

	var buildT time.Time
	if t, err := time.Parse(time.RFC3339, buildTime); err == nil {
		buildT = t
	}
	systemH := systemh.NewHandler(systemh.Info{
		Version:   version,
		Commit:    commit,
		BuildTime: buildT,
	}, db, cfg, configPath)

	verifier, err := buildVerifier(cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth verifier: %w", err)
	}
	opts := connect.WithInterceptors(auth.Interceptor(verifier))

	mux := http.NewServeMux()
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
	mux.Handle(paladinv1connect.NewTenantServiceHandler(connectshim.NewTenantServer(tenantH), opts))
	mux.Handle(paladinv1connect.NewObjectTagServiceHandler(connectshim.NewObjectTagServer(objectTagH), opts))
	mux.Handle(paladinv1connect.NewObjectKeyServiceHandler(connectshim.NewObjectKeyServer(objectKeyH), opts))
	mux.Handle(paladinv1connect.NewBucketServiceHandler(connectshim.NewBucketServer(bucketH), opts))
	mux.Handle(paladinv1connect.NewObjectServiceHandler(connectshim.NewObjectServer(objH), opts))
	mux.Handle(paladinv1connect.NewPresignServiceHandler(connectshim.NewPresignServer(presignH), opts))
	mux.Handle(paladinv1connect.NewMultipartUploadServiceHandler(connectshim.NewMultipartServer(mpH), opts))
	mux.Handle(paladinv1connect.NewOperationServiceHandler(connectshim.NewOperationServer(opH), opts))
	mux.Handle(paladinv1connect.NewBatchServiceHandler(connectshim.NewBatchServer(batchH), opts))
	mux.Handle(paladinv1connect.NewPolicyServiceHandler(connectshim.NewPolicyServer(policyH), opts))
	mux.Handle(paladinv1connect.NewSystemServiceHandler(connectshim.NewSystemServer(systemH), opts))

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

// seedDefaultBuckets ensures every backend with a `bucket:` value in config
// has a corresponding row in the `buckets` table. The S3 bucket itself is
// expected to be pre-created out-of-band by the operator (or via the
// BucketService API at runtime); this seed only records the mapping so
// ObjectKey rows can FK to it. Idempotent via ON CONFLICT DO NOTHING.
func seedDefaultBuckets(ctx context.Context, pool postgres.PgxPool, s config.Storage) error {
	for name, b := range s.Backends {
		if b.Bucket == "" {
			continue
		}
		const q = `
			INSERT INTO buckets (backend_id, bucket_name, display_name, region, labels)
			VALUES ($1, $2, $3, $4, '{}'::jsonb)
			ON CONFLICT (backend_id, bucket_name) DO NOTHING
		`
		var displayName, region *string
		if d := "Default bucket for " + name; d != "" {
			displayName = &d
		}
		if b.Region != "" {
			r := b.Region
			region = &r
		}
		if _, err := pool.Exec(ctx, q, name, b.Bucket, displayName, region); err != nil {
			return fmt.Errorf("seed default bucket %q in backend %q: %w", b.Bucket, name, err)
		}
	}
	return nil
}

// backfillObjectKeyBuckets fills in object_keys.bucket_name for any rows
// where it is NULL, using the configured default bucket for the row's
// backend_id. This bridges legacy installs (where ObjectKey predates the
// bucket model) onto the new schema; once every row has a value, migration
// 005 can flip the column to NOT NULL.
func backfillObjectKeyBuckets(ctx context.Context, pool postgres.PgxPool, s config.Storage) (int64, error) {
	var total int64
	for name, b := range s.Backends {
		if b.Bucket == "" {
			continue
		}
		const q = `
			UPDATE object_keys
			SET bucket_name = $1
			WHERE backend_id = $2 AND bucket_name IS NULL
		`
		tag, err := pool.Exec(ctx, q, b.Bucket, name)
		if err != nil {
			return total, fmt.Errorf("backfill backend %q: %w", name, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

// seedStorageBackends upserts the storage_backends registry from config so
// that `object_keys.backend_id` FK references resolve for object_keys created at
// runtime. Idempotent — re-runs on every startup to pick up config edits.
func seedStorageBackends(ctx context.Context, q *sqlc.Queries, s config.Storage) error {
	for name, b := range s.Backends {
		var endpoint, region, eventsTarget *string
		if b.Endpoint != "" {
			e := b.Endpoint
			endpoint = &e
		}
		if b.Region != "" {
			r := b.Region
			region = &r
		}
		if b.Events.Target != "" {
			t := b.Events.Target
			eventsTarget = &t
		}
		if err := q.CreateStorageBackend(ctx, name, b.Kind, endpoint, region, b.Events.Enabled, eventsTarget); err != nil {
			return fmt.Errorf("seed storage backend %q: %w", name, err)
		}
	}
	return nil
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
