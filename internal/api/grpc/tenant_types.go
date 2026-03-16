package grpcapi

import (
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"google.golang.org/grpc/status"
)

// grpcError converts a domain error (AppError) to a Connect error with
// the correct gRPC status code. Both gRPC and Connect handlers use this
// because connect.NewError is wire-compatible for both protocols.
func grpcError(err error) error {
	grpcErr := apperrors.MapToGRPC(err)
	if s, ok := status.FromError(grpcErr); ok {
		return connect.NewError(connect.Code(s.Code()), errors.New(s.Message()))
	}
	return grpcErr
}

// ─── Tenant domain → proto helpers ───────────────────────────────────────────

func toCreateTenantResponse(t *domain.Tenant) *CreateTenantResponse {
	displayName, labels, tags := normalizeTenantFields(t)

	return &CreateTenantResponse{
		TenantId:      t.TenantID,
		DisplayName:   displayName,
		Labels:        labels,
		Tags:          tags,
		CreatedAtUnix: t.CreatedAt.Unix(),
		UpdatedAtUnix: t.UpdatedAt.Unix(),
	}
}

func toGetTenantResponse(t *domain.Tenant) *GetTenantResponse {
	displayName, labels, tags := normalizeTenantFields(t)

	return &GetTenantResponse{
		TenantId:      t.TenantID,
		DisplayName:   displayName,
		Labels:        labels,
		Tags:          tags,
		CreatedAtUnix: t.CreatedAt.Unix(),
		UpdatedAtUnix: t.UpdatedAt.Unix(),
	}
}

func toListTenantsItem(t *domain.Tenant) *ListTenantsItem {
	displayName, labels, tags := normalizeTenantFields(t)

	return &ListTenantsItem{
		TenantId:      t.TenantID,
		DisplayName:   displayName,
		Labels:        labels,
		Tags:          tags,
		CreatedAtUnix: t.CreatedAt.Unix(),
		UpdatedAtUnix: t.UpdatedAt.Unix(),
	}
}

func toPatchTenantMetadataResponse(t *domain.Tenant) *PatchTenantMetadataResponse {
	displayName, labels, tags := normalizeTenantFields(t)

	return &PatchTenantMetadataResponse{
		TenantId:      t.TenantID,
		DisplayName:   displayName,
		Labels:        labels,
		Tags:          tags,
		CreatedAtUnix: t.CreatedAt.Unix(),
		UpdatedAtUnix: t.UpdatedAt.Unix(),
	}
}

// normalizeTenantFields extracts and normalises the display name, labels,
// and tags from a domain.Tenant, replacing nil maps/slices with empty values
// to produce deterministic proto output.
func normalizeTenantFields(t *domain.Tenant) (string, map[string]string, []string) {
	displayName := ""
	if t.DisplayName != nil {
		displayName = *t.DisplayName
	}

	labels := t.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	tags := t.Tags
	if tags == nil {
		tags = []string{}
	}

	return displayName, labels, tags
}

// ─── JSON helpers ────────────────────────────────────────────────────────────

// parseLabelsPatch safely decodes a JSON string into a map[string]interface{}.
// Valid JSON null values for keys are preserved for the merge-delete semantics.
func parseLabelsPatch(raw string) (map[string]interface{}, error) {
	if raw == "" || raw == "{}" {
		return map[string]interface{}{}, nil
	}

	var patch map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &patch); err != nil {
		return nil, fmt.Errorf("labels_patch_json must be valid JSON: %w", err)
	}

	// Only string or null values are allowed.
	for k, v := range patch {
		if v != nil {
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("label value for key %q must be a string or null", k)
			}
		}
	}

	return patch, nil
}
