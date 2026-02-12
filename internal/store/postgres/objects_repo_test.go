package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestObjectsRepo_Create(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewObjectsRepo(db)
	ctx := context.Background()

	id := uuid.New()
	rec := domain.Object{
		ID:          id,
		TenantID:    "test-tenant",
		ObjectKey:   "test-key",
		Bucket:      "test-bucket",
		ContentType: "application/json",
		SizeBytes:   100,
		Status:      domain.ObjectPending,
	}

	mock.ExpectExec("INSERT INTO objects").
		WithArgs(rec.ID, rec.TenantID, rec.ObjectKey, rec.Bucket, rec.ContentType, rec.SizeBytes, rec.ChecksumSHA256, rec.Status, rec.ExpiresAt, rec.Labels, rec.ExternalRef).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(ctx, rec)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestObjectsRepo_Get(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewObjectsRepo(db)
	ctx := context.Background()

	id := uuid.New()
	tenantID := "test-tenant"
	now := time.Now()

	columns := []string{"id", "tenant_id", "object_key", "bucket", "content_type", "size_bytes", "checksum_sha256", "status", "created_at", "updated_at", "expires_at", "labels", "external_ref", "stored_etag", "stored_size_bytes", "completed_at", "deleted_at"}
	mock.ExpectQuery(`SELECT (.+) FROM objects WHERE tenant_id=\$1 AND id=\$2`).
		WithArgs(tenantID, id).
		WillReturnRows(pgxmock.NewRows(columns).
			AddRow(id, tenantID, "test-key", "test-bucket", "application/json", int64(100), nil, domain.ObjectPending, now, now, nil, nil, nil, nil, nil, nil, nil))

	rec, err := repo.Get(ctx, tenantID, id)
	assert.NoError(t, err)
	assert.NotNil(t, rec)
	assert.Equal(t, id, rec.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
