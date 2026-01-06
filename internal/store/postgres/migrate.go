package postgres

import (
	"context"
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

func (d *DB) RunMigrations(ctx context.Context, migrationsDir string) error {
	// Create a new *sql.DB just for migrations using the pool's config
	db := stdlib.OpenDB(*d.Pool.Config().ConnConfig)
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	gl := &gooseLogger{log: d.log}
	goose.SetLogger(gl)

	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}

	d.log.Info("Migrations applied successfully",
		zap.String("service", "paladin"),
		zap.Int("count", gl.count),
	)
	return nil
}
