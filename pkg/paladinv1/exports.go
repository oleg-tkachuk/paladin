// Package paladinv1 re-exports the protobuf-generated types and messages from
// internal/api/pb/v1 so downstream Go modules can consume them without
// violating the Go internal-package rule.
package paladinv1

import internal "github.com/oleg-tkachuk/paladin/internal/api/pb/v1"

// ─── Enums ───────────────────────────────────────────────────────────────────

type ObjectState = internal.ObjectState

const (
	ObjectState_OBJECT_STATE_UNSPECIFIED = internal.ObjectState_OBJECT_STATE_UNSPECIFIED
	ObjectState_OBJECT_STATE_PENDING     = internal.ObjectState_OBJECT_STATE_PENDING
	ObjectState_OBJECT_STATE_AVAILABLE   = internal.ObjectState_OBJECT_STATE_AVAILABLE
	ObjectState_OBJECT_STATE_FAILED      = internal.ObjectState_OBJECT_STATE_FAILED
	ObjectState_OBJECT_STATE_DELETED     = internal.ObjectState_OBJECT_STATE_DELETED
)

type ChecksumAlgorithm = internal.ChecksumAlgorithm

const (
	ChecksumAlgorithm_CHECKSUM_ALGORITHM_UNSPECIFIED = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_UNSPECIFIED
	ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C      = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C
	ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256      = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256
	ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5         = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5
)

type SortOrder = internal.SortOrder

const (
	SortOrder_SORT_ORDER_UNSPECIFIED = internal.SortOrder_SORT_ORDER_UNSPECIFIED
	SortOrder_SORT_ORDER_ASC         = internal.SortOrder_SORT_ORDER_ASC
	SortOrder_SORT_ORDER_DESC        = internal.SortOrder_SORT_ORDER_DESC
)

type PresignTransport = internal.PresignTransport

const (
	PresignTransport_PRESIGN_TRANSPORT_UNSPECIFIED = internal.PresignTransport_PRESIGN_TRANSPORT_UNSPECIFIED
	PresignTransport_PRESIGN_TRANSPORT_PUT         = internal.PresignTransport_PRESIGN_TRANSPORT_PUT
	PresignTransport_PRESIGN_TRANSPORT_POST        = internal.PresignTransport_PRESIGN_TRANSPORT_POST
)

type CompletionMode = internal.CompletionMode

const (
	CompletionMode_COMPLETION_MODE_UNSPECIFIED = internal.CompletionMode_COMPLETION_MODE_UNSPECIFIED
	CompletionMode_COMPLETION_MODE_IMPLICIT    = internal.CompletionMode_COMPLETION_MODE_IMPLICIT
	CompletionMode_COMPLETION_MODE_EXPLICIT    = internal.CompletionMode_COMPLETION_MODE_EXPLICIT
)

// ─── Core messages ───────────────────────────────────────────────────────────

type (
	Object                 = internal.Object
	PresignedUrl           = internal.PresignedUrl
	PresignedPostPolicy    = internal.PresignedPostPolicy
	ObjectKey              = internal.ObjectKey
	ObjectKeyPolicy        = internal.ObjectKeyPolicy
	ObjectKeyStats         = internal.ObjectKeyStats
	LifecycleRule          = internal.LifecycleRule
	LifecycleTransition    = internal.LifecycleTransition
	LifecycleExpiration    = internal.LifecycleExpiration
	Tenant                 = internal.Tenant
	ObjectTag              = internal.ObjectTag
	Bucket                 = internal.Bucket
	CompletedPart          = internal.CompletedPart
	PartInfo               = internal.PartInfo
	Operation              = internal.Operation
	HealthInfo             = internal.HealthInfo
	ComponentHealth        = internal.ComponentHealth
	VersionInfo            = internal.VersionInfo
	MetadataOverride       = internal.MetadataOverride
	TagsOverride           = internal.TagsOverride
	ObjectSelector         = internal.ObjectSelector
	BatchFailure           = internal.BatchFailure
	BatchOperationMetadata = internal.BatchOperationMetadata
	BatchOperationResult   = internal.BatchOperationResult
)

// ─── Object service ──────────────────────────────────────────────────────────

type (
	UploadObjectRequest    = internal.UploadObjectRequest
	UploadObjectResponse   = internal.UploadObjectResponse
	UploadSmallRequest     = internal.UploadSmallRequest
	UploadSmallResponse    = internal.UploadSmallResponse
	UploadSmallInit        = internal.UploadSmallInit
	DownloadObjectRequest  = internal.DownloadObjectRequest
	DownloadObjectResponse = internal.DownloadObjectResponse
	GetObjectRequest       = internal.GetObjectRequest
	LookupObjectRequest    = internal.LookupObjectRequest
	UpdateObjectRequest    = internal.UpdateObjectRequest
	CompleteObjectRequest  = internal.CompleteObjectRequest
	DeleteObjectRequest    = internal.DeleteObjectRequest
	RestoreObjectRequest   = internal.RestoreObjectRequest
	CopyObjectRequest      = internal.CopyObjectRequest
	ListObjectsRequest     = internal.ListObjectsRequest
	ListObjectsResponse    = internal.ListObjectsResponse
	CountObjectsRequest    = internal.CountObjectsRequest
	CountObjectsResponse   = internal.CountObjectsResponse
)

// ─── Presign service ─────────────────────────────────────────────────────────

type (
	RegenerateUploadUrlRequest  = internal.RegenerateUploadUrlRequest
	RegenerateUploadUrlResponse = internal.RegenerateUploadUrlResponse
	PresignDownloadRequest      = internal.PresignDownloadRequest
	PresignDownloadResponse     = internal.PresignDownloadResponse
)

// ─── Multipart service ───────────────────────────────────────────────────────

type (
	InitiateMultipartUploadRequest  = internal.InitiateMultipartUploadRequest
	InitiateMultipartUploadResponse = internal.InitiateMultipartUploadResponse
	PresignPartRequest              = internal.PresignPartRequest
	PresignPartResponse             = internal.PresignPartResponse
	CompleteMultipartUploadRequest  = internal.CompleteMultipartUploadRequest
	AbortMultipartUploadRequest     = internal.AbortMultipartUploadRequest
	AbortMultipartUploadResponse    = internal.AbortMultipartUploadResponse
	ListPartsRequest                = internal.ListPartsRequest
	ListPartsResponse               = internal.ListPartsResponse
)

// ─── ObjectTag service ───────────────────────────────────────────────────────

type (
	CreateObjectTagRequest  = internal.CreateObjectTagRequest
	GetObjectTagRequest     = internal.GetObjectTagRequest
	UpdateObjectTagRequest  = internal.UpdateObjectTagRequest
	DeleteObjectTagRequest  = internal.DeleteObjectTagRequest
	DeleteObjectTagResponse = internal.DeleteObjectTagResponse
	ListObjectTagsRequest   = internal.ListObjectTagsRequest
	ListObjectTagsResponse  = internal.ListObjectTagsResponse
)

// ─── Bucket service ──────────────────────────────────────────────────────────

type (
	CreateBucketRequest  = internal.CreateBucketRequest
	GetBucketRequest     = internal.GetBucketRequest
	UpdateBucketRequest  = internal.UpdateBucketRequest
	DeleteBucketRequest  = internal.DeleteBucketRequest
	DeleteBucketResponse = internal.DeleteBucketResponse
	ListBucketsRequest   = internal.ListBucketsRequest
	ListBucketsResponse  = internal.ListBucketsResponse
)

// ─── ObjectKey service ──────────────────────────────────────────────────────────

type (
	CreateObjectKeyRequest   = internal.CreateObjectKeyRequest
	GetObjectKeyRequest      = internal.GetObjectKeyRequest
	UpdateObjectKeyRequest   = internal.UpdateObjectKeyRequest
	DeleteObjectKeyRequest   = internal.DeleteObjectKeyRequest
	DeleteObjectKeyResponse  = internal.DeleteObjectKeyResponse
	ListObjectKeysRequest    = internal.ListObjectKeysRequest
	ListObjectKeysResponse   = internal.ListObjectKeysResponse
	GetObjectKeyStatsRequest = internal.GetObjectKeyStatsRequest
)

// ─── Tenant service ──────────────────────────────────────────────────────────

type (
	CreateTenantRequest  = internal.CreateTenantRequest
	GetTenantRequest     = internal.GetTenantRequest
	UpdateTenantRequest  = internal.UpdateTenantRequest
	DeleteTenantRequest  = internal.DeleteTenantRequest
	DeleteTenantResponse = internal.DeleteTenantResponse
	ListTenantsRequest   = internal.ListTenantsRequest
	ListTenantsResponse  = internal.ListTenantsResponse
)

// ─── Operation service ───────────────────────────────────────────────────────

type (
	GetOperationRequest    = internal.GetOperationRequest
	ListOperationsRequest  = internal.ListOperationsRequest
	ListOperationsResponse = internal.ListOperationsResponse
	WatchOperationRequest  = internal.WatchOperationRequest
	CancelOperationRequest = internal.CancelOperationRequest
)

// ─── Batch service ───────────────────────────────────────────────────────────

type (
	BatchCopyObjectsRequest    = internal.BatchCopyObjectsRequest
	BatchDeleteObjectsRequest  = internal.BatchDeleteObjectsRequest
	BatchRestoreObjectsRequest = internal.BatchRestoreObjectsRequest
)

// ─── Policy service ──────────────────────────────────────────────────────────

type (
	ValidatePolicyRequest  = internal.ValidatePolicyRequest
	ValidatePolicyResponse = internal.ValidatePolicyResponse
)

// ─── System service ──────────────────────────────────────────────────────────

type (
	GetVersionRequest = internal.GetVersionRequest
	GetHealthRequest  = internal.GetHealthRequest
)
