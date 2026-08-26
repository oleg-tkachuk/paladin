package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
)

// BuildHTTPServer wraps a mux into an h2c-enabled http.Server with
// per-plane timeouts taken from cfg.HTTPServer. Connect over HTTP/2 cleartext
// is the Paladin default — the plane is fronted by an ingress that terminates TLS,
// so h2c keeps the binary contract simple and lets the gateway handle ALPN.
func BuildHTTPServer(c config.HTTPServer, mux http.Handler, l *zap.Logger) *http.Server {
	// HTTP/2 cleartext (h2c) via the stdlib Protocols API (Go 1.24+), replacing
	// the deprecated golang.org/x/net/http2/h2c handler wrapper. The plane is
	// fronted by an ingress that terminates TLS, so unencrypted H2 keeps the
	// binary contract simple and lets the gateway handle ALPN. HTTP/1 stays on
	// for health probes and non-gRPC clients.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Addr:              c.Addr,
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: c.ReadHeaderTimeout,
		ReadTimeout:       c.ReadTimeout,
		WriteTimeout:      c.WriteTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    c.MaxHeaderBytes,
		BaseContext: func(_ net.Listener) context.Context {
			return logger.WithContext(context.Background(), l)
		},
		// Route the server's own errors — TLS handshake failures, malformed
		// requests, anything the handler never sees — into the structured log
		// with the listener's address attached.
		//
		// Without this they go to Go's default logger: raw lines on stderr,
		// carrying the client's address but not which listener they arrived
		// on. A pod that serves two listeners then produces messages nobody
		// can attribute. That is not hypothetical — "TLS handshake error from
		// 127.0.0.1: client sent an HTTP request to an HTTPS server" appeared
		// in bursts on the api pod, which serves data and iam, and the missing
		// half of the sentence is why the client is still unidentified.
		ErrorLog: serverErrorLog(l, c.Addr),
	}
}

// serverErrorLog adapts zap for http.Server.ErrorLog at warn level, tagged
// with the listener the message came from.
//
// NewStdLogAt only fails on an unknown level, which is a compile-time
// constant here; a nil ErrorLog is the stdlib default (Go's global logger), so
// falling back to it on the impossible branch loses the tag and nothing else.
func serverErrorLog(l *zap.Logger, addr string) *log.Logger {
	std, err := zap.NewStdLogAt(
		l.Named("http").With(zap.String("listen_addr", addr)),
		zapcore.WarnLevel,
	)
	if err != nil {
		return nil
	}
	return std
}

// BuildVerifier returns a TokenVerifier pinned to a specific audience.
// JWKS-mode wins when auth.jwks_url is set (federated IdP); otherwise the
// HMAC verifier reads auth.signing_key. The bootstrap codepath leaves
// signing_key required for the local-admin token flow even when JWKS is
// configured for tenant traffic.
func BuildVerifier(ctx context.Context, a config.Auth, audience string, l *zap.Logger) (auth.TokenVerifier, error) {
	if a.JWKSURL != "" {
		v := auth.NewJWKSVerifier(a.JWKSURL)
		v.ExpectedIssuer = a.Issuer
		v.ExpectedAudience = audience
		v.Leeway = a.Leeway
		if err := v.Start(ctx); err != nil {
			return nil, fmt.Errorf("jwks(%s): %w", audience, err)
		}
		l.Info("using jwks verifier", zap.String("audience", audience), zap.String("url", a.JWKSURL))
		return v, nil
	}
	return &auth.JWTVerifier{
		Key:              []byte(a.SigningKey),
		ExpectedIssuer:   a.Issuer,
		ExpectedAudience: audience,
		Leeway:           a.Leeway,
	}, nil
}

// ParseBuildTime turns the link-time `buildTime` string into a time.Time.
// Build pipelines emit RFC3339 (`date -Iseconds`); local `go run` builds
// carry "unknown" — return zero so SystemService returns nil for build_time.
func ParseBuildTime(s string) time.Time {
	if s == "" || s == "unknown" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// NewHealthHandler builds the shared probe handler for one plane.
//
// Multiple planes in the same process register against the same handler
// so the shutting-down state stays unified. When planes run in different
// pods (post-Phase-2 Helm split) they get independent handlers — that is
// the correct behaviour because each pod has its own readiness lifecycle.
//
// Today only Postgres is registered as a Critical check (Category=
// database). Storage / capability / MCP-upstream checks are added by
// callers that own those subsystems via the `Add` helper below — keeps
// this factory free of subsystem-specific deps and lets a role-only
// component (e.g. mcp upstreams on the mcp role) be conditionally
// registered.
func NewHealthHandler(db *postgres.DB, cfg config.Runtime, l *zap.Logger) *health.Handler {
	dbPing := health.Check{
		Name:     "postgres",
		Category: health.CategoryDatabase,
		Critical: true,
		Func:     func(ctx context.Context) error { return db.Ping(ctx) },
	}
	return &health.Handler{
		Logger:        l.Named("health"),
		LogSuccesses:  cfg.LogProbes,
		SnapshotToken: cfg.HealthSnapshotToken,
		Ready:         []health.Check{dbPing},
		Startup:       []health.Check{dbPing},
	}
}

// AddSubsystemCheck appends a Category=subsystem check (capability,
// cedar, ingest). Critical default true — these gate request-path
// authorisation and should fail readiness when broken.
func AddSubsystemCheck(h *health.Handler, name string, critical bool, fn func(ctx context.Context) error) {
	h.Ready = append(h.Ready, health.Check{
		Name:     name,
		Category: health.CategorySubsystem,
		Critical: critical,
		Func:     fn,
	})
}

// AddDisabledSubsystem registers an always-healthy informational
// component for a subsystem that is off-by-config. Surfaces a "disabled"
// note on the /health page so operators see the row instead of having
// to grep config to confirm a subsystem is intentionally absent.
// Critical=false: a disabled subsystem must never fail /readyz.
func AddDisabledSubsystem(h *health.Handler, name string) {
	h.Ready = append(h.Ready, health.Check{
		Name:     name,
		Category: health.CategorySubsystem,
		Critical: false,
		Func:     func(ctx context.Context) error { return nil },
		Note:     "disabled",
	})
}
