package grpcapi_test

import (
	"testing"

	grpcapi "github.com/oleg-tkachuk/paladin/internal/api/grpc"
	mocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain" // Added import for domain.Tenant
	"github.com/stretchr/testify/mock"                             // Added import for mock
)

func TestGRPCServer_Dummy(t *testing.T) {
	logger := zap.NewNop()
	svc := mocks.NewMockObjectsService(t)
	catSvc := mocks.NewMockCategoryService(t)
	tenantSvc := mocks.NewMockTenantService(t)
	tenantSvc.On("PatchMetadata", mock.Anything, "t1", mock.Anything, mock.Anything, mock.Anything).Return(&domain.Tenant{TenantID: "t1"}, nil).Maybe()

	server := grpcapi.NewServer(logger, svc, catSvc, tenantSvc)
	assert.NotNil(t, server)
}
