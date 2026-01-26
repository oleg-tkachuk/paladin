package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"paladin/internal/app"
	"paladin/internal/logger"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const defaultConfigPath = "/app/configs/paladin.yaml"

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
		bootstrap := logger.NewBootstrapLogger()
		logger.ReplaceGlobals(bootstrap)

		abs, err := filepath.Abs(configPath)
		if err != nil {
			bootstrap.Warn("Failed to resolve absolute config path", zap.Error(err))
		} else {
			configPath = abs
		}

		if _, err := os.Stat(configPath); err != nil {
			bootstrap.Fatal("Configuration file missing", zap.String("path", configPath), zap.Error(err))
		}

		bootstrap.Info("Starting service",
			zap.String("version", version),
			zap.String("commit_id", commit),
			zap.String("build_time", buildTime),
			zap.String("config_path", configPath),
		)

		a, err := app.New(version, commit, buildTime, configPath)
		if err != nil {
			bootstrap.Fatal("Failed to initialize app", zap.Error(err))
		}

		go func() {
			if err := a.Run(); err != nil {
				a.Logger.Warn("Server exited", zap.Error(err))
			}
		}()

		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit

		bootstrap.Info("Shutdown signal received", zap.String("signal", sig.String()))

		a.Shutdown()
	},
}

func Execute() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", defaultConfigPath, "Path to config YAML file")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
