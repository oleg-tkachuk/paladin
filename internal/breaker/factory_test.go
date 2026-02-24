package breaker_test

import (
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/breaker"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFactory(t *testing.T) {
	cfg := config.Config{}
	f := breaker.NewFactory(cfg)
	require.NotNil(t, f)

	// Get a breaker
	b1 := f.Get("b-test-1")
	require.NotNil(t, b1)

	// Get the same one
	b2 := f.Get("b-test-1")
	assert.Equal(t, b1, b2, "factory should return the same instance")

	b3 := f.Get("b-test-2")
	assert.NotEqual(t, b1, b3)

	health := f.CheckHealth()
	assert.Contains(t, health, "b-test-1")
	assert.Contains(t, health, "b-test-2")
	assert.Equal(t, "closed", health["b-test-1"])
}
