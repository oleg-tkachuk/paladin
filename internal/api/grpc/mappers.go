package grpcapi

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
