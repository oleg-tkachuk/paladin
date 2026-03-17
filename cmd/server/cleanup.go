package main

import (
	"context"
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/logger"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	retention string
	batchSize int
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Database cleanup utilities",
}

var auditLogsPruneCmd = &cobra.Command{
	Use:   "audit-logs",
	Short: "Prune old audit logs",
	Run: func(cmd *cobra.Command, args []string) {
		bootstrap := logger.NewBootstrapLogger()

		retentionDuration, err := time.ParseDuration(retention)
		if err != nil {
			// Handle simple "30d" format which time.ParseDuration doesn't support
			if len(retention) > 1 && retention[len(retention)-1] == 'd' {
				var days int
				if _, sErr := fmt.Sscanf(retention, "%dd", &days); sErr != nil {
					bootstrap.Fatal("Invalid retention format (failed to parse days)", zap.String("retention", retention), zap.Error(sErr))
				}
				retentionDuration = time.Duration(days) * 24 * time.Hour
			} else {
				bootstrap.Fatal("Invalid retention format", zap.String("retention", retention), zap.Error(err))
			}
		}

		cutoff := time.Now().Add(-retentionDuration)
		bootstrap.Info("Starting audit log pruning",
			zap.Time("cutoff", cutoff),
			zap.Int("batch_size", batchSize))

		cfg, err := config.Load(configPath, bootstrap)
		if err != nil {
			bootstrap.Fatal("Failed to load config", zap.Error(err))
		}

		// Use ReaperDSN if available, otherwise fallback to main DSN
		dsn := cfg.Datastores.Postgres.ReaperDSN
		if dsn == "" {
			dsn = cfg.Datastores.Postgres.DSN
			bootstrap.Warn("ReaperDSN not set, falling back to main DSN")
		}

		ctx := context.Background()
		db, err := postgres.New(ctx, config.Postgres{
			DSN: dsn,
			Pool: config.PostgresPool{
				MaxConns: 5,
			},
			Timeouts: config.PostgresTimeouts{
				Connect:   cfg.Datastores.Postgres.Timeouts.Connect,
				Statement: cfg.Datastores.Postgres.Timeouts.Statement,
			},
		}, bootstrap)
		if err != nil {
			bootstrap.Fatal("Failed to connect to database", zap.Error(err))
		}
		defer db.Close()

		repo := postgres.NewAuditLogRepo(db)
		totalDeleted := int64(0)

		for {
			deleted, err := repo.Prune(ctx, cutoff, batchSize)
			if err != nil {
				bootstrap.Fatal("Pruning iteration failed", zap.Error(err), zap.Int64("total_deleted", totalDeleted))
			}

			totalDeleted += deleted
			bootstrap.Info("Batch pruned", zap.Int64("deleted", deleted), zap.Int64("total_deleted", totalDeleted))

			if deleted < int64(batchSize) {
				break
			}

			// Small sleep to avoid slamming the DB
			time.Sleep(100 * time.Millisecond)
		}

		bootstrap.Info("Audit log pruning completed", zap.Int64("total_deleted", totalDeleted))
	},
}

func init() {
	auditLogsPruneCmd.Flags().StringVar(&retention, "retention", "30d", "Retention period (e.g. 24h, 7d, 30d)")
	auditLogsPruneCmd.Flags().IntVar(&batchSize, "batch-size", 1000, "Number of logs to delete per batch")

	cleanupCmd.AddCommand(auditLogsPruneCmd)
	rootCmd.AddCommand(cleanupCmd)
}
