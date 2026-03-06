package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
)

const (
	maxLabelKeys      = 10
	maxLabelSizeBytes = 4096
	defaultListLimit  = 20
	maxListLimit      = 100
)

type tenantService struct {
	repo domain.TenantRepository
}

// NewTenantService creates a new TenantService backed by the given repository.
func NewTenantService(repo domain.TenantRepository) domain.TenantService {
	return &tenantService{repo: repo}
}

// Create provisions a new tenant. Re-creating the same tenant_id updates
// display_name, labels, and tags, returning the resulting record (idempotent).
func (s *tenantService) Create(ctx context.Context, tenantID string, displayName *string, labels map[string]string, tags []string) (*domain.Tenant, error) {
	if tenantID == "" {
		return nil, apperrors.BadRequest("tenant_id must not be empty", nil)
	}

	if err := validateLabels(labels); err != nil {
		return nil, err
	}

	rec := domain.Tenant{
		ID:          uuid.New(),
		TenantID:    tenantID,
		DisplayName: displayName,
		Labels:      labels,
		Tags:        normalizeTags(tags),
	}

	t, err := s.repo.Create(ctx, rec)
	if err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	return t, nil
}

// Get retrieves a tenant by tenant_id.
func (s *tenantService) Get(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	t, err := s.repo.Get(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	return t, nil
}

// Delete removes a tenant. Returns ErrConflict when the tenant still has
// active objects. Callers must BulkPurge all objects before deleting.
func (s *tenantService) Delete(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return apperrors.BadRequest("tenant_id must not be empty", nil)
	}

	hasActive, err := s.repo.HasActiveObjects(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("delete tenant: check active objects: %w", err)
	}

	if hasActive {
		return apperrors.Conflict(
			"tenant has active objects; purge all objects before deleting the tenant",
			nil,
		)
	}

	deleted, err := s.repo.Delete(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("delete tenant: %w", err)
	}
	if !deleted {
		return apperrors.NotFound("tenant not found", nil)
	}

	return nil
}

// PatchMetadata merges the label patch into existing labels and replaces tags.
// labelsPatch values may be nil (interface{}) to delete a key.
func (s *tenantService) PatchMetadata(ctx context.Context, tenantID string, labelsPatch map[string]interface{}, tags []string) (*domain.Tenant, error) {
	if tenantID == "" {
		return nil, apperrors.BadRequest("tenant_id must not be empty", nil)
	}

	// Validate total label patch size.
	if len(labelsPatch) > maxLabelKeys {
		return nil, apperrors.BadRequest(
			fmt.Sprintf("label patch exceeds maximum of %d keys", maxLabelKeys),
			nil,
		)
	}

	if b, err := json.Marshal(labelsPatch); err == nil && len(b) > maxLabelSizeBytes {
		return nil, apperrors.BadRequest(
			fmt.Sprintf("label patch exceeds maximum size of %d bytes", maxLabelSizeBytes),
			nil,
		)
	}

	t, err := s.repo.UpdateMetadata(ctx, tenantID, labelsPatch, normalizeTags(tags))
	if err != nil {
		return nil, fmt.Errorf("patch tenant metadata: %w", err)
	}

	return t, nil
}

// List returns tenants matching filter, cursor-paginated.
func (s *tenantService) List(ctx context.Context, filter domain.ListTenantsFilter) ([]domain.Tenant, string, int64, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultListLimit
	}

	if filter.Limit > maxListLimit {
		filter.Limit = maxListLimit
	}

	tenants, nextCursor, total, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, "", 0, fmt.Errorf("list tenants: %w", err)
	}

	return tenants, nextCursor, total, nil
}

// validateLabels enforces label count and size constraints.
func validateLabels(labels map[string]string) error {
	if len(labels) > maxLabelKeys {
		return apperrors.BadRequest(
			fmt.Sprintf("labels exceed maximum of %d keys", maxLabelKeys),
			nil,
		)
	}

	if len(labels) > 0 {
		b, err := json.Marshal(labels)
		if err != nil {
			return apperrors.BadRequest("invalid labels", err)
		}

		if len(b) > maxLabelSizeBytes {
			return apperrors.BadRequest(
				fmt.Sprintf("labels exceed maximum size of %d bytes", maxLabelSizeBytes),
				nil,
			)
		}
	}

	return nil
}

// normalizeTags deduplicates and returns a stable tag slice.
// A nil input is treated as an empty slice.
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))

	for _, t := range tags {
		if _, ok := seen[t]; !ok && t != "" {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}

	return out
}
