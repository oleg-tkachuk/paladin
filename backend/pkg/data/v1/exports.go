// Package datav1 re-exports the protobuf-generated types for paladin.data.v1.
package datav1

import internal "github.com/oleg-tkachuk/paladin-private/internal/api/pb/data/v1"

type (
	Object            = internal.Object
	ObjectState       = internal.ObjectState
	ChecksumDigest    = internal.ChecksumDigest
	ObjectLockState   = internal.ObjectLockState
	PhysicalPlacement = internal.PhysicalPlacement
	CompletedPart     = internal.CompletedPart
	PartInfo          = internal.PartInfo
	PresignTransport  = internal.PresignTransport

	UploadObjectRequest    = internal.UploadObjectRequest
	UploadObjectResponse   = internal.UploadObjectResponse
	DownloadObjectRequest  = internal.DownloadObjectRequest
	DownloadObjectResponse = internal.DownloadObjectResponse
	GetObjectRequest       = internal.GetObjectRequest
	LookupObjectRequest    = internal.LookupObjectRequest
	UpdateObjectRequest    = internal.UpdateObjectRequest
	CompleteObjectRequest  = internal.CompleteObjectRequest
	DeleteObjectRequest    = internal.DeleteObjectRequest
	DeleteObjectResponse   = internal.DeleteObjectResponse
	RestoreObjectRequest   = internal.RestoreObjectRequest
	CopyObjectRequest      = internal.CopyObjectRequest
	MetadataOverride       = internal.MetadataOverride
	TagsOverride           = internal.TagsOverride
	ListObjectsRequest     = internal.ListObjectsRequest
	ListObjectsResponse    = internal.ListObjectsResponse
	CountObjectsRequest    = internal.CountObjectsRequest
	CountObjectsResponse   = internal.CountObjectsResponse

	InitiateMultipartUploadRequest  = internal.InitiateMultipartUploadRequest
	InitiateMultipartUploadResponse = internal.InitiateMultipartUploadResponse
	PresignPartRequest              = internal.PresignPartRequest
	PresignPartResponse             = internal.PresignPartResponse
	CompleteMultipartUploadRequest  = internal.CompleteMultipartUploadRequest
	AbortMultipartUploadRequest     = internal.AbortMultipartUploadRequest
	AbortMultipartUploadResponse    = internal.AbortMultipartUploadResponse
	ListPartsRequest                = internal.ListPartsRequest
	ListPartsResponse               = internal.ListPartsResponse

	RegenerateUploadUrlRequest  = internal.RegenerateUploadUrlRequest
	RegenerateUploadUrlResponse = internal.RegenerateUploadUrlResponse
	PresignDownloadRequest      = internal.PresignDownloadRequest
	PresignDownloadResponse     = internal.PresignDownloadResponse

	GetObjectTagsRequest     = internal.GetObjectTagsRequest
	GetObjectTagsResponse    = internal.GetObjectTagsResponse
	PutObjectTagsRequest     = internal.PutObjectTagsRequest
	PutObjectTagsResponse    = internal.PutObjectTagsResponse
	DeleteObjectTagsRequest  = internal.DeleteObjectTagsRequest
	DeleteObjectTagsResponse = internal.DeleteObjectTagsResponse

	Operation                  = internal.Operation
	ObjectSelector             = internal.ObjectSelector
	BatchDeleteObjectsRequest  = internal.BatchDeleteObjectsRequest
	BatchCopyObjectsRequest    = internal.BatchCopyObjectsRequest
	BatchRestoreObjectsRequest = internal.BatchRestoreObjectsRequest
	BatchUpdateTagsRequest     = internal.BatchUpdateTagsRequest

	GetOperationRequest    = internal.GetOperationRequest
	ListOperationsRequest  = internal.ListOperationsRequest
	ListOperationsResponse = internal.ListOperationsResponse
	CancelOperationRequest = internal.CancelOperationRequest

	EnsureTenantStorageRequest  = internal.EnsureTenantStorageRequest
	EnsureTenantStorageResponse = internal.EnsureTenantStorageResponse
)

const (
	ObjectState_PENDING   = internal.ObjectState_OBJECT_STATE_PENDING
	ObjectState_AVAILABLE = internal.ObjectState_OBJECT_STATE_AVAILABLE
	ObjectState_FAILED    = internal.ObjectState_OBJECT_STATE_FAILED
	ObjectState_DELETED   = internal.ObjectState_OBJECT_STATE_DELETED

	PresignTransport_PUT  = internal.PresignTransport_PRESIGN_TRANSPORT_PUT
	PresignTransport_POST = internal.PresignTransport_PRESIGN_TRANSPORT_POST
)
