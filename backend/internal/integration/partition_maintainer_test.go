//go:build integration

// Exercises worker.PartitionMaintainer against the real partitioned tables
// (migrations 041/042): create-ahead provisions upcoming partitions, and
// drop-old removes a partition whose whole range is past retention. The pure
// period/selection logic is unit-tested in the worker package; this proves
// the DDL the maintainer emits actually runs on a partitioned parent.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/oleg-tkachuk/paladin/migrations"
)

func TestPartitionMaintainer_CreateAndDrop(t *testing.T) {
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("paladin"),
		tcpostgres.WithUsername("paladin_migrate"),
		tcpostgres.WithPassword("paladin"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("postgres start: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `ALTER ROLE paladin_migrate BYPASSRLS`); err != nil {
		t.Fatalf("bypassrls: %v", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Manually create stale partitions the maintainer must drop: a daily
	// idempotency partition 10 days ago and a monthly audit partition 4
	// months ago. Both empty.
	old := time.Now().UTC().AddDate(0, 0, -10)
	oldIdem := "idempotency_keys_" + old.Format("20060102")
	if _, err := pool.Exec(ctx,
		`CREATE TABLE `+pgIdent(oldIdem)+` PARTITION OF idempotency_keys
		 FOR VALUES FROM ('`+old.Format("2006-01-02")+`') TO ('`+old.AddDate(0, 0, 1).Format("2006-01-02")+`')`); err != nil {
		t.Fatalf("create stale idem partition: %v", err)
	}
	oldMonth := time.Now().UTC().AddDate(0, -4, 0)
	oldMonthStart := time.Date(oldMonth.Year(), oldMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	oldAudit := "audit_log_" + oldMonthStart.Format("200601")
	if _, err := pool.Exec(ctx,
		`CREATE TABLE `+pgIdent(oldAudit)+` PARTITION OF audit_log
		 FOR VALUES FROM ('`+oldMonthStart.Format("2006-01-02")+`') TO ('`+oldMonthStart.AddDate(0, 1, 0).Format("2006-01-02")+`')`); err != nil {
		t.Fatalf("create stale audit partition: %v", err)
	}

	m := &worker.PartitionMaintainer{
		DB: pool,
		Specs: []worker.PartitionSpec{
			{Table: "audit_log", Period: worker.PeriodMonthly, Retention: 90 * 24 * time.Hour, Ahead: 3},
			{Table: "idempotency_keys", Period: worker.PeriodDaily, Retention: 0, Ahead: 8},
		},
	}
	m.RunOnce(ctx)

	// Stale partitions dropped.
	assertPartitionAbsent(ctx, t, pool, oldIdem)
	assertPartitionAbsent(ctx, t, pool, oldAudit)

	// Create-ahead provisioned the forward window: tomorrow's idempotency
	// day and next month's audit partition now exist.
	tomorrow := "idempotency_keys_" + time.Now().UTC().AddDate(0, 0, 1).Format("20060102")
	assertPartitionPresent(ctx, t, pool, tomorrow)
	nextMonth := time.Now().UTC().AddDate(0, 1, 0)
	nextMonthPart := "audit_log_" + time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC).Format("200601")
	assertPartitionPresent(ctx, t, pool, nextMonthPart)

	// Idempotent: a second sweep is a no-op (no error, partitions unchanged).
	m.RunOnce(ctx)
	assertPartitionPresent(ctx, t, pool, tomorrow)
}

func pgIdent(s string) string { return `"` + s + `"` }

func partitionExists(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_class WHERE relname=$1 AND relkind='r'`, name).Scan(&n); err != nil {
		t.Fatalf("exists(%s): %v", name, err)
	}
	return n == 1
}

func assertPartitionPresent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	if !partitionExists(ctx, t, pool, name) {
		t.Errorf("partition %q should exist", name)
	}
}

func assertPartitionAbsent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	if partitionExists(ctx, t, pool, name) {
		t.Errorf("partition %q should have been dropped", name)
	}
}
