//go:build integration

// Applies every migration as the role production applies them as: the
// database owner, with BYPASSRLS and nothing more. Every other harness runs
// goose as the container's bootstrap superuser, so a migration that needs
// superuser passes all of them and fails only on the cluster, where it stops
// the migrate Job and with it every backend rollout. 061 did exactly that: a
// function SET clause on a custom parameter needs superuser to create.
package components

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/oleg-tkachuk/paladin/backend/internal/pgtest"
	"github.com/oleg-tkachuk/paladin/backend/migrations"
)

const (
	privSuperuser         = "postgres"
	privSuperuserPassword = "postgres"
	privDatabase          = "paladin"
	privMigrateRole       = "paladin_migrate"
	privMigratePassword   = "paladin_migrate"
	privReadyLog          = "database system is ready to accept connections"
	privReadyLogCount     = 2 // once for the init run, once for the real server
	privStartupTimeout    = 60 * time.Second
)

// The cluster's roles, database and extensions, as the CNPG Cluster declares
// them (spec.managed.roles, bootstrap.initdb with owner and postInitSQL).
// Created by the superuser, as CNPG creates them; nothing after this runs as
// superuser.
var privClusterSetup = []string{
	fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEROLE NOCREATEDB BYPASSRLS`,
		privMigrateRole, privMigratePassword),
	`CREATE ROLE paladin_app LOGIN NOSUPERUSER NOBYPASSRLS`,
	`CREATE ROLE paladin_reaper NOLOGIN NOSUPERUSER BYPASSRLS`,
	fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, privDatabase, privMigrateRole),
}

var privDatabaseSetup = []string{
	`CREATE EXTENSION IF NOT EXISTS citext`,
	`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
}

func TestMigrations_ApplyWithoutSuperuser(t *testing.T) {
	// Not parallel: sets goose's package-level dialect and filesystem.
	ctx := context.Background()

	pgC, err := tcpostgres.Run(ctx,
		pgtest.Image,
		tcpostgres.WithUsername(privSuperuser),
		tcpostgres.WithPassword(privSuperuserPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog(privReadyLog).
				WithOccurrence(privReadyLogCount).WithStartupTimeout(privStartupTimeout),
		),
	)
	if err != nil {
		t.Fatalf("postgres start: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	adminDSN, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	execAll(ctx, t, adminDSN, privClusterSetup)
	execAll(ctx, t, withDatabase(t, adminDSN, privDatabase), privDatabaseSetup)

	migrateDSN := withUser(t, withDatabase(t, adminDSN, privDatabase), privMigrateRole, privMigratePassword)
	db, err := sql.Open("pgx", migrateDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The test means nothing if the role it migrates as is a superuser.
	var super bool
	if err := db.QueryRowContext(ctx,
		`SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil {
		t.Fatalf("role check: %v", err)
	}
	if super {
		t.Fatalf("%s is a superuser; the test would prove nothing", privMigrateRole)
	}

	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrations as a non-superuser owner: %v", err)
	}
}

func execAll(ctx context.Context, t *testing.T, dsn string, stmts []string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func withDatabase(t *testing.T, dsn, database string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + database
	return u.String()
}

func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
