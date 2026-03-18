package postgres

import (
	"context"
	"testing"

	pgmocks "github.com/oleg-tkachuk/paladin/internal/store/postgres/mocks"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDB_Ping_Mockery(t *testing.T) {
	mockPool := new(pgmocks.MockPgxPool)
	db := &DB{Pool: mockPool, log: zap.NewNop()}
	ctx := context.Background()

	mockPool.On("Ping", ctx).Return(nil).Once()

	err := db.Ping(ctx)
	require.NoError(t, err)
	mockPool.AssertExpectations(t)
}
