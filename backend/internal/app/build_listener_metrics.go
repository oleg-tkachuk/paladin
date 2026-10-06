package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/middleware"
)

// BuildMetricsListener returns the Prometheus scrape endpoint, or nil when
// metrics are not exported by pull.
//
// Its own listener, in plain HTTP, for the reason config.OTel.MetricsAddr
// documents: the API planes serve TLS from an internal CA, and reaching them
// would mean telling the collector to skip verification for every target in
// the cluster. Garage and SeaweedFS are already scraped this way, on 3903 and
// 9327 — this follows the shape that exists rather than asking the shared
// collector to accommodate one service.
//
// It serves exactly one path. A scraper that finds anything else here is
// pointed at the wrong port, and should be told so rather than handed a 200.
func BuildMetricsListener(deps *SharedDeps) *HTTPListener {
	if deps == nil || deps.Metrics == nil {
		return nil
	}
	addr := deps.Cfg.OTel.MetricsAddr
	if addr == "" {
		addr = "0.0.0.0:9095"
	}

	mux := http.NewServeMux()
	mux.Handle(middleware.PathMetrics, deps.Metrics)

	return &HTTPListener{
		Plane: "metrics",
		Server: &http.Server{
			Addr:    addr,
			Handler: mux,
			// Short and fixed rather than config-driven: a scrape is a small
			// GET on a loopback-adjacent network, and the endpoint has no
			// other traffic to accommodate.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		// Deliberately no TLS: see above. Metrics carry no credentials and the
		// port is not exposed beyond the pod network.
		TLS: config.TLS{Enabled: false},
	}
}

// MetricsListener is a running scrape endpoint, for the roles that manage
// their own listeners instead of handing an HTTPListener to RunApp: worker,
// dispatcher and ingest. A nil *MetricsListener is valid and does nothing, so
// those roles need no branch for "metrics are pushed, not pulled".
type MetricsListener struct {
	srv *http.Server
	ln  net.Listener
}

// StartMetricsListener binds BuildMetricsListener's endpoint and serves it in
// the background. It returns nil, nil when metrics are not exported by pull.
// ctx bounds the bind only; the listener runs until Shutdown.
//
// The bind happens here rather than in the goroutine, so a port already in
// use fails the role's start instead of leaving a pod that is Ready and
// cannot be scraped.
func StartMetricsListener(ctx context.Context, deps *SharedDeps, l *zap.Logger) (*MetricsListener, error) {
	m := BuildMetricsListener(deps)
	if m == nil {
		return nil, nil
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", m.Server.Addr)
	if err != nil {
		return nil, fmt.Errorf("metrics listener %s: %w", m.Server.Addr, err)
	}
	ml := &MetricsListener{srv: m.Server, ln: ln}
	l.Info("metrics listener", zap.String("addr", ln.Addr().String()))
	go func() {
		if err := ml.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error("metrics listener exited", zap.Error(err))
		}
	}()
	return ml, nil
}

// Addr is the address the listener is bound to.
func (m *MetricsListener) Addr() net.Addr {
	if m == nil {
		return nil
	}
	return m.ln.Addr()
}

// Shutdown stops the listener, waiting for in-flight scrapes up to ctx.
//
// It closes the socket itself as well as the server. http.Server.Shutdown
// closes only the listeners Serve has already registered, and Serve runs in a
// goroutine that may not have started yet; until it does, the bound socket
// stays open and the kernel completes connections on it, so the port would
// outlive a Shutdown that had already returned.
func (m *MetricsListener) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	err := m.srv.Shutdown(ctx)
	if cerr := m.ln.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
		err = errors.Join(err, fmt.Errorf("close metrics socket: %w", cerr))
	}
	return err
}
