package utils

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestIDFromContext(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), RequestIDKey, "req-123")
		assert.Equal(t, "req-123", RequestIDFromContext(ctx, "fallback"))
	})

	t.Run("Fallback", func(t *testing.T) {
		assert.Equal(t, "fallback", RequestIDFromContext(context.Background(), "fallback"))
	})
}

func TestTenantIDFromContext(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), TenantIDKey, "tenant-123")
		assert.Equal(t, "tenant-123", TenantIDFromContext(ctx, "fallback"))
	})

	t.Run("Fallback", func(t *testing.T) {
		assert.Equal(t, "fallback", TenantIDFromContext(context.Background(), "fallback"))
	})
}

func TestWithTenantID(t *testing.T) {
	ctx := WithTenantID(context.Background(), "tenant-123")
	assert.Equal(t, "tenant-123", ctx.Value(TenantIDKey))
}
