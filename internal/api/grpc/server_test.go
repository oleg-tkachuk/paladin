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

	server := grpcapi.NewServer(logger, svc)
	assert.NotNil(t, server)
}
