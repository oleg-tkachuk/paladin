package grpcapi_test

import (
	"testing"

	grpcapi "github.com/oleg-tkachuk/paladin/internal/api/grpc"
	mocks "github.com/oleg-tkachuk/paladin/internal/domain/mocks"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestGRPCServer_Dummy(t *testing.T) {
	logger := zap.NewNop()
	svc := mocks.NewMockObjectsService(t)
	catSvc := mocks.NewMockCategoryService(t)
	tenantSvc := mocks.NewMockTenantService(t)

	server := grpcapi.NewServer(logger, svc, catSvc, tenantSvc)
	assert.NotNil(t, server)
}
