package paladinapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	domainmocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestObjectHandler_UploadObject(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockObjectsService{}
	handler := NewObjectHandler(log, svc)

	ctx := context.Background()
	req := connect.NewRequest(&UploadObjectRequest{
		TenantId:       "tenant-1",
		Bucket:         "bucket-1",
		ContentType:    "application/pdf",
		SizeBytes:      1024,
		IdempotencyKey: "idem-1",
		Metadata:       map[string]string{"foo": "bar"},
	})

	objID := uuid.New()
	svc.On("CreateSingle", mock.Anything, "tenant-1", "bucket-1", "application/pdf", int64(1024), map[string]string{"foo": "bar"}, (map[string]string)(nil), (*string)(nil), 0, mock.Anything).
		Return(domain.CreateObjectResponse{
			ID:     objID,
			Key:    "test-key",
			Bucket: "bucket-1",
			Upload: domain.Presigned{URL: "http://upload"},
		}, nil)

	res, err := handler.UploadObject(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, objID.String(), res.Msg.Object.ObjectId)
	assert.Equal(t, "http://upload", res.Msg.UploadUrl.Url)

	svc.AssertExpectations(t)
}

func TestObjectHandler_GetObjectMetadata(t *testing.T) {
	log := zap.NewNop()
	svc := &domainmocks.MockObjectsService{}
	handler := NewObjectHandler(log, svc)

	ctx := context.Background()
	req := connect.NewRequest(&GetObjectMetadataRequest{
		TenantId: "tenant-1",
		Bucket:   "bucket-1",
		Key:      "test-key",
	})

	objID := uuid.New()
	svc.On("GetByKey", mock.Anything, "tenant-1", "bucket-1", "test-key").
		Return(&domain.Object{
			ID:        objID,
			ObjectKey: "test-key",
			Bucket:    "bucket-1",
			Status:    domain.ObjectComplete,
		}, nil)

	res, err := handler.GetObjectMetadata(ctx, req)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, objID.String(), res.Msg.Object.ObjectId)

	svc.AssertExpectations(t)
}
