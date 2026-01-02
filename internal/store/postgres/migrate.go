package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"
)

func (d *DB) RunMigrations(ctx context.Context, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	files := make([]string, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		if strings.HasSuffix(name, ".sql") {
			files = append(files, filepath.Join(migrationsDir, name))
		}
	}

	sort.Strings(files)

	for _, f := range files {
		sqlBytes, err := os.ReadFile(f)
		if err != nil {
			d.log.Error("Migration file read error", zap.String("file", f), zap.Error(err))
			return fmt.Errorf("read migration %s: %w", f, err)
		}

		if _, err := d.Pool.Exec(ctx, string(sqlBytes)); err != nil {
			d.log.Error("Migration apply error", zap.String("file", f), zap.Error(err))
			return fmt.Errorf("apply migration %s: %w", f, err)
		}
		d.log.Info("Migration applied", zap.String("file", f))
	}

	d.log.Info("All migrations completed", zap.Int("count", len(files)))
	return nil
}
