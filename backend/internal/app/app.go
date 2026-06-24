package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/internal/utils"
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

	// health is the optional probe registrar. When set, Shutdown calls
	// MarkShuttingDown FIRST and pauses long enough for kubelet to
	// observe the 503 readyz before listeners go away — otherwise
	// in-flight traffic gets reset mid-request because the Service
	// still has us in its endpoint slice.
	health *health.Handler

	jobsCancel context.CancelFunc
}

// readyDrainPause is the wait between MarkShuttingDown and listener
// shutdown. It needs to exceed the longest readiness probe period across
// the planes (the Helm chart's default is 10s) so kubelet observes at
// least one 503 readyz response before the kube-proxy endpoint slice
// removes us. 12s gives one full probe cycle plus a small buffer.
const readyDrainPause = 12 * time.Second

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
			a.Logger.Info("plane listening",
				zap.String("plane", l.Plane),
				zap.String("addr", l.Server.Addr),
				zap.Bool("tls", l.TLS.Enabled),
				zap.String("client_auth", l.TLS.ClientAuth),
			)
			var err error
			if l.TLS.Enabled {
				// mTLS termination — construct a tls.Config that adds
				// ClientCAs + ClientAuth based on the listener's
				// config.TLS.ClientAuth string. When ClientAuth is
				// "none" or unset, behaviour is identical to the
				// pre-mTLS posture (one-way TLS, ignore client cert);
				// "permissive" verifies a presented cert without
				// requiring one (rollout cutover so plaintext + mTLS
				// callers both work); "strict" requires a verified
				// client cert (the destination posture once every
				// in-cluster caller is migrated). See
				// utils.ParseClientAuth + internal/utils/tls.go.
				tlsCfg, terr := utils.NewTLSConfig(
					l.TLS.CertPath, l.TLS.KeyPath, l.TLS.CaPath,
					l.TLS.ServerName, l.TLS.InsecureSkipVerify,
				)
				if terr != nil {
					errCh <- fmt.Errorf("plane %s: build tls config: %w", l.Plane, terr)
					return
				}
				clientAuth, terr := utils.ParseClientAuth(l.TLS.ClientAuth)
				if terr != nil {
					errCh <- fmt.Errorf("plane %s: parse client_auth: %w", l.Plane, terr)
					return
				}
				tlsCfg.ClientAuth = clientAuth
				l.Server.TLSConfig = tlsCfg
				// Cert + key are already loaded into tlsCfg.Certificates
				// — empty paths tell ListenAndServeTLS to use the
				// TLSConfig directly. Without this hand-off the
				// server would re-parse the files itself and lose
				// the ClientAuth + ClientCAs we just set.
				err = l.Server.ListenAndServeTLS("", "")
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
					a.Logger.Warn("background job exited",
						zap.String("type", fmt.Sprintf("%T", job)),
						zap.Error(err))
				}
			}()
		}
	}

	a.Started.Store(true)

	// Block on the first listener error; clean shutdown comes via Shutdown.
	return <-errCh
}

// WithHealth installs the probe registrar so Shutdown can flip /readyz
// to 503 BEFORE listeners go away. Builder-style so existing callers
// that don't yet supply one keep compiling unchanged.
func (a *App) WithHealth(h *health.Handler) *App {
	a.health = h
	return a
}

// Shutdown stops every HTTP listener and the reconciler. Idempotent.
//
// Order matters:
//
//  1. health.MarkShuttingDown — readyz starts returning 503; kubelet
//     stops adding new endpoints to the Service.
//  2. readyDrainPause       — let kubelet observe at least one 503
//     before we tear down listeners. Without this, kube-proxy may
//     still route a few requests to a half-closed pod and clients see
//     `connection reset` mid-request.
//  3. Cancel background jobs — workers stop accepting new ticks.
//  4. http.Server.Shutdown — waits for in-flight requests to finish
//     within ShutdownTimeout.
//  5. Close DB pool + flush observability.
func (a *App) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), a.Cfg.Runtime.ShutdownTimeout+readyDrainPause)
	defer cancel()

	a.Logger.Info("shutting down")

	// Phase 1: flip readyz BEFORE anything else so kubelet's probe loop
	// sees the 503 promptly. Skipping when health is unwired keeps the
	// shape testable from contexts that don't bring up real probes.
	if a.health != nil {
		a.health.MarkShuttingDown()
		a.Logger.Info("readyz draining; waiting for kubelet to observe before stopping listeners",
			zap.Duration("pause", readyDrainPause))
		select {
		case <-time.After(readyDrainPause):
		case <-ctx.Done():
			// Outer deadline fired (ShutdownTimeout exceeded). Skip the
			// pause and proceed with shutdown so we don't get SIGKILL'd
			// before any cleanup runs.
		}
	}

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
