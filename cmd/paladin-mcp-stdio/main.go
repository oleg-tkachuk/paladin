// paladin-mcp-stdio is the stdio-transport Model Context Protocol bridge to PALADIN.
//
// Designed for Claude Desktop / Cursor / IDE plugins. The binary reads JSON-RPC
// frames from stdin and writes responses to stdout via the official MCP Go
// SDK (github.com/modelcontextprotocol/go-sdk). All logging goes to stderr to
// avoid polluting the protocol stream.
//
// Configuration:
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

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/paladin/internal/mcp"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
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
	fmt.Fprintf(os.Stderr, "paladin-mcp-stdio %s (commit %s) starting; allow_write=%v\n", version, commit, allowWrite)

	server := mcp.NewServer("paladin-mcp", version, clients, allowWrite)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "paladin-mcp-stdio: Run failed: %v\n", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
