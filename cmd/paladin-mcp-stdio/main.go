// paladin-mcp-stdio is the stdio-transport Model Context Protocol bridge to PALADIN.
//
// Designed for Claude Desktop / Cursor / IDE plugins. The binary reads JSON-RPC
// frames from stdin and writes responses to stdout via the official MCP Go
// SDK (github.com/modelcontextprotocol/go-sdk). All logging goes to stderr to
// avoid polluting the protocol stream.
//
// Configuration: pass `PALADIN_CONFIG=/path/to/config.yaml` to read the `mcp:`
// section, or rely on env vars alone.
//
//	PALADIN_CONFIG                — optional path to the PALADIN YAML config
//	PALADIN_ADMIN_URL             — admin plane base (default http://localhost:8090)
//	PALADIN_DATA_URL              — data plane base  (default http://localhost:8080)
//	PALADIN_IAM_URL               — iam plane base   (default http://localhost:8085)
//	PALADIN_MCP_TOKEN             — bearer token (admin-aud or service-account)
//	PALADIN_MCP_STDIO_ENABLED     — set false to refuse to start
//	PALADIN_MCP_STDIO_ALLOW_WRITE — opt in to mutating tools (off by default)
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
		fmt.Fprintf(os.Stderr, "paladin-mcp-stdio: config: %v\n", err)
		os.Exit(2)
	}
	if !cfg.Stdio.Enabled {
		fmt.Fprintln(os.Stderr, "paladin-mcp-stdio: disabled via config (mcp.stdio.enabled=false); exiting cleanly")
		return
	}

	clients := mcp.NewClients(
		&http.Client{Timeout: 30 * time.Second},
		cfg.Upstreams.AdminURL, cfg.Upstreams.DataURL, cfg.Upstreams.IAMURL,
		os.Getenv("PALADIN_MCP_TOKEN"),
	)

	if !cfg.Stdio.AllowWrite {
		fmt.Fprintln(os.Stderr, "paladin-mcp-stdio: read-only mode (set mcp.stdio.allow_write=true to enable mutating tools)")
	}
	fmt.Fprintf(os.Stderr, "paladin-mcp-stdio %s (commit %s) starting; allow_write=%v\n", version, commit, cfg.Stdio.AllowWrite)

	server := mcp.NewServer("paladin-mcp", version, clients, cfg.Stdio.AllowWrite)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		fmt.Fprintf(os.Stderr, "paladin-mcp-stdio: Run failed: %v\n", err)
		os.Exit(1)
	}
}
