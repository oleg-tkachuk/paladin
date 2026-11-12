package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
	"github.com/oleg-tkachuk/paladin/backend/internal/session"
	"github.com/oleg-tkachuk/paladin/backend/internal/tlsconfig"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// sessionPlane names the session role's listener in logs.
const sessionPlane = "session"

// sessionIAMCallTimeout bounds one call to the iam plane: a login, an
// exchange, a rotation. Forwarded calls have none of their own; a stream or
// an upload lasts as long as the browser keeps it.
const sessionIAMCallTimeout = 30 * time.Second

// iamComponent names the iam plane on the session role's health page.
const iamComponent = "iam"

// BuildSessionHandler builds the session role's handler from its config:
// the planes it forwards to, the iam plane it mints at, and the iam role's
// JWKS it verifies refresh tokens by (ADR-0048).
func BuildSessionHandler(ctx context.Context, cfg config.Config, meta BuildMeta, l *zap.Logger) (*session.Handler, error) {
	up := cfg.Session.Upstreams
	forwardTransport, err := sessionTransport(up)
	if err != nil {
		return nil, err
	}
	planes := map[session.Plane]*url.URL{}
	for plane, raw := range map[session.Plane]string{
		session.PlaneData: up.DataURL, session.PlaneAdmin: up.AdminURL, session.PlaneIAM: up.IAMURL,
	} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("session.upstreams: the %s plane's URL %q does not parse", plane, raw)
		}
		planes[plane] = u
	}
	iamHTTP := &http.Client{Timeout: sessionIAMCallTimeout, Transport: forwardTransport}
	iam := paladiniamv1connect.NewAuthServiceClient(connect.NewClient(connecthttp.NewTransport(iamHTTP, up.IAMURL)))
	// Refresh tokens are the iam plane's audience; the role holds no key, so
	// it reads their age by the iam role's JWKS like any other verifier.
	verifier, err := BuildVerifier(ctx, cfg.Auth, nil, auth.AudienceIAM, l)
	if err != nil {
		return nil, err
	}
	return session.New(session.Options{
		IAM:         iam,
		Planes:      planes,
		Transport:   forwardTransport,
		Refresh:     refreshAges{verifier},
		Origins:     cfg.Session.ConsoleOrigins,
		RotateAfter: cfg.Session.RotateAfter,
		Version:     meta.Version,
		Log:         l.Named(sessionPlane),
	})
}

// sessionTransport dials the planes with session.upstreams.tls: the internal
// CA their https listeners chain to.
func sessionTransport(up config.MCPUpstreams) (http.RoundTripper, error) {
	tr, err := tlsconfig.ClientTransport(up.TLS.CertPath, up.TLS.KeyPath, up.TLS.CaPath, up.TLS.ServerName, up.TLS.InsecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("session.upstreams.tls: %w", err)
	}
	if tr == nil {
		return http.DefaultTransport, nil
	}
	return tr, nil
}

// refreshAges reads a refresh token's verified iat for the session role.
type refreshAges struct{ v auth.TokenVerifier }

func (r refreshAges) IssuedAt(ctx context.Context, token string) (time.Time, error) {
	p, err := r.v.Verify(ctx, token)
	if err != nil {
		return time.Time{}, err
	}
	return p.IssuedAt, nil
}

// sessionHealth is the role's health: its one dependency is the iam plane,
// shown but never critical. The role cannot sign anyone in without it, and
// taking every replica out of service while iam restarts would only add the
// console to the outage.
func sessionHealth(cfg config.Config, l *zap.Logger, client *http.Client) *health.Handler {
	iamLive := cfg.Session.Upstreams.IAMURL + health.LivePath
	return &health.Handler{
		Logger:        l.Named("health"),
		LogSuccesses:  cfg.Runtime.LogProbes,
		SnapshotToken: cfg.Runtime.HealthSnapshotToken,
		Ready: []health.Probe{health.Check{
			Name:     iamComponent,
			Category: health.CategoryUpstream,
			Func: func(ctx context.Context) error {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, iamLive, nil)
				if err != nil {
					return err
				}
				res, err := client.Do(req)
				if err != nil {
					return err
				}
				_ = res.Body.Close()
				if res.StatusCode != http.StatusOK {
					return fmt.Errorf("iam answered %d", res.StatusCode)
				}
				return nil
			},
		}},
	}
}

// BuildSessionListeners is the session role's listener and its metrics.
func BuildSessionListeners(ctx context.Context, cfg config.Config, meta BuildMeta, l *zap.Logger, metrics observability.MetricsHandler) (ListenerSet, error) {
	h, err := BuildSessionHandler(ctx, cfg, meta, l)
	if err != nil {
		return ListenerSet{}, err
	}
	tr, err := sessionTransport(cfg.Session.Upstreams)
	if err != nil {
		return ListenerSet{}, err
	}
	// The probe's context bounds each check; the client adds no timeout.
	hh := sessionHealth(cfg, l, &http.Client{Transport: tr}).WithRole(string(config.RoleSession))
	mux := http.NewServeMux()
	hh.Register(mux)
	mux.Handle(session.PathPrefix+"/", h)
	srv, err := BuildHTTPServer(cfg.Session.Server, mux, l)
	if err != nil {
		return ListenerSet{}, err
	}
	listeners := []HTTPListener{{Plane: sessionPlane, Server: srv, TLS: cfg.Session.Server.TLS}}
	if m := metricsListener(cfg.OTel, metrics); m != nil {
		listeners = append(listeners, *m)
	}
	l.Info("session role ready to listen",
		zap.String("addr", cfg.Session.Server.Addr), zap.Strings("console_origins", cfg.Session.ConsoleOrigins))
	return ListenerSet{Listeners: listeners, Health: hh}, nil
}

func provideSessionListenerSet(cfg config.Config, meta BuildMeta, l *zap.Logger, metrics observability.MetricsHandler) (ListenerSet, error) {
	return BuildSessionListeners(context.Background(), cfg, meta, l, metrics)
}

// provideSessionApp is the *App of a role with no database and no shared
// dependencies.
func provideSessionApp(meta BuildMeta, cfg config.Config, l *zap.Logger, otel observability.ShutdownFunc, set ListenerSet) *App {
	return NewContainer(meta.Version, meta.Commit, meta.BuildTime, cfg, l, set.Listeners, nil, otel, nil, &atomic.Bool{}).WithHealth(set.Health)
}

// SessionModule is the fx graph for `paladin serve session` (ADR-0048): no
// database, no storage, no key — config, telemetry and the one listener.
var SessionModule = fx.Options(
	LiteModule,
	fx.Provide(ProvideOTel, provideSessionListenerSet, provideSessionApp),
	fx.Invoke(RunApp),
)
