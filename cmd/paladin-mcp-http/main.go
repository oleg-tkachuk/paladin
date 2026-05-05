// paladin-mcp-http is the HTTP+SSE Model Context Protocol bridge to PALADIN.
//
// Used by remote LLM platforms that connect to MCP servers over the network
// instead of stdio. Listens on a configurable port; serves:
//
//	POST /mcp   — JSON-RPC request, JSON-RPC response (one frame per call)
//	GET  /sse   — Server-Sent Events stream of asynchronous notifications
//	              (currently empty; reserved for future tool-result streams)
//
// Authentication: requests must carry an `X-PALADIN-Token` header that the bridge
// forwards verbatim as the PALADIN Authorization bearer. The bridge itself does
// not verify the token — PALADIN does. For untrusted networks, run behind mTLS.
//
// Environment matches paladin-mcp-stdio:
//
//	PALADIN_ADMIN_URL, PALADIN_DATA_URL, PALADIN_IAM_URL
//	PALADIN_MCP_HTTP_ADDR (default :8095)
//	PALADIN_MCP_ALLOW_WRITE=1 — enable mutating tools
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	logger := zap.Must(zap.NewProduction())
	defer func() { _ = logger.Sync() }()

	addr := envOr("PALADIN_MCP_HTTP_ADDR", ":8095")
	adminURL := envOr("PALADIN_ADMIN_URL", "http://localhost:8090")
	dataURL := envOr("PALADIN_DATA_URL", "http://localhost:8080")
	iamURL := envOr("PALADIN_IAM_URL", "http://localhost:8085")
	allowWrite := os.Getenv("PALADIN_MCP_ALLOW_WRITE") == "1"

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", handleMCP(adminURL, dataURL, iamURL, allowWrite, logger))
	mux.HandleFunc("GET /sse", handleSSE())
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

	logger.Info("MCP HTTP server starting",
		zap.String("addr", addr),
		zap.String("version", version),
		zap.String("commit", commit),
		zap.Bool("allow_write", allowWrite),
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("ListenAndServe failed", zap.Error(err))
	}
}

// handleMCP routes one JSON-RPC frame through a per-request mcp.Server. We
// build a fresh Clients for each request so the caller's X-PALADIN-Token is the
// bearer used by the underlying Connect calls — no cross-request leakage.
func handleMCP(adminURL, dataURL, iamURL string, allowWrite bool, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-PALADIN-Token")
		if token == "" {
			http.Error(w, "X-PALADIN-Token header required", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		clients := mcp.NewClients(
			&http.Client{Timeout: 30 * time.Second},
			adminURL, dataURL, iamURL, token,
		)
		server := mcp.NewServer("paladin-mcp", version, logger)
		mcp.RegisterDefaults(server, clients, allowWrite)

		resp, herr := server.Handle(r.Context(), body)
		if herr != nil {
			http.Error(w, herr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if resp == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write(resp)
	}
}

// handleSSE serves a long-lived event stream. Currently the bridge has no
// asynchronous events to publish, so the stream stays open emitting `:keep`
// comments every 15s until the client disconnects. Reserved for future
// long-running tool result streaming.
func handleSSE() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := fmt.Fprintf(w, ": keep-alive %s\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// silence unused json import — kept to match stdio binary interface
var _ = json.Marshal
