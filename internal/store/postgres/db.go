package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"go.uber.org/zap"
)

type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	Ping(ctx context.Context) error
	Close()
	Stat() *pgxpool.Stat
	Config() *pgxpool.Config
}

type DB struct {
	Pool    PgxPool
	Queries *sqlc.Queries
	log     *zap.Logger
}

func New(ctx context.Context, cfg config.Postgres, log *zap.Logger) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgxpool config parse: %w", err)
	}

	// Apply config from struct
	poolCfg.MaxConns = cfg.Pool.MaxConns
	poolCfg.MinConns = cfg.Pool.MinConns
	poolCfg.MaxConnLifetime = cfg.Pool.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.Pool.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthcheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = cfg.Timeouts.Connect

	poolCfg.ConnConfig.ConnectTimeout = cfg.Timeouts.Connect

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("PostgreSQL pool init failed", zap.Error(err))
		return nil, fmt.Errorf("pgxpool init: %w", err)
	}

	log.Info("PostgreSQL pool initialized",
		zap.String("endpoint", fmt.Sprintf("postgres://%s:****@%s:%d/%s",
			poolCfg.ConnConfig.User, poolCfg.ConnConfig.Host, poolCfg.ConnConfig.Port, poolCfg.ConnConfig.Database)),
		zap.Int32("max_conns", poolCfg.MaxConns),
		zap.Int32("min_conns", poolCfg.MinConns),
	)

	queries := sqlc.New(pool)

	return &DB{Pool: pool, Queries: queries, log: log}, nil
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

// Stats returns connection pool statistics
func (d *DB) Stats() *pgxpool.Stat {
	if d == nil || d.Pool == nil {
		return nil
	}
	return d.Pool.Stat()
}

// HealthWithStats performs a health check and returns detailed pool statistics
func (d *DB) HealthWithStats(ctx context.Context) (map[string]interface{}, error) {
	if err := d.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping failed: %w", err)
	}

	stats := d.Pool.Stat()
	return map[string]interface{}{
		"healthy":                true,
		"acquired_conns":         stats.AcquiredConns(),
		"idle_conns":             stats.IdleConns(),
		"max_conns":              stats.MaxConns(),
		"total_conns":            stats.TotalConns(),
		"acquire_count":          stats.AcquireCount(),
		"acquire_duration_ms":    stats.AcquireDuration().Milliseconds(),
		"empty_acquire_count":    stats.EmptyAcquireCount(),
		"canceled_acquire_count": stats.CanceledAcquireCount(),
	}, nil
}

// WithTx executes a function within a transaction
func (d *DB) WithTx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("database pool not initialized")
	}

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := d.Queries.WithTx(tx)
	if err := fn(qtx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
