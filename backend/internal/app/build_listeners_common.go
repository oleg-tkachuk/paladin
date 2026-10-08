package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/logfield"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

// BuildHTTPServer wraps a mux into an h2c-enabled http.Server with
// per-plane timeouts taken from cfg.HTTPServer. Connect over HTTP/2 cleartext
// is the Paladin default — the plane is fronted by an ingress that terminates TLS,
// so h2c keeps the binary contract simple and lets the gateway handle ALPN.
//
// The handler is wrapped in a clientip.Resolver built from the plane's
// real_ip_header and trusted_proxies, so every request's context carries the
// client address — the Cedar engine reads it as context.ip. An invalid
// trusted_proxies entry is an error, not a silently ignored proxy.
func BuildHTTPServer(c config.HTTPServer, mux http.Handler, l *zap.Logger) (*http.Server, error) {
	resolver, err := clientip.New(c.RealIPHeader, c.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("listener %s: %w", c.Addr, err)
	}
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
		Handler:           resolver.Middleware(mux),
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
	}, nil
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
		l.Info("using jwks verifier", zap.String("audience", audience), logfield.URL("url", a.JWKSURL))
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
// callers that own those subsystems via AddComponent below — keeps
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
		Ready:         []health.Probe{dbPing, replicaCheck(db)},
		Startup:       []health.Probe{dbPing},
	}
}

// replicaCheck shows the read replica on the health page. Never critical: a
// replica that is down or behind slows nothing, because the reads it would
// serve go to the primary — failing readiness for it would turn an
// optimisation into an outage.
func replicaCheck(db *postgres.DB) health.Check {
	return health.Check{
		Name:     "postgres-replica",
		Category: health.CategoryDatabase,
		Switch:   health.Fixed(health.ByConfig(db != nil && db.Reads.HasReplica(), config.KeyReplicaEnabled)),
		Func:     func(context.Context) error { return db.Reads.HealthErr() },
	}
}

// AddComponent registers p on the role's health page and readiness.
func AddComponent(h *health.Handler, p health.Probe) {
	h.Ready = append(h.Ready, p)
}

// capabilityComponent reports the capability subsystem. On, it checks the
// capability store answers a lookup as the verifier makes one; critical,
// as a request bearing a capability cannot be authorised without it.
func capabilityComponent(deps *SharedDeps) health.Check {
	on := deps.Capability != nil
	return health.Check{
		Name:     "capability",
		Category: health.CategorySubsystem,
		Critical: true,
		Switch:   health.Fixed(health.ByConfig(on, config.KeyCapabilityEnabled)),
		Func: func(ctx context.Context) error {
			_, err := deps.Capability.Store.Get(ctx, uuid.New())
			return storeAnswers(err, capability.ErrNotFound)
		},
	}
}

// apiTokenComponent reports the api_token subsystem. On, it checks the
// token store answers the lookup authentication makes, before any tenant
// is known. Not critical: users and capabilities still authenticate.
func apiTokenComponent(deps *SharedDeps) health.Check {
	on := deps.APIToken != nil
	return health.Check{
		Name:     "api_token",
		Category: health.CategorySubsystem,
		Switch:   health.Fixed(health.ByConfig(on, config.KeyAPITokenEnabled)),
		Func: func(ctx context.Context) error {
			_, err := deps.APIToken.Store.FindByDigest(ctx, make([]byte, sha256.Size))
			return storeAnswers(err, api_token.ErrTokenNotFound)
		},
	}
}

// storeAnswers reads a store the way the request path does, for a row that
// cannot exist: notFound is the store answering, anything else is not.
func storeAnswers(err, notFound error) error {
	if err == nil || errors.Is(err, notFound) {
		return nil
	}
	return err
}
