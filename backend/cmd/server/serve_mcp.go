package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/internal/app"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/health"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/mcp"
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
//     comes from X-PALADIN-Token per request; getServer mints a fresh
//     server (and Clients bundle) per session so concurrent MCP
//     clients never share auth state.
//
// Two transports for PALADIN-internal Connect dispatch:
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
		ctx, stop := signalCtx()
		defer stop()

		if mcpEmbedded {
			runMCPEmbedded(ctx)
			return
		}
		runMCPBridge(ctx)
	},
}

func init() {
	serveMCPCmd.Flags().StringVar(&mcpTransport, "transport", "http", `MCP client transport: "stdio" or "http"`)
	serveMCPCmd.Flags().StringVar(&mcpProfile, "profile", "", "Override the MCP tool catalog profile (read_only | agent_safe | admin | <custom>). Empty = use cfg.MCP.{Stdio,HTTP}.Profile.")
	serveMCPCmd.Flags().BoolVar(&mcpEmbedded, "embedded", false, "Co-host api/admin/iam handlers in-process; route Connect calls via inline transport instead of HTTP")
}

// runMCPBridge is the network-mode codepath. No DB; speaks Connect over HTTP
// against the upstream URLs in cfg.MCP.Upstreams.
func runMCPBridge(ctx context.Context) {
	bootstrapLog, err := logger.NewBootstrapLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build bootstrap logger: %v\n", err)
		os.Exit(1)
	}
	cfg, err := config.Load([]string{configPath}, bootstrapLog)
	if err != nil {
		bootstrapLog.Fatal("failed to load config", zap.Error(err))
	}
	l, err := logger.New(cfg.Logger)
	if err != nil {
		bootstrapLog.Fatal("failed to build logger", zap.Error(err))
	}
	logger.ReplaceGlobals(l)

	httpc := &http.Client{Timeout: 30 * time.Second}
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

	switch mcpTransport {
	case "stdio":
		// stdio sessions are one-per-process; pick up an optional
		// capability from env so a developer can experiment without
		// hand-editing JSON-RPC frames.
		runStdio(ctx, cfg, l, makeClients(
			os.Getenv("PALADIN_MCP_TOKEN"),
			os.Getenv("PALADIN_MCP_CAPABILITY"),
		))
	case "http":
		runHTTP(ctx, cfg, l, "bridge", func(r *http.Request) *mcp.Clients {
			token := r.Header.Get("X-PALADIN-Token")
			if token == "" {
				return nil
			}
			// Capability optional — forwarded only when the MCP host
			// supplies it. Absent capability → JWT-only auth flow.
			cap := r.Header.Get("X-PALADIN-Capability")
			return makeClients(token, cap)
		})
	default:
		l.Fatal("unknown transport (expected stdio|http)", zap.String("transport", mcpTransport))
	}
}

// runMCPEmbedded co-hosts api/admin/iam handlers in this process and routes
// Connect calls through the inline transport. Boots the full SharedDeps
// graph the listener subcommands use; the only difference is no TCP.
func runMCPEmbedded(ctx context.Context) {
	cfg, l, db, otelShutdown := boot(ctx)
	defer db.Close()
	defer flushOTel(otelShutdown)

	deps, err := app.BuildSharedDeps(ctx, cfg, db, l)
	if err != nil {
		l.Fatal("failed to build shared deps", zap.Error(err))
	}

	meta := app.BuildMeta{
		Version:    version,
		Commit:     commit,
		BuildTime:  buildTime,
		ConfigPath: configPath,
	}

	muxes, err := app.BuildEmbedMuxes(ctx, deps, meta)
	if err != nil {
		l.Fatal("failed to build embed muxes", zap.Error(err))
	}

	inlineHandlers := mcp.InlineHandlers{
		Data:  muxes.Data,
		Admin: muxes.Admin,
		IAM:   muxes.IAM,
	}

	makeInlineClients := func(bearer, capToken string) *mcp.Clients {
		return mcp.NewInlineClientsWithCapability(inlineHandlers, bearer, capToken)
	}

	switch mcpTransport {
	case "stdio":
		runStdio(ctx, cfg, l, makeInlineClients(
			os.Getenv("PALADIN_MCP_TOKEN"),
			os.Getenv("PALADIN_MCP_CAPABILITY"),
		))
	case "http":
		runHTTP(ctx, cfg, l, "embedded", func(r *http.Request) *mcp.Clients {
			token := r.Header.Get("X-PALADIN-Token")
			if token == "" {
				return nil
			}
			cap := r.Header.Get("X-PALADIN-Capability")
			return makeInlineClients(token, cap)
		})
	default:
		l.Fatal("unknown transport (expected stdio|http)", zap.String("transport", mcpTransport))
	}
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
func runStdio(ctx context.Context, cfg config.Config, l *zap.Logger, clients *mcp.Clients) {
	profile := pickProfile(mcpProfile, cfg.MCP.Stdio.Profile)
	filter := mcp.NewToolFilter(cfg.MCP, profile)
	server := mcp.NewServer("paladin-mcp", version, clients, filter)

	l.Info("mcp stdio starting", zap.String("profile", profile))
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		l.Fatal("mcp stdio run failed", zap.Error(err))
	}
}

// runHTTP runs the streamable-HTTP MCP transport. The clientsFor closure
// is invoked per-session; it returns nil to refuse the session (which
// the SDK surfaces as 400 Bad Request to the LLM client).
func runHTTP(ctx context.Context, cfg config.Config, l *zap.Logger, modeLabel string, clientsFor func(*http.Request) *mcp.Clients) {
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
	// agent_subject enrichment: the agent's bearer JWT rides in X-PALADIN-Token.
	// subjectFn verifies its signature (NOT its audience — an agent token
	// targets whichever plane it calls: admin/data/iam) and reads the `sub`
	// claim for the session's display label. Degrades to blank when no signing
	// key is configured (a thin bridge) or the token is absent/invalid.
	sessions := mcp.NewSessionRegistry()
	subjectFn := agentSubjectFn(ctx, cfg.Auth, l)
	tracked := mcp.TrackSessions(handler, sessions, subjectFn)

	mux := http.NewServeMux()
	mux.Handle("/mcp", tracked)

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

	go func() {
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
		l.Fatal("mcp http listen failed", zap.Error(err))
	}
}

// agentSubjectFn returns a TrackSessions subjectFn that extracts the verified
// `sub` claim from the agent's X-PALADIN-Token for the session display label. It is
// audience-agnostic on purpose (ExpectedAudience left empty) — an agent token
// targets whichever plane it calls (admin/data/iam). Returns a no-op (blank
// subject) when no usable verifier can be built (e.g. a thin bridge with no
// signing key), so the column stays empty rather than trusting an unverified
// token.
func agentSubjectFn(ctx context.Context, a config.Auth, l *zap.Logger) func(*http.Request) string {
	blank := func(*http.Request) string { return "" }
	var verifier auth.TokenVerifier
	switch {
	case a.JWKSURL != "":
		v := auth.NewJWKSVerifier(a.JWKSURL)
		v.ExpectedIssuer = a.Issuer
		v.Leeway = a.Leeway // ExpectedAudience left empty → any audience
		if err := v.Start(ctx); err != nil {
			l.Warn("agent_subject disabled: jwks verifier failed to start", zap.Error(err))
			return blank
		}
		verifier = v
	case a.SigningKey != "":
		verifier = &auth.JWTVerifier{
			Key:            []byte(a.SigningKey),
			ExpectedIssuer: a.Issuer,
			Leeway:         a.Leeway,
		}
	default:
		return blank
	}
	return func(r *http.Request) string {
		tok := r.Header.Get("X-PALADIN-Token")
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
