package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	mocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/oleg-tkachuk/paladin/internal/worker"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
)

func TestReaper(t *testing.T) {
	cfg := config.Housekeeping{
		EnableReaper: true,
		GCInterval:   10 * time.Millisecond,
		PendingTTL:   10 * time.Minute,
		AuditLogTTL:  30 * time.Hour,
	}

	objRepo := mocks.NewMockObjectsRepository(t)
	mpRepo := mocks.NewMockMultipartRepository(t)
	intentRepo := mocks.NewMockUploadIntentsRepository(t)
	s3c := mocks.NewMockStorageClient(t)
	logger := zap.NewNop()

	objRepo.On("ListExpiredPending", mock.Anything, mock.Anything, 100).
		Return([]domain.Object{
			{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), TenantID: "t1", ObjectKey: "key1"},
		}, nil).Maybe()

	objRepo.On("Delete", mock.Anything, "t1", uuid.MustParse("00000000-0000-0000-0000-000000000001")).
		Return(true, nil).Maybe()

	mpRepo.On("ListExpired", mock.Anything, 100).
		Return([]domain.Multipart{
			{UploadID: "up1", TenantID: "t1", ObjectKey: "key2"},
		}, nil).Maybe()

	s3c.On("AbortMultipartUpload", mock.Anything, "key2", "up1").
		Return(nil).Maybe()

	mpRepo.On("MarkAborted", mock.Anything, "t1", "up1").
		Return(nil).Maybe()

	intentRepo.On("DeleteExpired", mock.Anything, mock.Anything, 100).
		Return(int64(0), nil).Maybe()

	r := worker.NewReaper(cfg, objRepo, mpRepo, intentRepo, s3c, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	r.Start(ctx)
}

func TestReaperDisabled(t *testing.T) {
	cfg := config.Housekeeping{EnableReaper: false}

	r := worker.NewReaper(cfg, nil, nil, nil, nil, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	r.Start(ctx) // Should return immediately
}
