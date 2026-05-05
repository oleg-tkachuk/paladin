// paladin-mcp-http is the streamable-HTTP Model Context Protocol bridge to PALADIN.
//
// Used by remote LLM platforms that connect to MCP servers over the network
// instead of stdio. Built on the official MCP Go SDK's StreamableHTTPHandler,
// which serves the MCP streamable-HTTP transport (POST/GET on a single path,
// session resumability, SSE event streams) per the MCP spec.
//
// The handler is mounted at "/mcp"; the per-request bearer token is taken
// from the X-PALADIN-Token header and injected into the PALADIN Connect calls. The
// SDK's getServer hook lets us mint a fresh server (and therefore a fresh
// per-request Clients bundle) per session, so two MCP clients hitting the
// bridge concurrently never share auth state.
//
// Configuration: pass `PALADIN_CONFIG=/path/to/config.yaml` to read the `mcp:`
// section, or rely on env vars alone.
//
//	PALADIN_CONFIG, PALADIN_ADMIN_URL, PALADIN_DATA_URL, PALADIN_IAM_URL
//	PALADIN_MCP_HTTP_ENABLED          — set false to refuse to start
//	PALADIN_MCP_HTTP_ADDR             — listen address (default :8095)
//	PALADIN_MCP_HTTP_ALLOW_WRITE      — enable mutating tools
//	PALADIN_MCP_HTTP_SESSION_TIMEOUT  — Go duration (default 10m)
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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	cfg, err := config.LoadMCP(os.Getenv("PALADIN_CONFIG"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "paladin-mcp-http: config: %v\n", err)
		os.Exit(2)
	}
	if !cfg.HTTP.Enabled {
		fmt.Fprintln(os.Stderr, "paladin-mcp-http: disabled via config (mcp.http.enabled=false); exiting cleanly")
		return
	}

	httpc := &http.Client{Timeout: 30 * time.Second}

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		token := r.Header.Get("X-PALADIN-Token")
		if token == "" {
			// Returning nil makes the SDK respond with 400 Bad Request — the
			// LLM client can surface that as "no token" without a server
			// roundtrip burning quota.
			return nil
		}
		clients := mcp.NewClients(httpc, cfg.Upstreams.AdminURL, cfg.Upstreams.DataURL, cfg.Upstreams.IAMURL, token)
		return mcp.NewServer("paladin-mcp", version, clients, cfg.HTTP.AllowWrite)
	}, &mcpsdk.StreamableHTTPOptions{
		SessionTimeout: cfg.HTTP.SessionTimeout,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "paladin-mcp-http %s (commit %s) listening on %s; allow_write=%v\n",
		version, commit, cfg.HTTP.Addr, cfg.HTTP.AllowWrite)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "paladin-mcp-http: ListenAndServe failed: %v\n", err)
		os.Exit(1)
	}
}
