package grpcapi

import (
	"encoding/json"
	"fmt"
	"time"

	apperrors "github.com/oleg-tkachuk/paladin/internal/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcError converts a domain error (AppError) to a properly-coded gRPC status.
// This replaces all the ad-hoc status.Error(codes.Internal, err.Error()) calls
// that were previously scattered across handlers.
func grpcError(err error) error {
	return apperrors.MapToGRPC(err)
}

// ─── Tenant proto message types ──────────────────────────────────────────────
// These are now generated from paladin.proto.

// ─── Validation for PatchObjectMetaRequest ───────────────────────────────────

// Validate implements middleware.GRPCValidator for PatchObjectMetaRequest.
func (r *PatchObjectMetaRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	return nil
}

// ─── Helper: tenant domain → proto response ──────────────────────────────────

func tenantToProtoResponse(t interface {
	GetID() string
	GetTenantID() string
	GetDisplayName() *string
	GetLabels() map[string]string
	GetTags() []string
	GetCreatedAt() time.Time
	GetUpdatedAt() time.Time
}) *TenantResponse {
	displayName := ""
	if dn := t.GetDisplayName(); dn != nil {
		displayName = *dn
	}

	return &TenantResponse{
		TenantId:      t.GetTenantID(),
		DisplayName:   displayName,
		Labels:        t.GetLabels(),
		Tags:          t.GetTags(),
		CreatedAtUnix: t.GetCreatedAt().Unix(),
		UpdatedAtUnix: t.GetUpdatedAt().Unix(),
	}
}

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

// statusErr wraps a plain error from gRPC status string extraction.
func statusErr(code codes.Code, format string, args ...any) error {
	return status.Errorf(code, format, args...)
}
