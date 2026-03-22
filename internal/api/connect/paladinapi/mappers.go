package paladinapi

import (
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// objectToProto converts a domain.Object to the proto Object message.
func objectToProto(obj *domain.Object) *Object {
	if obj == nil {
		return nil
	}

	o := &Object{
		ObjectId:    obj.ID.String(),
		Bucket:      obj.Bucket,
		Key:         obj.ObjectKey,
		ContentType: obj.ContentType,
		SizeBytes:   obj.SizeBytes,
		Status:      objectStatusToProto(obj.Status),
		Metadata:    obj.Labels,
		Tags:        obj.Tags,
		CreatedAt:   timestamppb.New(obj.CreatedAt),
		UpdatedAt:   timestamppb.New(obj.UpdatedAt),
		Category:    obj.Category,
	}

	if obj.StoredETag != nil {
		o.Etag = *obj.StoredETag
	} else if obj.ChecksumSHA256 != nil {
		o.Etag = *obj.ChecksumSHA256
	}

	if obj.ChecksumSHA256 != nil {
		o.Checksum = *obj.ChecksumSHA256
		o.ChecksumAlgorithm = ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256
	}

	if obj.ExternalRef != nil {
		o.ExternalRef = obj.ExternalRef
	}

	if obj.CompletedAt != nil {
		o.CompletedAt = timestamppb.New(*obj.CompletedAt)
	}

	if obj.DeletedAt != nil {
		o.DeletedAt = timestamppb.New(*obj.DeletedAt)
	}

	if obj.ExpiresAt != nil {
		o.ExpiresAt = timestamppb.New(*obj.ExpiresAt)
	}

	if obj.StoredSizeBytes != nil {
		o.StoredSizeBytes = obj.StoredSizeBytes
	}

	return o
}

// presignedToProto converts a domain.Presigned to the proto PresignedUrl message.
func presignedToProto(p domain.Presigned) *PresignedUrl {
	return &PresignedUrl{
		Url:       p.URL,
		Method:    p.Method,
		Headers:   p.Headers,
		ExpiresAt: timestamppb.New(p.ExpiresAt),
	}
}

// objectStatusToProto maps domain.ObjectStatus to the proto ObjectStatus enum.
func objectStatusToProto(s domain.ObjectStatus) ObjectStatus {
	switch s {
	case domain.ObjectPending:
		return ObjectStatus_OBJECT_STATUS_PENDING
	case domain.ObjectUploading:
		return ObjectStatus_OBJECT_STATUS_UPLOADING
	case domain.ObjectUploaded, domain.ObjectComplete:
		return ObjectStatus_OBJECT_STATUS_AVAILABLE
	case domain.ObjectDeleted, domain.ObjectSoftDeleted, domain.ObjectHardDeleted:
		return ObjectStatus_OBJECT_STATUS_DELETED
	case domain.ObjectAborted, domain.ObjectError:
		return ObjectStatus_OBJECT_STATUS_ARCHIVED
	default:
		return ObjectStatus_OBJECT_STATUS_UNSPECIFIED
	}
}

// protoStatusToDomain maps the proto ObjectStatus enum to domain.ObjectStatus.
func protoStatusToDomain(s ObjectStatus) domain.ObjectStatus {
	switch s {
	case ObjectStatus_OBJECT_STATUS_PENDING:
		return domain.ObjectPending
	case ObjectStatus_OBJECT_STATUS_UPLOADING:
		return domain.ObjectUploading
	case ObjectStatus_OBJECT_STATUS_AVAILABLE:
		return domain.ObjectComplete
	case ObjectStatus_OBJECT_STATUS_DELETED:
		return domain.ObjectSoftDeleted
	case ObjectStatus_OBJECT_STATUS_ARCHIVED:
		return domain.ObjectAborted
	default:
		return domain.ObjectPending
	}
}

// categoryToProto converts a domain.Category to the proto Category message.
func categoryToProto(c *domain.Category) *Category {
	if c == nil {
		return nil
	}

	return &Category{
		CategoryId:  c.ID.String(),
		TenantId:    c.TenantID,
		Slug:        c.Slug,
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   timestamppb.New(c.CreatedAt),
		UpdatedAt:   timestamppb.New(c.UpdatedAt),
	}
}

// categoryStatsToProto converts domain.CategoryStats to proto CategoryStats.
func categoryStatsToProto(s *domain.CategoryStats) *CategoryStats {
	if s == nil {
		return nil
	}

	return &CategoryStats{
		TotalCount:       s.TotalCount,
		TotalSize:        s.TotalSize,
		SoftDeletedCount: s.SoftDeletedCount,
	}
}

// tenantToProto converts a domain.Tenant to the proto Tenant message.
func tenantToProto(t *domain.Tenant) *Tenant {
	if t == nil {
		return nil
	}

	return &Tenant{
		TenantId:    t.TenantID,
		DisplayName: t.DisplayName,
		Labels:      t.Labels,
		Tags:        t.Tags,
		CreatedAt:   timestamppb.New(t.CreatedAt),
		UpdatedAt:   timestamppb.New(t.UpdatedAt),
	}
}
