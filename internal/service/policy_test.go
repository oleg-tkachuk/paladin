package service

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicy_Authorize(t *testing.T) {
	p := NewPolicy(config.Policy{})

	t.Run("Valid Tenant", func(t *testing.T) {
		err := p.Authorize(context.Background(), "test-tenant", domain.ActionCreate)
		require.NoError(t, err)
	})

	t.Run("Empty Tenant", func(t *testing.T) {
		err := p.Authorize(context.Background(), "", domain.ActionCreate)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tenant_id required")
	})
}

func TestPolicy_Validate(t *testing.T) {
	p := NewPolicy(config.Policy{
		MaxObjectSizeBytes:  1024,
		AllowedContentTypes: []string{"image/png", "application/json"},
	})

	t.Run("Valid", func(t *testing.T) {
		err := p.Validate("image/png", 512)
		require.NoError(t, err)
	})

	t.Run("Invalid Content Type Format", func(t *testing.T) {
		err := p.Validate("invalid", 512)
		require.Error(t, err)
	})

	t.Run("Content Type Not Allowed", func(t *testing.T) {
		err := p.Validate("text/plain", 512)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("Size Too Large", func(t *testing.T) {
		err := p.Validate("image/png", 2048)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})
}
