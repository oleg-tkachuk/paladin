package openapi_test

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/openapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSpec(t *testing.T) {
	spec, err := openapi.LoadSpec()
	require.NoError(t, err)
	assert.NotNil(t, spec)
	assert.Greater(t, len(spec.Paths.Map()), 0, "should have paths")

	// Test caching/idempotency
	spec2, err2 := openapi.LoadSpec()
	require.NoError(t, err2)
	assert.Equal(t, spec, spec2, "should return the same cached instance")
}
