package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/validation"
)

type policyImpl struct {
	maxObjectSizeBytes  int64
	allowedContentTypes map[string]struct{}
	presignPutTTL       time.Duration
	presignGetTTL       time.Duration
	presignPartTTL      time.Duration
}

func NewPolicy(cfg config.Policy) domain.Policy {
	m := make(map[string]struct{}, len(cfg.AllowedContentTypes))
	for _, ct := range cfg.AllowedContentTypes {
		m[strings.ToLower(strings.TrimSpace(ct))] = struct{}{}
	}

	return &policyImpl{
		maxObjectSizeBytes:  cfg.MaxObjectSizeBytes,
		allowedContentTypes: m,
		presignPutTTL:       cfg.PresignPutTTL,
		presignGetTTL:       cfg.PresignGetTTL,
		presignPartTTL:      cfg.PresignPartTTL,
	}
}

func (p *policyImpl) Authorize(ctx context.Context, tenantID string, action domain.Action) error {
	// Tenant validation.
	// In a real app, this would check RBAC/ABAC if needed.
	if tenantID == "" {
		return errors.New("unauthorized: tenant_id required")
	}

	return nil
}

func (p *policyImpl) Validate(contentType string, sizeBytes int64) error {
	// Validate content type format
	if err := validation.ContentType(contentType); err != nil {
		return fmt.Errorf("invalid content_type: %w", err)
	}

	// Validate size bounds
	if err := validation.SizeBytes(sizeBytes, p.maxObjectSizeBytes); err != nil {
		return err
	}

	// Check if content type is allowed
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if _, ok := p.allowedContentTypes[ct]; !ok {
		return fmt.Errorf("content_type not allowed: %s", contentType)
	}

	return nil
}
