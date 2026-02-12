package service

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestPolicy_Authorize(t *testing.T) {
	p := NewPolicy(config.Policy{})

	t.Run("Valid Tenant", func(t *testing.T) {
		err := p.Authorize(context.Background(), "test-tenant", domain.ActionCreate)
		assert.NoError(t, err)
	})

	t.Run("Empty Tenant", func(t *testing.T) {
		err := p.Authorize(context.Background(), "", domain.ActionCreate)
		assert.Error(t, err)
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
		assert.NoError(t, err)
	})

	t.Run("Invalid Content Type Format", func(t *testing.T) {
		err := p.Validate("invalid", 512)
		assert.Error(t, err)
	})

	t.Run("Content Type Not Allowed", func(t *testing.T) {
		err := p.Validate("text/plain", 512)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("Size Too Large", func(t *testing.T) {
		err := p.Validate("image/png", 2048)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})
}
