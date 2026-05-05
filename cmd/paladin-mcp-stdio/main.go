// paladin-mcp-stdio is the stdio-transport Model Context Protocol bridge to PALADIN.
//
// Designed for Claude Desktop / Cursor / IDE plugins. The binary reads
// newline-delimited JSON-RPC frames from stdin and writes responses to
// stdout. All logging goes to stderr to avoid polluting the protocol stream.
//
// Configuration is environment-driven so the LLM client config can pin it
// without a YAML round-trip:
//
//	PALADIN_ADMIN_URL    — admin plane base (default http://localhost:8090)
//	PALADIN_DATA_URL     — data plane base  (default http://localhost:8080)
//	PALADIN_IAM_URL      — iam plane base   (default http://localhost:8085)
//	PALADIN_MCP_TOKEN    — bearer token (admin-aud or service-account)
//	PALADIN_MCP_ALLOW_WRITE=1 — opt in to mutating tools (off by default)
package main

import (
	"context"
	"fmt"
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
	logger := zap.Must(zap.NewDevelopment(zap.IncreaseLevel(zap.InfoLevel)))
	defer func() { _ = logger.Sync() }()

	clients := mcp.NewClients(
		&http.Client{Timeout: 30 * time.Second},
		envOr("PALADIN_ADMIN_URL", "http://localhost:8090"),
		envOr("PALADIN_DATA_URL", "http://localhost:8080"),
		envOr("PALADIN_IAM_URL", "http://localhost:8085"),
		os.Getenv("PALADIN_MCP_TOKEN"),
	)

	allowWrite := os.Getenv("PALADIN_MCP_ALLOW_WRITE") == "1"
	if !allowWrite {
		fmt.Fprintln(os.Stderr, "paladin-mcp-stdio: read-only mode (set PALADIN_MCP_ALLOW_WRITE=1 to enable mutating tools)")
	}

	server := mcp.NewServer("paladin-mcp", version, logger)
	mcp.RegisterDefaults(server, clients, allowWrite)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("MCP stdio server starting",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.Bool("allow_write", allowWrite),
	)

	if err := server.ServeStdio(ctx, os.Stdin, os.Stdout); err != nil {
		logger.Fatal("ServeStdio failed", zap.Error(err))
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
