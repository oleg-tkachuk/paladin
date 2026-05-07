// Package main implements the PALADIN server binary.
//
// Single-binary, multiple-mode pattern (Phase 2 of the agentic-plane
// migration). Subcommands:
//
//	paladin migrate           Apply SQL migrations from migrations/ FS.
//	paladin bootstrap         Provision the platform admin user + mirror
//	                      storage backends into the DB. Idempotent.
//	paladin serve api         Run data + iam Connect listeners. No workers.
//	paladin serve admin       Run the admin Connect listener.
//	paladin serve worker      Run all background jobs, lease-coordinated.
//	paladin serve mcp         Run the MCP server (stdio or streamable-HTTP).
//
// Each subcommand exits non-zero on a fatal error. The legacy collapsed
// mode that ran every plane + every worker in one process is gone — the
// Helm chart (Phase 4) deploys one Deployment per `serve` subcommand.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultConfigPath    = "/app/configs/config.yaml"
	defaultShutdownGrace = 15 * time.Second
)

// Persistent flag, set on rootCmd, available to every subcommand.
var configPath string

// Link-time identity. The build pipeline injects these via -ldflags;
// `go run` keeps the defaults so SystemService can return a stable
// "dev" / "none" / "unknown" tuple.
var (
	version   = "dev"
	commit    = "none"
	buildTime = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "paladin",
	Short: "Paladin",
	Long:  "Single binary, multiple modes. Use `paladin serve <role>` to run a plane or worker; `paladin migrate` / `paladin bootstrap` for one-shot lifecycle.",
}

// Execute is the entry point invoked from main.go. Returns non-zero exit
// status on any error.
func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Path to config YAML file")
	rootCmd.AddCommand(migrateCmd, bootstrapCmd, serveCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
