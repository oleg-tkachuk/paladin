package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// serveCmd is the parent for every long-running PALADIN role. Children:
// `api`, `admin`, `worker`, `mcp`, `ingest`, `dispatcher`. The collapsed
// "all-in-one" mode that used to live in rootCmd is intentionally absent —
// Helm deploys one Deployment per child.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run an PALADIN plane or worker",
}

func init() {
	serveCmd.AddCommand(serveAPICmd, serveAdminCmd, serveWorkerCmd, serveMCPCmd, serveIngestCmd, serveDispatcherCmd)
}

// signalCtx wires SIGINT/SIGTERM into a parent context. Used by the roles that
// still run on the pre-fx boot() path (worker/mcp/ingest/dispatcher); the fx
// roles (api/admin) get signal handling from fx.App.Run() instead. Returned
// cancel must be deferred.
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
