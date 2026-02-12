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

func TestMultipartRepo_Create(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewMultipartRepo(db)
	ctx := context.Background()

	id := uuid.New()
	rec := domain.Multipart{
		ID:          id,
		TenantID:    "test-tenant",
		ObjectID:    uuid.New(),
		UploadID:    "upload-123",
		Bucket:      "test-bucket",
		ObjectKey:   "test-key",
		ContentType: "application/json",
		PartSize:    5 * 1024 * 1024,
		Status:      domain.MultipartInitiated,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
	}

	mock.ExpectExec("INSERT INTO multipart_uploads").
		WithArgs(rec.ID, rec.TenantID, rec.ObjectID, rec.UploadID, rec.Bucket, rec.ObjectKey, rec.ContentType, rec.PartSize, rec.Status, rec.ExpiresAt).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err = repo.Create(ctx, rec)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMultipartRepo_CompleteUpload(t *testing.T) {
	mock, err := pgxmock.NewPool()
	assert.NoError(t, err)
	defer mock.Close()

	db := &DB{Pool: mock, log: zap.NewNop()}
	repo := NewMultipartRepo(db)
	ctx := context.Background()

	tenantID := "test-tenant"
	uploadID := "upload-123"
	objectID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectExec(`SELECT set_config\('app.tenant_id', 'test-tenant', true\)`).WillReturnResult(pgxmock.NewResult("SELECT", 1))
	mock.ExpectExec("UPDATE multipart_uploads").WithArgs(tenantID, uploadID).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE objects").WithArgs(objectID, tenantID).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	err = repo.CompleteUpload(ctx, tenantID, uploadID, objectID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
