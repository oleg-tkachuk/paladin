// Command paladin-console serves the console: its static export, embedded,
// and the few routes a static file cannot answer (ADR-0049).
//
//	paladin-console          serve
//	paladin-console probe    exit 0 when a server on this host is live
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/frontend/server/contract"
	"github.com/oleg-tkachuk/paladin/frontend/server/dist"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/policy"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/site"
	"github.com/oleg-tkachuk/paladin/frontend/server/internal/web"
)

const (
	probeCommand = "probe"
	probeHost    = "127.0.0.1"
	probeTimeout = 2 * time.Second
	// relayTimeout bounds one consent post to the IAM plane.
	relayTimeout      = 10 * time.Second
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 60 * time.Second
	// shutdownGrace lets requests in flight finish after SIGTERM, within the
	// pod's default termination grace period.
	shutdownGrace = 10 * time.Second
)

func main() {
	names, err := contract.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := loadConfig(os.Getenv, os.ReadFile, names.AuthorizePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == probeCommand {
		os.Exit(probe(cfg.port, names.LivePath))
	}
	log, err := newLogger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logger:", err)
		os.Exit(1)
	}
	if err := serve(cfg, names, log); err != nil {
		log.Fatal("console server failed", zap.Error(err))
	}
}

func newLogger(cfg config) (*zap.Logger, error) {
	zc := zap.NewProductionConfig()
	zc.Level = zap.NewAtomicLevelAt(cfg.logLevel)
	return zc.Build()
}

func serve(cfg config, names contract.Names, log *zap.Logger) error {
	files, err := dist.Files()
	if err != nil {
		return err
	}
	s, err := site.New(files, names.PlaceholderSegment)
	if err != nil {
		return err
	}
	origins, invalid := policy.StorageOrigins(cfg.storageOrigins)
	if len(invalid) > 0 {
		log.Warn("storage origins left out of the CSP: not bare http(s) origins",
			zap.Strings("invalid", invalid))
	}
	handler := web.Handler(web.Config{
		Names:        names,
		Site:         s,
		Policy:       policy.New(origins),
		Banner:       cfg.banner,
		AuthorizeURL: cfg.authorizeURL,
		Relay:        web.RelayClient(cfg.relayTransport(), relayTimeout),
		Log:          log,
	})
	srv := &http.Server{
		Addr:              cfg.listenAddr(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          zap.NewStdLog(log.Named("http")),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	log.Info("console server started",
		zap.String("addr", srv.Addr),
		zap.Int("files", s.Files()),
		zap.Strings("storage_origins", origins),
		zap.String("authorize_url", cfg.authorizeURL),
		zap.Bool("backend_ca", cfg.backendCA != nil),
		zap.Bool("banner", cfg.banner.Content != ""),
	)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	log.Info("console server stopping", zap.Duration("grace", shutdownGrace))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errs; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("console server stopped")
	return nil
}

// probe asks the server on this host whether it is live: the image's
// healthcheck, since a distroless image has no shell or wget to ask with.
func probe(port int, livePath string) int {
	client := &http.Client{Timeout: probeTimeout}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d%s", probeHost, port, livePath))
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "probe: status", resp.StatusCode)
		return 1
	}
	return 0
}
