package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
)

// boot is the prologue every subcommand runs: build the bootstrap logger,
// load the YAML config, build the production logger, initialise OpenTelemetry,
// open the DB pool, ping it. Returns a started, pinged *postgres.DB, the
// production logger, and the OTel shutdown hook; the caller is responsible
// for db.Close() AND otelShutdown(ctx) in its own defers.
//
// The OTel init (ADR-0001) is the single activation point for the whole
// process: it installs the global Tracer/Meter providers the otelconnect
// and otelpgx instrumentation already feed. When cfg.OTel.Enabled is false
// it returns a no-op shutdown, so non-observable environments pay nothing.
//
// Subcommands that do not need a DB (none today, but future read-only
// `paladin version` / `paladin config` flavours might) should not call boot —
// they can build the logger directly.
func boot(ctx context.Context) (config.Config, *zap.Logger, *postgres.DB, observability.ShutdownFunc) {
	bootstrap, err := logger.NewBootstrapLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build bootstrap logger: %v\n", err)
		os.Exit(1)
	}

	// Config path resolves to absolute so log lines + error messages
	// quote the canonical location regardless of cwd.
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}

	// Optional overlay chain via PALADIN_CONFIG_OVERLAYS env var (colon-
	// separated paths). Operators stack environment-specific deltas
	// over a shared base.yaml without touching the binary's CLI.
	// Files merge in order; later wins. Common pattern in K8s:
	//   PALADIN_CONFIG_PATH=/etc/paladin/base.yaml
	//   PALADIN_CONFIG_OVERLAYS=/etc/paladin/local.yaml:/etc/paladin/secrets.yaml
	paths := []string{configPath}
	if overlays := os.Getenv("PALADIN_CONFIG_OVERLAYS"); overlays != "" {
		for _, p := range strings.Split(overlays, ":") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
			paths = append(paths, p)
		}
	}

	cfg, err := config.Load(paths, bootstrap)
	if err != nil {
		bootstrap.Fatal("failed to load config", zap.Error(err))
	}

	l, err := logger.New(cfg.Logger)
	if err != nil {
		bootstrap.Fatal("failed to build logger", zap.Error(err))
	}
	logger.ReplaceGlobals(l)

	// Activate OpenTelemetry (ADR-0001). This is the single point that
	// installs the global Tracer/Meter providers; the otelconnect + otelpgx
	// instrumentation wired into the planes/pool feed these providers. A
	// failure here is non-fatal — observability must never block the
	// service from starting — so we log and continue with the no-op
	// providers. With cfg.OTel.Enabled=false, InitOTel itself returns a
	// no-op shutdown and never errors.
	otelShutdown, err := observability.InitOTel(ctx, cfg.OTel)
	if err != nil {
		l.Error("failed to initialise OpenTelemetry; continuing without it", zap.Error(err))
		otelShutdown = func(context.Context) error { return nil }
	}

	// RLS is non-optional. Migration 023 enables per-table policies
	// unconditionally and the runtime DSN connects as paladin_app
	// (NOBYPASSRLS), so the BeforeAcquire hook that stamps
	// paladin.tenant_id is the only place tenant context reaches the
	// session GUC. Without it, every INSERT fails 'new row violates
	// row-level security policy'. The hook is microseconds per
	// connection acquire — there is no operator-meaningful reason
	// to ever disable it. Worker / migrate paths that legitimately
	// span tenants run as paladin_migrate (BYPASSRLS), so the hook is
	// a no-op for them at the policy level.
	db, err := postgres.New(ctx, cfg.Datastores.Postgres, l.Named("postgres"), postgres.WithRLS())
	if err != nil {
		l.Fatal("failed to connect to database", zap.Error(err))
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		l.Fatal("failed to ping database", zap.Error(err))
	}
	return cfg, l, db, otelShutdown
}

// flushOTel runs the OTel shutdown hook with a bounded, fresh context so a
// cancelled parent context (SIGTERM already fired) can't abort the final
// span/metric batch flush. Callers `defer flushOTel(otelShutdown)`.
func flushOTel(shutdown observability.ShutdownFunc) {
	if shutdown == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}
