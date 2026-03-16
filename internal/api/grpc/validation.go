package grpcapi

import (
	"fmt"

	"github.com/google/uuid"
)

// Validate implements middleware.GRPCValidator for CreateObjectRequest.
func (r *CreateObjectRequest) Validate() error {
	if r.ContentType == "" {
		return fmt.Errorf("content_type is required")
	}

	if r.SizeBytes <= 0 {
		return fmt.Errorf("size_bytes must be greater than 0")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for GetObjectRequest.
func (r *GetObjectRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for GetObjectMetaRequest.
func (r *GetObjectMetaRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for CompleteObjectRequest.
func (r *CompleteObjectRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for DeleteObjectRequest.
func (r *DeleteObjectRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for RestoreObjectRequest.
func (r *RestoreObjectRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for PurgeObjectRequest.
func (r *PurgeObjectRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	if _, err := uuid.Parse(r.ObjectId); err != nil {
		return fmt.Errorf("object_id must be a valid UUID")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for PatchObjectMetaRequest.
func (r *PatchObjectMetaRequest) Validate() error {
	if r.ObjectId == "" {
		return fmt.Errorf("object_id is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for InitiateMultipartRequest.
func (r *InitiateMultipartRequest) Validate() error {
	if r.ContentType == "" {
		return fmt.Errorf("content_type is required")
	}

	if r.SizeBytes <= 0 {
		return fmt.Errorf("size_bytes must be greater than 0")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for SignPartRequest.
func (r *SignPartRequest) Validate() error {
	if r.UploadId == "" {
		return fmt.Errorf("upload_id is required")
	}

	if r.PartNumber < 1 {
		return fmt.Errorf("part_number must be >= 1")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for CompleteMultipartRequest.
func (r *CompleteMultipartRequest) Validate() error {
	if r.UploadId == "" {
		return fmt.Errorf("upload_id is required")
	}

	if len(r.Parts) == 0 {
		return fmt.Errorf("parts must not be empty")
	}

	for _, p := range r.Parts {
		if p.PartNumber < 1 {
			return fmt.Errorf("each part must have part_number >= 1")
		}

		if p.Etag == "" {
			return fmt.Errorf("each part must have a non-empty etag")
		}
	}

	return nil
}

// Validate implements middleware.GRPCValidator for AbortMultipartRequest.
func (r *AbortMultipartRequest) Validate() error {
	if r.UploadId == "" {
		return fmt.Errorf("upload_id is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for CreateCategoryRequest.
func (r *CreateCategoryRequest) Validate() error {
	if r.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	if r.Name == "" {
		return fmt.Errorf("name is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for GetCategoryRequest.
func (r *GetCategoryRequest) Validate() error {
	if r.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for DeleteCategoryRequest.
func (r *DeleteCategoryRequest) Validate() error {
	if r.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for GetCategoryStatsRequest.
func (r *GetCategoryStatsRequest) Validate() error {
	if r.Slug == "" {
		return fmt.Errorf("slug is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for CreateTenantRequest.
func (r *CreateTenantRequest) Validate() error {
	if r.TenantId == "" {
		return fmt.Errorf("tenant_id is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for GetTenantRequest.
func (r *GetTenantRequest) Validate() error {
	if r.TenantId == "" {
		return fmt.Errorf("tenant_id is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for DeleteTenantRequest.
func (r *DeleteTenantRequest) Validate() error {
	if r.TenantId == "" {
		return fmt.Errorf("tenant_id is required")
	}

	return nil
}

// Validate implements middleware.GRPCValidator for PatchTenantMetadataRequest.
func (r *PatchTenantMetadataRequest) Validate() error {
	if r.TenantId == "" {
		return fmt.Errorf("tenant_id is required")
	}

	return nil
}
