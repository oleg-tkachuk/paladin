package postgres

import (
	"context"
	"fmt"
	"strconv"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"go.uber.org/zap"
)

type PgxPool interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	Ping(ctx context.Context) error
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Close()
	Stat() *pgxpool.Stat
	Config() *pgxpool.Config
}

type DB struct {
	Pool    PgxPool
	Queries *sqlc.Queries
	// Replica is the read-replica pool, nil when none is configured.
	Replica PgxPool
	// Reads routes lag-tolerant reads to Replica while it keeps up, and to
	// the primary otherwise. Never nil on a DB built by New. See ReadRouter
	// for which reads may use it.
	Reads *ReadRouter
	log   *zap.Logger
}

// Option mutates the pool config after parse and before the pool is
// constructed. Used today only to install the RLS PrepareConn /
// AfterRelease hooks; future hooks (telemetry, soft-delete defaults)
// can plug in the same way.
type Option func(*pgxpool.Config) *pgxpool.Config

// WithRLS turns on tenant-isolating PrepareConn / AfterRelease hooks
// that set / wipe `paladin.tenant_id` per acquisition. `002_roles_and_rls.sql`
// installs the matching per-table policies. The runtime DSN must
// connect as `paladin_app` (NOBYPASSRLS); workers / migrations as
// `paladin_migrate` (BYPASSRLS).
func WithRLS() Option {
	return func(cfg *pgxpool.Config) *pgxpool.Config {
		return EnableRLS(cfg)
	}
}

func New(ctx context.Context, cfg config.Postgres, log *zap.Logger, opts ...Option) (*DB, error) {
	pool, err := newPool(ctx, cfg.DSN, cfg.Password, cfg, log, "pool initialized", opts...)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(pool)
	return &DB{Pool: pool, Queries: queries, Reads: NewPrimaryOnlyRouter(pool), log: log}, nil
}

// AttachReplica opens the read-replica pool described by cfg.Replica and
// points d.Reads at it. A no-op unless replica.enabled is set — the replica
// is opt-in, and with it off every read stays on the primary. opts should be the
// ones the primary was opened with: the replica serves the same tenant-scoped
// reads, so it needs the same RLS hooks — the policies replicate with the
// schema, the GUC does not.
//
// The replica is not pinged and not trusted yet: reads stay on the primary
// until ReadRouter.Run has measured the lag, so a replica that is down at boot
// slows nothing and fails nothing, and one still catching up (a standby CNPG
// has just added) takes reads by itself once it is within max_lag. Long-lived roles call this; the one-shot
// commands (migrate, bootstrap) never read through the router and do not.
//
// The pool keeps no idle connections (min_conns 0): every long-lived role gets
// one, and only the ones that list objects use more than the probe's.
func (d *DB) AttachReplica(ctx context.Context, cfg config.Postgres, opts ...Option) error {
	if !cfg.Replica.Enabled {
		return nil
	}
	dsn, err := cfg.ReplicaDSN()
	if err != nil {
		return fmt.Errorf("replica: %w", err)
	}
	if d.Replica != nil {
		return fmt.Errorf("replica already attached")
	}
	// The replica is a physical standby of the same cluster, so the runtime
	// role and its password are the same; a separate password is needed only
	// when the replica is reached through a different role.
	password := cfg.Replica.Password
	if password == "" {
		password = cfg.Password
	}
	rcfg := cfg
	rcfg.Pool.MinConns = 0
	replica, err := newPool(ctx, dsn, password, rcfg, d.log, "replica pool initialized", opts...)
	if err != nil {
		return fmt.Errorf("replica: %w", err)
	}
	d.Replica = replica
	d.Reads = newReplicaRouter(d.Pool, replica,
		cfg.Replica.MaxLag, cfg.Replica.LagCheckPeriod, d.log.Named("replica"))
	return nil
}

// newPool builds one pgx pool for dsn with the shared pool, timeout and
// runtime settings from cfg. pgxpool connects lazily, so an unreachable host
// is not an error here; Ping is.
func newPool(
	ctx context.Context, dsn, password string, cfg config.Postgres, log *zap.Logger, msg string, opts ...Option,
) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool config parse: %w", err)
	}
	// pgx query tracer → OTel spans (one span per query, with the SQL as
	// the span name). Uses the global TracerProvider, which is a no-op
	// when OTel is disabled (InitOTel not called), so this is a cheap
	// unconditional install — the DB layer doesn't need to know whether
	// tracing is on.
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	if password != "" {
		poolCfg.ConnConfig.Password = password
	}

	// Apply config from struct
	poolCfg.MaxConns = cfg.Pool.MaxConns
	poolCfg.MinConns = cfg.Pool.MinConns
	poolCfg.MaxConnLifetime = cfg.Pool.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.Pool.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthcheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = cfg.Timeouts.Connect

	// Server-side runtime parameters applied on every new connection. These
	// bound worst-case behaviour at the Postgres layer — independent of any
	// app-level deadline that might be skipped on a leaked goroutine.
	//
	//   * statement_timeout: kills any query that runs longer than the
	//     configured ceiling. 0 disables (don't ship to prod that way).
	//   * idle_in_transaction_session_timeout: closes connections that
	//     hold a transaction open without doing work — the classic source
	//     of unkillable AccessExclusiveLock waits.
	//   * lock_timeout: bounds how long any single statement waits for a
	//     lock before failing fast. Combined with the above, prevents
	//     a slow ALTER from queueing every reader.
	if poolCfg.ConnConfig.RuntimeParams == nil {
		poolCfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	if cfg.Timeouts.Statement > 0 {
		poolCfg.ConnConfig.RuntimeParams["statement_timeout"] =
			strconv.FormatInt(cfg.Timeouts.Statement.Milliseconds(), 10)
	}
	// Idle-in-transaction defaults to the statement timeout when set, else
	// 60s — whichever is sooner. A live transaction with no work for a
	// minute is almost always a bug.
	idleInTx := int64(60_000)
	if cfg.Timeouts.Statement > 0 && cfg.Timeouts.Statement.Milliseconds() < idleInTx {
		idleInTx = cfg.Timeouts.Statement.Milliseconds()
	}
	poolCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] =
		strconv.FormatInt(idleInTx, 10)
	// Lock acquisition cap — 5s by default. Enough for normal contention,
	// short enough that a stuck DDL doesn't pile up readers.
	if _, set := poolCfg.ConnConfig.RuntimeParams["lock_timeout"]; !set {
		poolCfg.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	}
	// Application name surfaces in pg_stat_activity so DBAs can tell
	// Paladin traffic from migrations / ad-hoc queries.
	if _, set := poolCfg.ConnConfig.RuntimeParams["application_name"]; !set {
		poolCfg.ConnConfig.RuntimeParams["application_name"] = "paladin"
	}

	for _, opt := range opts {
		poolCfg = opt(poolCfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool init: %w", err)
	}

	log.Info(msg,
		zap.String("endpoint", fmt.Sprintf("postgres://%s:****@%s:%d/%s",
			poolCfg.ConnConfig.User, poolCfg.ConnConfig.Host, poolCfg.ConnConfig.Port, poolCfg.ConnConfig.Database)),
		zap.Int32("max_conns", poolCfg.MaxConns),
		zap.Int32("min_conns", poolCfg.MinConns),
	)

	return pool, nil
}

func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("database pool not initialized")
	}

	return d.Pool.Ping(ctx)
}

func (d *DB) Close() {
	if d == nil {
		return
	}
	if d.Replica != nil {
		d.Replica.Close()
	}
	if d.Pool != nil {
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
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			d.log.Error("failed to rollback transaction", zap.Error(err))
		}
	}()

	qtx := d.Queries.WithTx(tx)
	if err := fn(qtx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
