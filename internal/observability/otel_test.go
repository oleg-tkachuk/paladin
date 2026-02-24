package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitOTel_Disabled(t *testing.T) {
	ctx := context.Background()
	cfg := config.OTel{
		Enabled: false,
	}

	shutdown, err := observability.InitOTel(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	err = shutdown(ctx)
	assert.NoError(t, err)
}

func TestInitOTel_Enabled(t *testing.T) {
	ctx := context.Background()
	cfg := config.OTel{
		Enabled:  true,
		Endpoint: "localhost:4317",
		Resource: config.OTelResource{
			ServiceName:           "test-service",
			DeploymentEnvironment: "test",
		},
	}

	shutdown, err := observability.InitOTel(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	ctxCancel, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()

	// Best-effort shutdown; ignoring error as localhost:4317 won't be reachable.
	_ = shutdown(ctxCancel)
}
