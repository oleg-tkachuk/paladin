package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

// openMigrationDB returns the *sql.DB goose runs against. When
// migrateCfg.MigrateDSN is non-empty, a fresh ConnConfig is parsed from
// that DSN — production deploys point it at a DDL-capable role distinct
// from the runtime `dsn` (`paladin_migrate` vs `paladin_app`). When MigrateDSN
// is empty, the runtime pool's ConnConfig is reused (dev path; the
// runtime user must have DDL rights in that mode).
//
// Either way the connection is tagged with `application_name=paladin-migrate`
// so DBA dashboards can tell the migration session apart from runtime
// traffic in pg_stat_activity.
func openMigrationDB(runtimeCfg *pgx.ConnConfig, migrateCfg *config.Postgres) (*sql.DB, error) {
	cc := runtimeCfg
	if migrateCfg != nil && migrateCfg.MigrateDSN != "" {
		// IMPORTANT: parse `MigrateDSN`, NOT `DSN`. Reading `DSN` here
		// would point goose at the runtime user (paladin_app) — which has
		// no DDL grants — and migrations would fail with permission
		// denied even though the field naming and pg_stat_activity
		// tag make it look like the migrate path is active. Past
		// regression: see commit fixing
		// "goose up: ERROR: permission denied for schema public".
		parsed, err := pgx.ParseConfig(migrateCfg.MigrateDSN)
		if err != nil {
			return nil, fmt.Errorf("parse migrate_dsn: %w", err)
		}
		if migrateCfg.MigratePassword != "" {
			parsed.Password = migrateCfg.MigratePassword
		}
		// Dropping runtime params from the migration session is intentional:
		// statement_timeout / idle_in_transaction_session_timeout / lock_timeout
		// are sized for hot-path queries and would kill long DDL statements
		// (e.g. CREATE INDEX CONCURRENTLY on a populated table). DBAs who
		// want migration timeouts set them explicitly via the role's DEFAULTs
		// or per-migration `SET LOCAL`.
		parsed.RuntimeParams = map[string]string{}
		cc = parsed
	}
	// Tag the session for pg_stat_activity. Independent of which DSN was
	// chosen — even the dev path benefits from the distinct label.
	if cc.RuntimeParams == nil {
		cc.RuntimeParams = map[string]string{}
	}
	cc.RuntimeParams["application_name"] = "paladin-migrate"
	return stdlib.OpenDB(*cc), nil
}

// gooseLogger adapts goose's Printf to zap, parsing for specific messages
type gooseLogger struct {
	log   *zap.Logger
	count int
}

func (l *gooseLogger) Fatalf(format string, v ...interface{}) {
	l.log.Fatal(fmt.Sprintf(format, v...))
}

func (l *gooseLogger) Printf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	// goose logs "OK   path/to/file.sql" when a migration is applied
	if strings.HasPrefix(msg, "OK   ") {
		l.count++
	}
	// Log the original message with the service field
	l.log.Info(strings.TrimSpace(msg),
		zap.String("service", "paladin"),
	)
}

// RunMigrations applies pending goose migrations using the runtime pool's
// connection config. Use this only in dev/test where the runtime role has
// DDL rights. Production deploys should call RunMigrationsWith and pass a
// distinct migrate-role config so the runtime role can stay DML-only.
func (d *DB) RunMigrations(ctx context.Context, fs embed.FS) error {
	return d.runMigrationsTo(ctx, fs, 0, nil)
}

// RunMigrationsWith applies migrations using the supplied Postgres config —
// typically a DDL-capable role distinct from the runtime user. The pool
// stays untouched; only goose's transient *sql.DB is rebuilt.
//
// Empty migrateCfg.MigrateDSN falls back to RunMigrations (runtime-pool
// path) so callers can pass `cfg.Datastores.Postgres` unconditionally.
func (d *DB) RunMigrationsWith(ctx context.Context, fs embed.FS, migrateCfg config.Postgres) error {
	if migrateCfg.MigrateDSN == "" {
		return d.RunMigrations(ctx, fs)
	}
	return d.runMigrationsTo(ctx, fs, 0, &migrateCfg)
}

// RunMigrationsTo runs goose up only as far as the supplied version. A
// version of 0 means "all the way". Used to interleave application-level
// data work (seeds, backfills) between schema phases — e.g. populate
// object_keys.bucket_name before migration 005 enforces NOT NULL on it.
func (d *DB) RunMigrationsTo(ctx context.Context, fs embed.FS, target int64) error {
	return d.runMigrationsTo(ctx, fs, target, nil)
}

func (d *DB) runMigrationsTo(_ context.Context, fs embed.FS, target int64, migrateCfg *config.Postgres) error {
	db, err := openMigrationDB(d.Pool.Config().ConnConfig, migrateCfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			d.log.Error("failed to close db", zap.Error(err))
		}
	}()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	gl := &gooseLogger{log: d.log}
	goose.SetLogger(gl)

	goose.SetBaseFS(fs)

	if target > 0 {
		err = goose.UpTo(db, ".", target)
	} else {
		err = goose.Up(db, ".")
	}
	if err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	d.log.Info("migrations applied",
		zap.String("service", "paladin"),
		zap.Int("count", gl.count),
		zap.Int64("target", target),
	)

	return nil
}
