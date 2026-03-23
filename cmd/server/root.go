package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/oleg-tkachuk/paladin/internal/logger"
	appwire "github.com/oleg-tkachuk/paladin/internal/wire"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const defaultConfigPath = "/app/configs/config.yaml"

var (
	configPath string
	version    = "dev"
	commit     = "none"
	buildTime  = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "paladin",
	Short: "Start the Paladin service",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		abs, err := filepath.Abs(configPath)
		if err != nil {
			configPath = abs
		} else {
			configPath = abs
		}

		a, cleanup, err := InitializeApp(
			ctx,
			appwire.Version(version),
			appwire.Commit(commit),
			appwire.BuildTime(buildTime),
			appwire.ConfigPath(configPath),
		)
		if err != nil {
			logger.NewBootstrapLogger().Fatal("Failed to initialize app", zap.Error(err))
		}
		defer cleanup()

		go func() {
			if err := a.Run(); err != nil {
				a.Logger.Warn("Server exited", zap.Error(err))
			}
		}()

		<-ctx.Done()
		a.Logger.Info("Shutdown signal received")
	},
}

func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Path to config YAML file")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
