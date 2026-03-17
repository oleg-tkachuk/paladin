//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestObjectsRepo_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Setup container
	container, err := SetupPostgresContainer(ctx)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	// 2. Setup DB and Repo
	pool, err := container.GetTestPool(ctx)
	require.NoError(t, err)
	defer pool.Close()

	logger := zap.NewNop()
	db := &DB{
		Pool:    pool,
		Queries: sqlc.New(pool),
		log:     logger,
	}

	err = db.RunMigrations(ctx, migrations.FS)
	require.NoError(t, err)

	repo := NewObjectsRepo(db)

	// 4. Perform operations
	id := uuid.New()
	tenantID := "test-tenant"
	rec := domain.Object{
		ID:          id,
		TenantID:    tenantID,
		ObjectKey:   "test/key.json",
		Bucket:      "test-bucket",
		ContentType: "application/json",
		SizeBytes:   1024,
		Status:      domain.ObjectPending,
	}

	// Create
	err = repo.Create(ctx, rec)
	assert.NoError(t, err)

	// Get
	got, err := repo.Get(ctx, tenantID, id)
	assert.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, rec.ID, got.ID)
	assert.Equal(t, rec.ObjectKey, got.ObjectKey)

	// Update Status
	updated, err := repo.MarkComplete(ctx, tenantID, id, "etag-123", 1024)
	assert.NoError(t, err)
	assert.True(t, updated)

	// Verify update
	got, err = repo.Get(ctx, tenantID, id)
	assert.NoError(t, err)
	assert.Equal(t, domain.ObjectComplete, got.Status)
}
