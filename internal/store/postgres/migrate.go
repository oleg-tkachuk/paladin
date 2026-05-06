package postgres

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"go.uber.org/zap"
)

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

func (d *DB) RunMigrations(ctx context.Context, fs embed.FS) error {
	return d.runMigrationsTo(ctx, fs, 0)
}

// RunMigrationsTo runs goose up only as far as the supplied version. A
// version of 0 means "all the way". Used to interleave application-level
// data work (seeds, backfills) between schema phases — e.g. populate
// object_keys.bucket_name before migration 005 enforces NOT NULL on it.
func (d *DB) RunMigrationsTo(ctx context.Context, fs embed.FS, target int64) error {
	return d.runMigrationsTo(ctx, fs, target)
}

func (d *DB) runMigrationsTo(_ context.Context, fs embed.FS, target int64) error {
	// Create a new *sql.DB just for migrations using the pool's config
	db := stdlib.OpenDB(*d.Pool.Config().ConnConfig)
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

	var err error
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
