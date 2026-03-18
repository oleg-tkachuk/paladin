package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	mockdomain "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCachedObjectsRepo_Get(t *testing.T) {
	mockRepo := new(mockdomain.MockObjectsRepository)
	repo := NewCachedObjectsRepo(mockRepo, 10, 1*time.Minute, zap.NewNop())
	ctx := context.Background()

	id := uuid.New()
	tenantID := "test-tenant"
	obj := &domain.Object{ID: id, TenantID: tenantID, ObjectKey: "key1"}

	// 1. Cache miss - should call underlying repo
	mockRepo.On("Get", mock.Anything, tenantID, id).Return(obj, nil).Once()

	res, err := repo.Get(ctx, tenantID, id)
	require.NoError(t, err)
	assert.Equal(t, obj, res)

	// 2. Cache hit - should NOT call underlying repo
	res, err = repo.Get(ctx, tenantID, id)
	require.NoError(t, err)
	assert.Equal(t, obj, res)

	mockRepo.AssertExpectations(t)
}

func TestCachedObjectsRepo_Create(t *testing.T) {
	mockRepo := new(mockdomain.MockObjectsRepository)
	repo := NewCachedObjectsRepo(mockRepo, 10, 1*time.Minute, zap.NewNop())
	ctx := context.Background()

	obj := domain.Object{ID: uuid.New(), TenantID: "test-tenant", ObjectKey: "key1"}

	mockRepo.On("Create", mock.Anything, obj).Return(nil).Once()

	err := repo.Create(ctx, obj)
	require.NoError(t, err)

	// Now it should be in cache
	res, err := repo.Get(ctx, obj.TenantID, obj.ID)
	require.NoError(t, err)
	assert.Equal(t, &obj, res)

	mockRepo.AssertExpectations(t)
}

func TestCachedObjectsRepo_MarkComplete(t *testing.T) {
	mockRepo := new(mockdomain.MockObjectsRepository)
	repo := NewCachedObjectsRepo(mockRepo, 10, 1*time.Minute, zap.NewNop())
	ctx := context.Background()

	id := uuid.New()
	tenantID := "test-tenant"
	obj := &domain.Object{ID: id, TenantID: tenantID, ObjectKey: "key1"}
	completedObj := &domain.Object{ID: id, TenantID: tenantID, ObjectKey: "key1", Status: "complete"}

	// Put in cache first
	mockRepo.On("Get", mock.Anything, tenantID, id).Return(obj, nil).Once()
	_, _ = repo.Get(ctx, tenantID, id)

	// Mark complete - write-through: should re-fetch from underlying repo and cache
	mockRepo.On("MarkComplete", mock.Anything, tenantID, id, "etag1", int64(100)).Return(true, nil).Once()
	mockRepo.On("Get", mock.Anything, tenantID, id).Return(completedObj, nil).Once() // write-through re-fetch

	updated, err := repo.MarkComplete(ctx, tenantID, id, "etag1", int64(100))
	require.NoError(t, err)
	assert.True(t, updated)

	// Should be a cache HIT now (write-through cached the completed object)
	res, err := repo.Get(ctx, tenantID, id)
	require.NoError(t, err)
	assert.Equal(t, completedObj, res)

	mockRepo.AssertExpectations(t)
}
