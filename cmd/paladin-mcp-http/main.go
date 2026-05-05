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
// Environment matches paladin-mcp-stdio:
//
//	PALADIN_ADMIN_URL, PALADIN_DATA_URL, PALADIN_IAM_URL
//	PALADIN_MCP_HTTP_ADDR (default :8095)
//	PALADIN_MCP_ALLOW_WRITE=1 — enable mutating tools
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

	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	addr := envOr("PALADIN_MCP_HTTP_ADDR", ":8095")
	adminURL := envOr("PALADIN_ADMIN_URL", "http://localhost:8090")
	dataURL := envOr("PALADIN_DATA_URL", "http://localhost:8080")
	iamURL := envOr("PALADIN_IAM_URL", "http://localhost:8085")
	allowWrite := os.Getenv("PALADIN_MCP_ALLOW_WRITE") == "1"

	httpc := &http.Client{Timeout: 30 * time.Second}

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(r *http.Request) *mcpsdk.Server {
		token := r.Header.Get("X-PALADIN-Token")
		if token == "" {
			// Returning nil makes the SDK respond with 400 Bad Request — the
			// LLM client can surface that as "no token" without a server
			// roundtrip burning quota.
			return nil
		}
		clients := mcp.NewClients(httpc, adminURL, dataURL, iamURL, token)
		return mcp.NewServer("paladin-mcp", version, clients, allowWrite)
	}, &mcpsdk.StreamableHTTPOptions{
		// SessionTimeout closes idle sessions after 10 min so we don't pin
		// memory for browsers that vanish mid-conversation.
		SessionTimeout: 10 * time.Minute,
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              addr,
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
		version, commit, addr, allowWrite)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "paladin-mcp-http: ListenAndServe failed: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
