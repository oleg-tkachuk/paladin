package app

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/worker"
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
	Started      *atomic.Bool

	reaper       *worker.Reaper
	reaperCancel context.CancelFunc
}

func NewContainer(
	version, commit, buildTime string,
	cfg config.Config,
	l *zap.Logger,
	httpSrv *http.Server,
	grpcSrv *grpc.Server,
	db *postgres.DB,
	otelShutdown observability.ShutdownFunc,
	reaper *worker.Reaper,
	started *atomic.Bool,
) *App {
	return &App{
		Version: version, Commit: commit, BuildTime: buildTime,
		Cfg: cfg, Logger: l,
		httpSrv: httpSrv, grpcSrv: grpcSrv,
		db: db, otelShutdown: otelShutdown,
		reaper:  reaper,
		Started: started,
	}
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
		rCtx, rCancel := context.WithCancel(context.Background())
		a.reaperCancel = rCancel
		go a.reaper.Start(rCtx)
	}

	a.Started.Store(true)

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
