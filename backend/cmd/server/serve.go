package main

import (
	"github.com/spf13/cobra"
)

// serveCmd is the parent for every long-running PALADIN role. Children:
// `api`, `admin`, `worker`, `mcp`, `ingest`, `dispatcher`. The collapsed
// "all-in-one" mode that used to live in rootCmd is intentionally absent —
// Helm deploys one Deployment per child.
//
// Every child now runs on Uber fx: fx.App.Run() owns SIGINT/SIGTERM handling
// and drives the start/stop lifecycle, so there is no shared signalCtx or
// runListeners loop here anymore. The DB-backed bootstrap/migrate one-shot
// commands still use the boot() helper in common.go.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run an PALADIN plane or worker",
}

func init() {
	serveCmd.AddCommand(serveAPICmd, serveAdminCmd, serveWorkerCmd, serveMCPCmd, serveIngestCmd, serveDispatcherCmd)
}
