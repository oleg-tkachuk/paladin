package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/fx"
	"go.uber.org/zap"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/backend/internal/app"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/mcp"
	"github.com/oleg-tkachuk/paladin/backend/internal/observability"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/backend/internal/utils"
)

// Flags scoped to `serve mcp`. Cobra binds them in init().
var (
	mcpTransport string // "stdio" | "http"
	mcpProfile   string // "" → use cfg.MCP.{Stdio,HTTP}.Profile, else override
	mcpEmbedded  bool
)

// serveMCPCmd hosts the MCP server using the official modelcontextprotocol/
// go-sdk. Two transports for the protocol facing the LLM client:
//
//   - stdio:  reads JSON-RPC frames from stdin, writes to stdout. For
//     local IDE plugins (Claude Desktop / Cursor / Cline). All
//     logs route to stderr to keep the protocol stream clean.
//
//   - http:   streamable-HTTP per the MCP spec, mounted on /mcp. Token
//     comes from X-Paladin-Token per request; getServer mints a fresh
//     server (and Clients bundle) per session so concurrent MCP
//     clients never share auth state.
//
// Two transports for Paladin-internal Connect dispatch:
//
//   - bridge (default): MCP server holds Connect HTTP clients pointed at
//     cfg.MCP.Upstreams.{admin,data,iam}URL. Suitable when MCP runs in
//     its own pod separately from api/admin. No DB connection needed.
//
//   - embedded (--embedded): MCP server runs in the same process as the
//     api/admin/iam handlers, plumbed through internal/mcp.NewInlineTransport.
//     Avoids the network roundtrip; needed for laptop dev (no infra),
//     edge deploys (one binary near the agent runtime), and integration
//     testing. Requires DB + bootstrap; effectively a single-process
//     superset of `serve api` + `serve admin` minus their TCP listeners.
var serveMCPCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the MCP server (stdio or streamable-HTTP, bridge or embedded)",
	Run: func(cmd *cobra.Command, args []string) {
		// Two modes, two fx graphs. Embedded co-hosts the api/admin/iam
		// handlers, so it needs the full DB-backed BaseModule; bridge is a
		// pure Connect-over-HTTP proxy with no DB, so it rides the lighter
		// LiteModule (config + logger only — no pool is ever opened).
		roleModule := mcpBridgeModule
		if mcpEmbedded {
			roleModule = mcpEmbeddedModule
		}
		fx.New(
			fx.Supply(configSource()),
			fx.Supply(buildMeta()),
			roleModule,
		).Run()
	},
}

// mcpBridgeModule / mcpEmbeddedModule are the two mcp role graphs. Bridge rides
// the DB-less LiteModule (pure Connect-over-HTTP proxy); embedded rides the
// full BaseModule (co-hosts the api/admin/iam handlers). Extracted so both the
// command and the graph-validation test (fx_validate_test.go) reference the
// same wiring.
var (
	mcpBridgeModule = fx.Options(
		app.LiteModule,
		fx.Provide(provideMCPBridgeRunner),
		fx.Invoke(runMCPServer),
	)
	mcpEmbeddedModule = fx.Options(
		app.BaseModule,
		fx.Provide(provideMCPEmbeddedRunner),
		fx.Invoke(runMCPServer),
	)
)

func init() {
	serveMCPCmd.Flags().StringVar(&mcpTransport, "transport", "http", `MCP client transport: "stdio" or "http"`)
	serveMCPCmd.Flags().StringVar(&mcpProfile, "profile", "", "Override the MCP tool catalog profile (read_only | agent_safe | admin | <custom>). Empty = use cfg.MCP.{Stdio,HTTP}.Profile.")
	serveMCPCmd.Flags().BoolVar(&mcpEmbedded, "embedded", false, "Co-host api/admin/iam handlers in-process; route Connect calls via inline transport instead of HTTP")
}

// mcpRunner is the transport-agnostic product both modes build. It carries the
// config + logger, the mode's per-request / stdio Clients factories, a mode
// label for logs, and an optional onStop that releases mode-owned resources
// (embedded: DB + OTel) after the transport has drained. runMCPServer consumes
// exactly this, so the two modes differ only in how the runner is provided.
type mcpRunner struct {
	cfg       config.Config
	l         *zap.Logger
	modeLabel string
	// stdioClients builds the single process-wide Clients for the stdio
	// transport (one session per process).
	stdioClients func() *mcp.Clients
	// httpClients builds per-request Clients for the streamable-HTTP transport;
	// it returns nil to refuse a session (missing bearer).
	httpClients func(*http.Request) *mcp.Clients
	// onStop releases mode-owned resources after the transport drains. Bridge
	// owns none and leaves it nil.
	onStop func(context.Context)
}

// provideMCPBridgeRunner is the network-mode constructor. No DB; speaks Connect
// over HTTP against the upstream URLs in cfg.MCP.Upstreams.
func provideMCPBridgeRunner(cfg config.Config, l *zap.Logger) mcpRunner {
	httpc, err := upstreamHTTPClient(cfg.MCP.Upstreams)
	if err != nil {
		// A bridge that cannot dial its planes has nothing to serve: every
		// tools/call would fail with an opaque transport error. Fail loudly
		// at construction instead.
		l.Fatal("mcp bridge: build upstream client", zap.Error(err))
	}
	makeClients := func(bearer, capToken string) *mcp.Clients {
		return mcp.NewClientsWithCapability(
			httpc,
			cfg.MCP.Upstreams.AdminURL,
			cfg.MCP.Upstreams.DataURL,
			cfg.MCP.Upstreams.IAMURL,
			bearer,
			capToken,
		)
	}
	return mcpRunner{
		cfg:       cfg,
		l:         l,
		modeLabel: "bridge",
		stdioClients: func() *mcp.Clients {
			// stdio sessions are one-per-process; pick up an optional
			// capability from env so a developer can experiment without
			// hand-editing JSON-RPC frames.
			return makeClients(os.Getenv("PALADIN_MCP_TOKEN"), os.Getenv("PALADIN_MCP_CAPABILITY"))
		},
		httpClients: func(r *http.Request) *mcp.Clients {
			token := mcp.BearerToken(r)
			if token == "" {
				return nil
			}
			// Capability optional — forwarded only when the MCP host
			// supplies it. Absent capability → JWT-only auth flow.
			return makeClients(token, r.Header.Get("X-Paladin-Capability"))
		},
	}
}

// provideMCPEmbeddedRunner co-hosts api/admin/iam handlers in this process and
// routes Connect calls through the inline transport. It consumes the full
// SharedDeps graph the listener subcommands use (via app.BaseModule); the only
// difference is no TCP. It owns the DB + OTel it was handed, so onStop closes
// them once the transport has drained.
func provideMCPEmbeddedRunner(
	cfg config.Config,
	l *zap.Logger,
	deps *app.SharedDeps,
	meta app.BuildMeta,
	db *postgres.DB,
	otel observability.ShutdownFunc,
) (mcpRunner, error) {
	muxes, err := app.BuildEmbedMuxes(context.Background(), deps, meta)
	if err != nil {
		return mcpRunner{}, fmt.Errorf("build embed muxes: %w", err)
	}
	inlineHandlers := mcp.InlineHandlers{
		Data:  muxes.Data,
		Admin: muxes.Admin,
		IAM:   muxes.IAM,
	}
	makeInlineClients := func(bearer, capToken string) *mcp.Clients {
		return mcp.NewInlineClientsWithCapability(inlineHandlers, bearer, capToken)
	}
	return mcpRunner{
		cfg:       cfg,
		l:         l,
		modeLabel: "embedded",
		stdioClients: func() *mcp.Clients {
			return makeInlineClients(os.Getenv("PALADIN_MCP_TOKEN"), os.Getenv("PALADIN_MCP_CAPABILITY"))
		},
		httpClients: func(r *http.Request) *mcp.Clients {
			token := mcp.BearerToken(r)
			if token == "" {
				return nil
			}
			return makeInlineClients(token, r.Header.Get("X-Paladin-Capability"))
		},
		onStop: func(context.Context) {
			// Bounded (5s) fresh-context OTel flush — see the same note in the
			// other roles: the fx OnStop context carries the 90s StopTimeout, so
			// a slow/unreachable OTLP endpoint would otherwise block teardown
			// past the pod's termination grace and get SIGKILLed.
			flushOTel(otel)
			deps.StopWatchers() // release the Cedar LISTEN conn before pool close
			db.Close()
		},
	}, nil
}

// runMCPServer is the mcp role's fx lifecycle, shared by both modes. OnStart
// spawns the selected transport (stdio blocks on the JSON-RPC stream; http
// blocks on its listener) in a goroutine; a run error escalates to an exit-1
// fx shutdown, and a clean return (stdio EOF, or a signal that cancelled the
// transport) triggers a normal shutdown so fx.App.Run() unblocks. OnStop
// cancels the transport, waits for it to drain, then runs the mode's onStop
// (embedded: OTel flush + DB close) — the same teardown the pre-fx defers did.
func runMCPServer(lc fx.Lifecycle, sd fx.Shutdowner, r mcpRunner) {
	// workCtx bounds the transport; cancelled OnStop. done closes when the
	// transport goroutine returns so OnStop can wait for a clean drain.
	workCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				var err error
				switch mcpTransport {
				case "stdio":
					err = runStdio(workCtx, r.cfg, r.l, r.stdioClients())
				case "http":
					err = runHTTP(workCtx, r.cfg, r.l, r.modeLabel, r.httpClients)
				default:
					r.l.Error("unknown transport (expected stdio|http)", zap.String("transport", mcpTransport))
					_ = sd.Shutdown(fx.ExitCode(1))
					return
				}
				if err != nil && !errorsIsCancelled(err) {
					r.l.Error("mcp transport exited", zap.Error(err))
					_ = sd.Shutdown(fx.ExitCode(1))
					return
				}
				// Clean return (ctx cancel on signal, or stdio EOF) — ask fx
				// to shut down so fx.App.Run() unblocks. A concurrent
				// signal-driven Shutdown makes this a harmless no-op.
				_ = sd.Shutdown()
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			cancel()
			<-done
			if r.onStop != nil {
				r.onStop(ctx)
			}
			_ = r.l.Sync()
			return nil
		},
	})
}

// pickProfile chooses the MCP tool catalog profile name. CLI flag wins
// over config; empty config falls back to "read_only" — the same
// closed-by-default discipline the gating layer applies. Operators
// who deliberately want a tighter or looser catalog set the flag /
// cfg explicitly.
func pickProfile(flag, cfgValue string) string {
	if flag != "" {
		return flag
	}
	if cfgValue != "" {
		return cfgValue
	}
	return "read_only"
}

// runStdio runs the stdio MCP transport with the supplied Connect clients.
// The clients are built once (network mode) or per-call wouldn't make
// sense on stdio because there is exactly one session per process.
func runStdio(ctx context.Context, cfg config.Config, l *zap.Logger, clients *mcp.Clients) error {
	profile := pickProfile(mcpProfile, cfg.MCP.Stdio.Profile)
	filter := mcp.NewToolFilter(cfg.MCP, profile)
	server := mcp.NewServer("paladin-mcp", version, clients, filter)

	l.Info("mcp stdio starting", zap.String("profile", profile))
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp stdio run: %w", err)
	}
	return nil
}

// runHTTP runs the streamable-HTTP MCP transport. The clientsFor closure
// is invoked per-session; it returns nil to refuse the session (which the SDK
// surfaces as 400 Bad Request). Credential-less requests are turned away with
// 401 before reaching it — see RequireToken / RequireBearer below.
func runHTTP(ctx context.Context, cfg config.Config, l *zap.Logger, modeLabel string, clientsFor func(*http.Request) *mcp.Clients) error {
	profile := pickProfile(mcpProfile, cfg.MCP.HTTP.Profile)
	filter := mcp.NewToolFilter(cfg.MCP, profile)

	handler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		clients := clientsFor(r)
		if clients == nil {
			return nil
		}
		return mcp.NewServer("paladin-mcp", version, clients, filter)
	}, &mcpsdk.StreamableHTTPOptions{
		SessionTimeout: cfg.MCP.HTTP.SessionTimeout,
	})

	// Track live sessions off the wire — the SDK exposes no enumeration hook,
	// so the middleware records Mcp-Session-Id activity into a process-local
	// registry. A reaper (below) evicts idle sessions.
	//
	// agent_subject enrichment: the agent's bearer JWT rides in X-Paladin-Token.
	// subjectFn verifies its signature (NOT its audience — an agent token
	// targets whichever plane it calls: admin/data/iam) and reads the `sub`
	// claim for the session's display label. Degrades to blank when no signing
	// key is configured (a thin bridge) or the token is absent/invalid.
	sessions := mcp.NewSessionRegistry()
	subjectFn := agentSubjectFn(ctx, cfg.Auth, l)
	tracked := mcp.TrackSessions(handler, sessions, subjectFn)

	mux := http.NewServeMux()

	// OAuth 2.1 Resource-Server posture (ADR-0008). When enabled, the MCP
	// endpoint advertises where to authenticate (RFC 9728 / RFC 8414) and
	// challenges unauthenticated requests with 401 + WWW-Authenticate so a
	// standard MCP client can run discovery. Disabled → the legacy
	// X-Paladin-Token path still answers a credential-less request with 401
	// (RequireToken), just without a resource_metadata pointer.
	mcpHandler := http.Handler(tracked)
	if cfg.MCP.OAuth.Enabled {
		mux.Handle(mcp.WellKnownProtectedResource, mcp.ProtectedResourceMetadataHandler(cfg.MCP.OAuth))
		if cfg.MCP.OAuth.AuthorizationServer.Issuer != "" {
			mux.Handle(mcp.WellKnownAuthorizationServer, mcp.AuthorizationServerMetadataHandler(cfg.MCP.OAuth.AuthorizationServer))
		}
		// Edge verifier: signature + issuer + expiry, NOT audience (an agent
		// token targets whichever plane it calls; the planes do per-audience
		// checks downstream). Without a usable verifier we can't enforce, so
		// log and fall back to serving metadata only.
		if v := buildAgentVerifier(ctx, cfg.Auth, l); v != nil {
			mcpHandler = mcp.RequireBearer(tracked, v, oauthResourceMetadataURL(cfg.MCP.OAuth.ResourceURL))
		} else {
			l.Warn("mcp oauth: bearer challenge disabled — no usable JWT verifier (set auth.signing_key or auth.jwks_url)")
			mcpHandler = mcp.RequireToken(mcpHandler)
		}
	} else {
		// Legacy X-Paladin-Token path: no discovery metadata to advertise, but
		// a request with no credential is still 401, not the SDK's 400.
		mcpHandler = mcp.RequireToken(mcpHandler)
	}
	mux.Handle("/mcp", mcpHandler)

	// Expose the registry for the admin plane's MCPInspectService.ListSessions
	// proxy. The endpoint re-verifies the admin-audience JWT the admin plane
	// forwards and requires the platform-admin role — no new shared secret,
	// the call is gated by the same RBAC as the admin service. If no usable
	// verifier can be built (e.g. a bridge with no signing key configured),
	// skip the endpoint; the admin proxy treats unreachable as an empty list.
	if verifier, verr := app.BuildVerifier(ctx, cfg.Auth, auth.AudienceAdmin, l); verr != nil {
		l.Warn("mcp /sessions disabled: no usable JWT verifier", zap.Error(verr))
	} else {
		mux.Handle("GET /sessions", mcp.SessionsHandler(sessions, verifier))
		// /status answers what only the bridge can: whether it can currently
		// reach the planes it proxies to. The admin plane treats an
		// unreachable bridge as an empty session list, so without this "the
		// bridge is down" and "nobody is using MCP" look identical to an
		// operator — on the page they open precisely when something is wrong.
		// Probe over the same client the bridge uses for its upstream calls.
		// The default client has no CA bundle, so it reported all three planes
		// as "certificate signed by unknown authority" while the bridge was
		// reaching them without trouble.
		probeClient, perr := upstreamHTTPClient(cfg.MCP.Upstreams)
		if perr != nil {
			// Construction already fataled on this in the bridge runner; here
			// it can only mean the config changed underneath us. Fall back to
			// the default client rather than dropping /status entirely.
			l.Warn("mcp /status: falling back to the default probe client", zap.Error(perr))
			probeClient = nil
		}
		mux.Handle("GET /status", mcp.StatusHandler(sessions, verifier, []mcp.UpstreamTarget{
			{Name: "admin", URL: cfg.MCP.Upstreams.AdminURL},
			{Name: "data", URL: cfg.MCP.Upstreams.DataURL},
			{Name: "iam", URL: cfg.MCP.Upstreams.IAMURL},
		}, mcp.HealthProbe(probeClient)))
	}

	// The streamable transport has no reliable disconnect signal (clients can
	// vanish without a DELETE), so evict sessions idle past the session
	// timeout to keep the registry bounded.
	reapIdle := cfg.MCP.HTTP.SessionTimeout
	if reapIdle <= 0 {
		reapIdle = 5 * time.Minute
	}
	go func() {
		t := time.NewTicker(reapIdle)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				sessions.Reap(reapIdle)
			}
		}
	}()

	// Probe surface — same shape as api / admin / worker. /livez +
	// /readyz + /startupz follow the K8s contract; /system/health.json
	// is the structured snapshot the BFF aggregator fans out to. Probe
	// set is intentionally minimal here: an MCP pod runs in two modes
	// (bridge → no DB, embedded → has DB). Bridge can't probe DB; we
	// expose a "process" component so the snapshot is non-empty and the
	// UI can show "mcp: healthy" rather than treating an empty
	// components list as missing data. Upstream probes (admin / data /
	// iam reachability from the bridge) are BACKLOG — needs an MCP
	// client Ping method.
	healthH := &health.Handler{
		Logger:       l.Named("health"),
		LogSuccesses: cfg.Runtime.LogProbes,
		Ready: []health.Check{{
			Name:     "process",
			Category: health.CategorySubsystem,
			Critical: true,
			Func:     func(context.Context) error { return nil },
		}},
	}
	healthH.WithRole("mcp")
	healthH.Register(mux)

	// No /metrics here. runHTTP does not build SharedDeps — the MCP bridge
	// holds no repositories and emits no paladin_* instruments of its own; it
	// proxies to the planes, which are scraped. Wiring deps through purely to
	// expose go_* would be a lot of plumbing for runtime stats already
	// visible per-pod through kube-state-metrics.

	// Backwards-compatible alias for the chart's existing /healthz path.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/livez"
		mux.ServeHTTP(w, r2)
	})

	addr := cfg.MCP.HTTP.Addr
	if addr == "" {
		addr = ":8095"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() { // #nosec G118 -- detached ctx is intentional; the parent ctx is already canceled at shutdown time
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	l.Info("mcp http starting",
		zap.String("addr", addr),
		zap.String("mode", modeLabel),
		zap.String("profile", profile),
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("mcp http listen: %w", err)
	}
	return nil
}

// agentSubjectFn returns a TrackSessions subjectFn that extracts the verified
// `sub` claim from the agent's X-Paladin-Token for the session display label. It is
// audience-agnostic on purpose (ExpectedAudience left empty) — an agent token
// targets whichever plane it calls (admin/data/iam). Returns a no-op (blank
// subject) when no usable verifier can be built (e.g. a thin bridge with no
// signing key), so the column stays empty rather than trusting an unverified
// token.
func agentSubjectFn(ctx context.Context, a config.Auth, l *zap.Logger) func(*http.Request) string {
	verifier := buildAgentVerifier(ctx, a, l)
	if verifier == nil {
		return func(*http.Request) string { return "" }
	}
	return func(r *http.Request) string {
		tok := mcp.BearerToken(r)
		if tok == "" {
			return ""
		}
		p, err := verifier.Verify(r.Context(), tok)
		if err != nil {
			return ""
		}
		return p.Subject
	}
}

// buildAgentVerifier constructs the audience-agnostic JWT verifier the MCP
// edge uses for both session-subject enrichment and the OAuth bearer
// challenge (ADR-0008). Signature + issuer + expiry are checked;
// ExpectedAudience is deliberately left empty because an agent token targets
// whichever plane it calls (admin/data/iam) — the planes enforce audience
// downstream. Returns nil when no key/JWKS is configured (a thin bridge),
// so callers degrade gracefully rather than trusting unverified tokens.
func buildAgentVerifier(ctx context.Context, a config.Auth, l *zap.Logger) auth.TokenVerifier {
	switch {
	case a.JWKSURL != "":
		v := auth.NewJWKSVerifier(a.JWKSURL)
		v.ExpectedIssuer = a.Issuer
		v.Leeway = a.Leeway
		if err := v.Start(ctx); err != nil {
			l.Warn("mcp token verifier: jwks failed to start", zap.Error(err))
			return nil
		}
		return v
	case a.SigningKey != "":
		return &auth.JWTVerifier{
			Key:            []byte(a.SigningKey),
			ExpectedIssuer: a.Issuer,
			Leeway:         a.Leeway,
		}
	default:
		return nil
	}
}

// oauthResourceMetadataURL builds the absolute URL of the RFC 9728
// protected-resource document from the configured resource URL's origin. The
// WWW-Authenticate challenge points clients here to begin discovery. Falls
// back to the root-relative path when resourceURL is empty/unparseable —
// clients resolve it against the request origin.
func oauthResourceMetadataURL(resourceURL string) string {
	if resourceURL == "" {
		return mcp.WellKnownProtectedResource
	}
	u, err := url.Parse(resourceURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return mcp.WellKnownProtectedResource
	}
	return u.Scheme + "://" + u.Host + mcp.WellKnownProtectedResource
}

// upstreamHTTPClient builds the transport the bridge uses to reach the admin /
// data / iam planes. The planes' certificates chain to the internal mTLS CA,
// which the system roots do not contain, so an https:// upstream needs that
// bundle installed here or verification fails. CertPath/KeyPath are forwarded
// too, so the client can present a certificate to planes running client_auth
// stricter than "permissive". Installing the transport is harmless for
// plaintext upstreams — it is simply never consulted.
func upstreamHTTPClient(up config.MCPUpstreams) (*http.Client, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	// No trust material configured → nothing to install. Plaintext upstreams
	// take this path; so does an https:// upstream with insecure_skip_verify
	// left off, which config validation has already rejected.
	if up.TLS.CaPath == "" && up.TLS.CertPath == "" && !up.TLS.InsecureSkipVerify {
		return c, nil
	}
	tlsCfg, err := utils.NewTLSConfig(
		up.TLS.CertPath, up.TLS.KeyPath, up.TLS.CaPath,
		up.TLS.ServerName, up.TLS.InsecureSkipVerify,
	)
	if err != nil {
		return nil, fmt.Errorf("mcp upstream tls: %w", err)
	}
	tlsCfg.MinVersion = tls.VersionTLS12
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	c.Transport = tr
	return c, nil
}
