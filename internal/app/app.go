package app

import (
	"context"
	"net/http"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// App is the top-level runtime container. It owns every long-lived resource
// (HTTP server, DB pool, background workers, telemetry shutdown) and is the
// single point of orderly startup/shutdown.
type App struct {
	Version   string
	Commit    string
	BuildTime string

	Cfg    config.Config
	Logger *zap.Logger

	httpSrv *http.Server

	db           *postgres.DB
	otelShutdown observability.ShutdownFunc
	Started      *atomic.Bool

	reconciler       *worker.ReconcilerV2
	reconcilerCancel context.CancelFunc
}

func NewContainer(
	version, commit, buildTime string,
	cfg config.Config,
	l *zap.Logger,
	httpSrv *http.Server,
	db *postgres.DB,
	otelShutdown observability.ShutdownFunc,
	reconciler *worker.ReconcilerV2,
	started *atomic.Bool,
) *App {
	return &App{
		Version:      version,
		Commit:       commit,
		BuildTime:    buildTime,
		Cfg:          cfg,
		Logger:       l,
		httpSrv:      httpSrv,
		db:           db,
		otelShutdown: otelShutdown,
		reconciler:   reconciler,
		Started:      started,
	}
}

func (a *App) Run() error {
	errCh := make(chan error, 1)

	go func() {
		if a.Cfg.Server.HTTP.TLS.Enabled {
			a.Logger.Info("Starting HTTPS server with TLS",
				zap.String("addr", a.httpSrv.Addr),
				zap.String("cert_path", a.Cfg.Server.HTTP.TLS.CertPath),
			)
			if err := a.httpSrv.ListenAndServeTLS(a.Cfg.Server.HTTP.TLS.CertPath, a.Cfg.Server.HTTP.TLS.KeyPath); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		} else {
			a.Logger.Info("Starting HTTP server", zap.String("addr", a.httpSrv.Addr))
			if err := a.httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}
	}()

	if a.reconciler != nil {
		rctx, cancel := context.WithCancel(context.Background())
		a.reconcilerCancel = cancel
		go func() { _ = a.reconciler.Run(rctx) }()
	}

	a.Started.Store(true)

	return <-errCh
}

func (a *App) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Server.ShutdownTimeout)
	defer cancel()

	a.Logger.Info("Shutting down...")

	if a.reconcilerCancel != nil {
		a.reconcilerCancel()
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
