package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
)

// HTTPListener bundles one *http.Server with its plane label, used for
// startup/shutdown logging.
type HTTPListener struct {
	Plane  string // "data" | "admin" | "iam"
	Server *http.Server
	TLS    config.TLS
}

// BackgroundJob is any long-running goroutine bound to the app lifetime.
// Run blocks until ctx is cancelled. Workers (reconciler, dispatcher,
// purgers) all satisfy this shape.
type BackgroundJob interface {
	Run(ctx context.Context) error
}

// App is the top-level runtime container. v2 owns three HTTP listeners
// (data, admin, iam) plus a fan of background workers.
type App struct {
	Version   string
	Commit    string
	BuildTime string

	Cfg    config.Config
	Logger *zap.Logger

	listeners []HTTPListener
	jobs      []BackgroundJob

	db           *postgres.DB
	otelShutdown observability.ShutdownFunc
	Started      *atomic.Bool

	jobsCancel context.CancelFunc
}

func NewContainer(
	version, commit, buildTime string,
	cfg config.Config,
	l *zap.Logger,
	listeners []HTTPListener,
	db *postgres.DB,
	otelShutdown observability.ShutdownFunc,
	jobs []BackgroundJob,
	started *atomic.Bool,
) *App {
	return &App{
		Version:      version,
		Commit:       commit,
		BuildTime:    buildTime,
		Cfg:          cfg,
		Logger:       l,
		listeners:    listeners,
		jobs:         jobs,
		db:           db,
		otelShutdown: otelShutdown,
		Started:      started,
	}
}

// Run starts every HTTP listener and blocks until the first one returns a
// non-clean shutdown error. Subsequent listeners are stopped via Shutdown.
func (a *App) Run() error {
	errCh := make(chan error, len(a.listeners))
	var wg sync.WaitGroup

	for i := range a.listeners {
		l := a.listeners[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Logger.Info("HTTP plane listening",
				zap.String("plane", l.Plane),
				zap.String("addr", l.Server.Addr),
				zap.Bool("tls", l.TLS.Enabled),
			)
			var err error
			if l.TLS.Enabled {
				err = l.Server.ListenAndServeTLS(l.TLS.CertPath, l.TLS.KeyPath)
			} else {
				err = l.Server.ListenAndServe()
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("plane %s: %w", l.Plane, err)
			}
		}()
	}

	if len(a.jobs) > 0 {
		jctx, cancel := context.WithCancel(context.Background())
		a.jobsCancel = cancel
		for i := range a.jobs {
			job := a.jobs[i]
			go func() {
				if err := job.Run(jctx); err != nil && !errors.Is(err, context.Canceled) {
					a.Logger.Warn("background job exited with error",
						zap.String("type", fmt.Sprintf("%T", job)),
						zap.Error(err))
				}
			}()
		}
	}

	a.Started.Store(true)

	// Block on the first listener error; clean shutdown comes via Shutdown.
	select {
	case err := <-errCh:
		return err
	}
}

// Shutdown stops every HTTP listener and the reconciler. Idempotent.
func (a *App) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
	defer cancel()

	a.Logger.Info("Shutting down...")

	if a.jobsCancel != nil {
		a.jobsCancel()
	}

	for _, l := range a.listeners {
		if l.Server != nil {
			_ = l.Server.Shutdown(ctx)
		}
	}

	if a.db != nil {
		a.db.Close()
	}

	if a.otelShutdown != nil {
		_ = a.otelShutdown(ctx)
	}

	_ = a.Logger.Sync()
}
