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

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

// Flags scoped to `serve mcp`. Cobra binds them in init().
var (
	mcpTransport  string // "stdio" | "http"
	mcpAllowWrite bool
)

// serveMCPCmd hosts the MCP server using the official modelcontextprotocol/
// go-sdk. Two transports:
//
//   - stdio:  reads JSON-RPC frames from stdin, writes to stdout. For
//     local IDE plugins (Claude Desktop / Cursor / Cline). All
//     logs route to stderr to keep the protocol stream clean.
//
//   - http:   streamable-HTTP transport per the MCP spec. Mounts on /mcp.
//     Token comes from X-PALADIN-Token header per request, injected
//     into the PALADIN Connect calls. getServer hook mints a fresh
//     server (and Clients bundle) per session so concurrent MCP
//     clients never share auth state.
//
// The MCP server here is the bridge implementation in internal/mcp: it
// dispatches each MCP tool call into a Connect RPC against the PALADIN
// admin/data/iam planes. Inline mode (sharing the wire graph in the
// same process as `serve api`) is BACKLOG Phase 3.
var serveMCPCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the MCP server (stdio or streamable-HTTP)",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signalCtx()
		defer stop()

		// Logger only — no DB, the MCP bridge speaks Connect over HTTP.
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

		switch mcpTransport {
		case "stdio":
			runMCPStdio(ctx, cfg, l)
		case "http":
			runMCPHTTP(ctx, cfg, l)
		default:
			l.Fatal("unknown transport (expected stdio|http)", zap.String("transport", mcpTransport))
		}
	},
}

func init() {
	serveMCPCmd.Flags().StringVar(&mcpTransport, "transport", "http", `MCP transport: "stdio" or "http"`)
	serveMCPCmd.Flags().BoolVar(&mcpAllowWrite, "allow-write", false, "Enable mutating tools (off by default)")
}

func runMCPStdio(ctx context.Context, cfg config.Config, l *zap.Logger) {
	clients := mcp.NewClients(
		&http.Client{Timeout: 30 * time.Second},
		cfg.MCP.Upstreams.AdminURL,
		cfg.MCP.Upstreams.DataURL,
		cfg.MCP.Upstreams.IAMURL,
		os.Getenv("PALADIN_MCP_TOKEN"),
	)
	allow := mcpAllowWrite || cfg.MCP.Stdio.AllowWrite
	server := mcp.NewServer("paladin-mcp", version, clients, allow)

	l.Info("mcp stdio starting", zap.Bool("allow_write", allow))
	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		l.Fatal("mcp stdio run failed", zap.Error(err))
	}
}

func runMCPHTTP(ctx context.Context, cfg config.Config, l *zap.Logger) {
	httpc := &http.Client{Timeout: 30 * time.Second}
	allow := mcpAllowWrite || cfg.MCP.HTTP.AllowWrite

	handler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		token := r.Header.Get("X-PALADIN-Token")
		if token == "" {
			// nil → SDK responds 400 Bad Request. Cheaper than failing
			// inside an RPC and burning quota for the agent host.
			return nil
		}
		clients := mcp.NewClients(
			httpc,
			cfg.MCP.Upstreams.AdminURL,
			cfg.MCP.Upstreams.DataURL,
			cfg.MCP.Upstreams.IAMURL,
			token,
		)
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

	l.Info("mcp http starting", zap.String("addr", addr), zap.Bool("allow_write", allow))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		l.Fatal("mcp http listen failed", zap.Error(err))
	}
}
