package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetEnvOrDefault(t *testing.T) {
	key := "TEST_ENV_KEY_123"
	defer os.Unsetenv(key)

	t.Run("Default", func(t *testing.T) {
		os.Unsetenv(key)
		assert.Equal(t, "default", GetEnvOrDefault(key, "default"))
	})

	t.Run("Env Set", func(t *testing.T) {
		os.Setenv(key, "value")
		assert.Equal(t, "value", GetEnvOrDefault(key, "default"))
	})
}
