package app

// appfx wires the Paladin runtime with Uber fx (go.uber.org/fx). fx is the DI
// container + lifecycle driver: every heavy constructor is registered with
// fx.Provide and the dependency graph is resolved by fx; fx.App.Run() owns
// signal handling (SIGINT/SIGTERM) and drives start/stop.
//
// Deliberately, fx does NOT re-implement the graceful-shutdown choreography —
// the proven *App (health-drain → jobs cancel → server Shutdown → DB close →
// OTel flush, see app.go) is wrapped in a single fx.Lifecycle hook, so the
// teardown ordering is byte-for-byte the pre-fx behaviour. This replaces the
// old Google Wire ProviderSet (which was dead) and the hand-rolled
// signalCtx/runListeners loop.

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/config"
	"github.com/oleg-tkachuk/paladin-private/internal/health"
	"github.com/oleg-tkachuk/paladin-private/internal/logger"
	"github.com/oleg-tkachuk/paladin-private/internal/observability"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres"
)

// ConfigSource carries the resolved config file path + overlay chain the CLI
// derived. Each serve subcommand supplies one via fx.Supply.
type ConfigSource struct {
	Path     string
	Overlays []string // additional overlay files, merged in order (later wins)
}

// ListenerSet is what a role contributes to the runtime: its HTTP listeners,
// the shared health handler (drives readyz draining on shutdown), and any
// listener-scoped background jobs.
type ListenerSet struct {
	Listeners []HTTPListener
	Health    *health.Handler
	Jobs      []BackgroundJob
}

// ProvideConfig loads + validates config from the source (fx construction time).
func ProvideConfig(src ConfigSource) (config.Config, error) {
	boot, err := logger.NewBootstrapLogger()
	if err != nil {
		return config.Config{}, err
	}
	paths := append([]string{src.Path}, src.Overlays...)
	return config.Load(paths, boot)
}

// ProvideLogger builds the production logger and installs it as the global —
// the otelzap/otelpgx instrumentation and package-level logger.L() read it.
func ProvideLogger(cfg config.Config) (*zap.Logger, error) {
	l, err := logger.New(cfg.Logger)
	if err != nil {
		return nil, err
	}
	logger.ReplaceGlobals(l)
	return l, nil
}

// ProvideOTel activates OpenTelemetry (ADR-0001). Non-fatal: on error it
// returns a no-op shutdown so observability never blocks startup. The returned
// hook is run by *App.Shutdown (NOT a separate fx OnStop) so teardown ordering
// matches the pre-fx path exactly.
func ProvideOTel(cfg config.Config, l *zap.Logger) observability.ShutdownFunc {
	sh, err := observability.InitOTel(context.Background(), cfg.OTel)
	if err != nil {
		l.Error("failed to initialise OpenTelemetry; continuing without it", zap.Error(err))
		return func(context.Context) error { return nil }
	}
	return sh
}

// ProvideDB opens + pings the RLS-scoped pool. *App.Shutdown closes it.
func ProvideDB(cfg config.Config, l *zap.Logger) (*postgres.DB, error) {
	db, err := postgres.New(context.Background(), cfg.Datastores.Postgres, l.Named("postgres"), postgres.WithRLS())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// ProvideSharedDeps builds the heavy shared dependency product every plane and
// worker needs.
func ProvideSharedDeps(cfg config.Config, db *postgres.DB, l *zap.Logger) (*SharedDeps, error) {
	return BuildSharedDeps(context.Background(), cfg, db, l)
}

// ProvideApp assembles the runtime container from the resolved graph. It takes
// *SharedDeps so App.Shutdown can stop the Cedar LISTEN watcher (holds a pooled
// connection) before closing the DB pool — without that, pgxpool.Close
// deadlocks shutdown until SIGKILL.
func ProvideApp(
	meta BuildMeta,
	cfg config.Config,
	l *zap.Logger,
	db *postgres.DB,
	otel observability.ShutdownFunc,
	deps *SharedDeps,
	set ListenerSet,
) *App {
	started := &atomic.Bool{}
	return NewContainer(
		meta.Version, meta.Commit, meta.BuildTime,
		cfg, l, set.Listeners, db, otel, set.Jobs, started,
	).WithHealth(set.Health).WithStopWatchers(deps.StopWatchers)
}

// RunApp registers the App lifecycle with fx. OnStart launches the listeners in
// the background (App.Run blocks on the first fatal listener error); a bind
// failure triggers an fx shutdown with a non-zero exit code. OnStop runs the
// proven graceful teardown. fx.App.Run() (the caller) handles the signal wait.
func RunApp(lc fx.Lifecycle, sd fx.Shutdowner, a *App, l *zap.Logger) {
	l.Info("starting",
		zap.String("version", a.Version),
		zap.String("commit", a.Commit),
		zap.String("build_time", a.BuildTime),
	)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				if err := a.Run(); err != nil {
					l.Error("server run failed", zap.Error(err))
					_ = sd.Shutdown(fx.ExitCode(1))
				}
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			a.Shutdown()
			return nil
		},
	})
}

// fxLogger routes fx's own event log through zap (Named "fx") at debug level.
func fxLogger(l *zap.Logger) fxevent.Logger {
	zl := &fxevent.ZapLogger{Logger: l.Named("fx")}
	zl.UseLogLevel(zap.DebugLevel)
	return zl
}

// LiteModule is the smallest process-wide bundle: config + logger, fx's own log
// routed through zap, and the shared StopTimeout — but NO database, OTel, or
// SharedDeps. It exists for roles that legitimately run without a DB, such as
// the MCP bridge (a stdio/HTTP proxy that speaks Connect to upstream URLs and
// never opens a pool). The StopTimeout exceeds any role's graceful-shutdown
// budget (readyDrainPause + Runtime.ShutdownTimeout for planes; the worker's
// lease-drain + ops-shutdown) — 90s is comfortably above the 12s drain + a
// typical 20s shutdown.
var LiteModule = fx.Options(
	fx.Provide(ProvideConfig, ProvideLogger),
	fx.WithLogger(fxLogger),
	fx.StopTimeout(90*time.Second),
)

// BaseModule is LiteModule plus the heavy shared infrastructure EVERY DB-backed
// serve role needs (listener planes and workers alike): OTel → DB → SharedDeps.
var BaseModule = fx.Options(
	LiteModule,
	fx.Provide(ProvideOTel, ProvideDB, ProvideSharedDeps),
)

// SharedModule is BaseModule plus the *App container — the runtime shape the
// listener planes (api/admin) use. Worker-style roles compose BaseModule with
// their own lifecycle instead of *App.
var SharedModule = fx.Options(
	BaseModule,
	fx.Provide(ProvideApp),
)

// ─── Role modules ────────────────────────────────────────────────────────────

// ProvideAPIListenerSet builds the data + iam Connect listeners.
func ProvideAPIListenerSet(deps *SharedDeps, meta BuildMeta) (ListenerSet, error) {
	listeners, healthH, err := BuildAPIListeners(context.Background(), deps, meta)
	if err != nil {
		return ListenerSet{}, err
	}
	return ListenerSet{Listeners: listeners, Health: healthH, Jobs: deps.BackgroundJobs}, nil
}

// APIModule is the fx graph for `paladin serve api`.
var APIModule = fx.Options(
	SharedModule,
	fx.Provide(ProvideAPIListenerSet),
	fx.Invoke(RunApp),
)

// ProvideAdminListenerSet builds the single admin Connect listener.
func ProvideAdminListenerSet(deps *SharedDeps, meta BuildMeta) (ListenerSet, error) {
	listener, healthH, err := BuildAdminListener(context.Background(), deps, meta)
	if err != nil {
		return ListenerSet{}, err
	}
	return ListenerSet{Listeners: []HTTPListener{listener}, Health: healthH, Jobs: deps.BackgroundJobs}, nil
}

// AdminModule is the fx graph for `paladin serve admin`.
var AdminModule = fx.Options(
	SharedModule,
	fx.Provide(ProvideAdminListenerSet),
	fx.Invoke(RunApp),
)
