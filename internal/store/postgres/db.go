package postgres

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"paladin/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type DB struct {
	Pool *pgxpool.Pool
	log  *zap.Logger
}

func New(ctx context.Context, cfg config.Postgres, log *zap.Logger) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgxpool config parse: %w", err)
	}

	// Apply config from struct
	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime

	// Allow overrides via env
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			poolCfg.MaxConns = int32(i)
		}
	}
	if v := os.Getenv("DB_MIN_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			poolCfg.MinConns = int32(i)
		}
	}
	if v := os.Getenv("DB_MAX_CONN_LIFETIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			poolCfg.MaxConnLifetime = d
		}
	}
	if v := os.Getenv("DB_MAX_CONN_IDLE_TIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			poolCfg.MaxConnIdleTime = d
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("PostgreSQL pool init failed", zap.Error(err))
		return nil, fmt.Errorf("pgxpool init: %w", err)
	}

	log.Info("PostgreSQL pool initialized",
		zap.Int32("max_conns", poolCfg.MaxConns),
		zap.Int32("min_conns", poolCfg.MinConns),
	)

	return &DB{Pool: pool, log: log}, nil
}

func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("database pool not initialized")
	}

	return d.Pool.Ping(ctx)
}

func (d *DB) Close() {
	if d != nil && d.Pool != nil {
		d.Pool.Close()
	}
}
