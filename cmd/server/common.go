package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
)

// boot is the prologue every subcommand runs: build the bootstrap logger,
// load the YAML config, build the production logger, open the DB pool,
// ping it. Returns a started, pinged *postgres.DB and the production
// logger; the caller is responsible for db.Close() in its own defer.
//
// Subcommands that do not need a DB (none today, but future read-only
// `paladin version` / `paladin config` flavours might) should not call boot —
// they can build the logger directly.
func boot(ctx context.Context) (config.Config, *zap.Logger, *postgres.DB) {
	bootstrap, err := logger.NewBootstrapLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build bootstrap logger: %v\n", err)
		os.Exit(1)
	}

	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}

	cfg, err := config.Load(configPath, bootstrap)
	if err != nil {
		bootstrap.Fatal("failed to load config", zap.Error(err))
	}

	l, err := logger.New(cfg.Logger)
	if err != nil {
		bootstrap.Fatal("failed to build logger", zap.Error(err))
	}
	logger.ReplaceGlobals(l)

	var pgOpts []postgres.Option
	if cfg.Security.EnableRLS {
		pgOpts = append(pgOpts, postgres.WithRLS())
		l.Info("postgres: RLS enabled (defence-in-depth tenant isolation)")
	}
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, l.Named("postgres"), pgOpts...)
	if err != nil {
		l.Fatal("failed to connect to database", zap.Error(err))
	}
	if err := db.Ping(ctx); err != nil {
		_ = db.Close
		l.Fatal("failed to ping database", zap.Error(err))
	}
	return cfg, l, db
}
