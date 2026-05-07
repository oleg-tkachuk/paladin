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
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

// Flags scoped to `serve mcp`. Cobra binds them in init().
var (
	mcpTransport  string // "stdio" | "http"
	mcpAllowWrite bool
	mcpEmbedded   bool
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
	serveMCPCmd.Flags().BoolVar(&mcpAllowWrite, "allow-write", false, "Enable mutating tools (off by default)")
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
	cfg, err := config.Load(configPath, bootstrapLog)
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
	cfg, l, db := boot(ctx)
	defer func() { _ = db.Close }()

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

// runStdio runs the stdio MCP transport with the supplied Connect clients.
// The clients are built once (network mode) or per-call wouldn't make
// sense on stdio because there is exactly one session per process.
func runStdio(ctx context.Context, cfg config.Config, l *zap.Logger, clients *mcp.Clients) {
	allow := mcpAllowWrite || cfg.MCP.Stdio.AllowWrite
	server := mcp.NewServer("paladin-mcp", version, clients, allow)

	l.Info("mcp stdio starting", zap.Bool("allow_write", allow))
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		l.Fatal("mcp stdio run failed", zap.Error(err))
	}
}

// runHTTP runs the streamable-HTTP MCP transport. The clientsFor closure
// is invoked per-session; it returns nil to refuse the session (which
// the SDK surfaces as 400 Bad Request to the LLM client).
func runHTTP(ctx context.Context, cfg config.Config, l *zap.Logger, modeLabel string, clientsFor func(*http.Request) *mcp.Clients) {
	allow := mcpAllowWrite || cfg.MCP.HTTP.AllowWrite

	handler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		clients := clientsFor(r)
		if clients == nil {
			return nil
		}
		return mcp.NewServer("paladin-mcp", version, clients, allow)
	}, &mcpsdk.StreamableHTTPOptions{
		SessionTimeout: cfg.MCP.HTTP.SessionTimeout,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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
		zap.Bool("allow_write", allow),
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		l.Fatal("mcp http listen failed", zap.Error(err))
	}
}
