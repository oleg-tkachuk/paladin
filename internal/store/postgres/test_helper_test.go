package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer represents the postgres container type used in the tests
type PostgresContainer struct {
	*postgres.PostgresContainer
	ConnectionString string
}

// SetupPostgresContainer creates a new postgres container for testing
func SetupPostgresContainer(ctx context.Context) (*PostgresContainer, error) {
	dbName := "testdb"
	dbUser := "user"
	dbPassword := "password"

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("failed to get connection string: %w", err)
	}

	return &PostgresContainer{
		PostgresContainer: container,
		ConnectionString:  connStr,
	}, nil
}

// GetTestPool returns a pgxpool for the container
func (c *PostgresContainer) GetTestPool(ctx context.Context) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, c.ConnectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping pool: %w", err)
	}

	return pool, nil
}
