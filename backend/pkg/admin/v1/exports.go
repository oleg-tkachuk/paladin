// Package adminv1 re-exports the protobuf-generated types and Connect client
// stubs for paladin.admin.v1 so downstream Go modules can consume them without
// crossing the Go internal-package boundary.
//
// Service handler interfaces and constructors live in the
// internal/api/pb/admin/v1/paladinadminv1connect package — re-exported below as
// a sub-package import (`adminv1connect "...pkg/admin/v1/connect"`).
package adminv1

import internal "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"

// ─── Storage backend ────────────────────────────────────────────────────────

type (
	StorageBackend       = internal.StorageBackend
	StorageKind          = internal.StorageKind
	ServerSideEncryption = internal.ServerSideEncryption
	SseType              = internal.SseType
	EventSourceConfig    = internal.EventSourceConfig
	EventTarget          = internal.EventTarget

	CreateBackendRequest     = internal.CreateBackendRequest
	GetBackendRequest        = internal.GetBackendRequest
	UpdateBackendRequest     = internal.UpdateBackendRequest
	DeleteBackendRequest     = internal.DeleteBackendRequest
	DeleteBackendResponse    = internal.DeleteBackendResponse
	ListBackendsRequest      = internal.ListBackendsRequest
	ListBackendsResponse     = internal.ListBackendsResponse
	RotateCredentialsRequest = internal.RotateCredentialsRequest
	TestBackendRequest       = internal.TestBackendRequest
	TestBackendResponse      = internal.TestBackendResponse
)

const (
	StorageKind_AWS_S3        = internal.StorageKind_STORAGE_KIND_AWS_S3
	StorageKind_S3_COMPATIBLE = internal.StorageKind_STORAGE_KIND_S3_COMPATIBLE
	StorageKind_GCS           = internal.StorageKind_STORAGE_KIND_GCS

	SseType_NONE   = internal.SseType_SSE_TYPE_NONE
	SseType_AES256 = internal.SseType_SSE_TYPE_AES256
	SseType_KMS    = internal.SseType_SSE_TYPE_KMS

	EventTarget_NONE  = internal.EventTarget_EVENT_TARGET_NONE
	EventTarget_SQS   = internal.EventTarget_EVENT_TARGET_SQS
	EventTarget_REDIS = internal.EventTarget_EVENT_TARGET_REDIS
)

// ─── Bucket ─────────────────────────────────────────────────────────────────

type (
	Bucket              = internal.Bucket
	BucketConstraints   = internal.BucketConstraints
	BucketVersioning    = internal.BucketVersioning
	BucketReplication   = internal.BucketReplication
	ObjectLockConfig    = internal.ObjectLockConfig
	ObjectLockMode      = internal.ObjectLockMode
	LifecycleRule       = internal.LifecycleRule
	LifecycleTransition = internal.LifecycleTransition
	LifecycleExpiration = internal.LifecycleExpiration

	CreateBucketRequest          = internal.CreateBucketRequest
	GetBucketRequest             = internal.GetBucketRequest
	UpdateBucketRequest          = internal.UpdateBucketRequest
	DeleteBucketRequest          = internal.DeleteBucketRequest
	DeleteBucketResponse         = internal.DeleteBucketResponse
	ListBucketsRequest           = internal.ListBucketsRequest
	ListBucketsResponse          = internal.ListBucketsResponse
	ListAccessibleBucketsRequest = internal.ListAccessibleBucketsRequest
	SetBucketPolicyRequest       = internal.SetBucketPolicyRequest
	SetLifecycleRulesRequest     = internal.SetLifecycleRulesRequest
	SetObjectLockRequest         = internal.SetObjectLockRequest
	SetVersioningRequest         = internal.SetVersioningRequest
	SetReplicationRequest        = internal.SetReplicationRequest
)

const (
	ObjectLockMode_GOVERNANCE = internal.ObjectLockMode_OBJECT_LOCK_MODE_GOVERNANCE
	ObjectLockMode_COMPLIANCE = internal.ObjectLockMode_OBJECT_LOCK_MODE_COMPLIANCE
)

// ─── Tenant / Collection ─────────────────────────────────────────────────────

type (
	Tenant                    = internal.Tenant
	Collection                = internal.Collection
	CreateTenantRequest       = internal.CreateTenantRequest
	GetTenantRequest          = internal.GetTenantRequest
	UpdateTenantRequest       = internal.UpdateTenantRequest
	DeleteTenantRequest       = internal.DeleteTenantRequest
	DeleteTenantResponse      = internal.DeleteTenantResponse
	ListTenantsRequest        = internal.ListTenantsRequest
	ListTenantsResponse       = internal.ListTenantsResponse
	SetInheritedPolicyRequest = internal.SetInheritedPolicyRequest

	CreateCollectionRequest       = internal.CreateCollectionRequest
	GetCollectionRequest          = internal.GetCollectionRequest
	UpdateCollectionRequest       = internal.UpdateCollectionRequest
	DeleteCollectionRequest       = internal.DeleteCollectionRequest
	DeleteCollectionResponse      = internal.DeleteCollectionResponse
	ListCollectionsRequest        = internal.ListCollectionsRequest
	ListCollectionsResponse       = internal.ListCollectionsResponse
	SetCollectionPolicyRequest    = internal.SetCollectionPolicyRequest
	BindCollectionToBucketRequest = internal.BindCollectionToBucketRequest
)

// ─── Operation / Audit / Quota / EventSubscription ──────────────────────────

type (
	Operation              = internal.Operation
	GetOperationRequest    = internal.GetOperationRequest
	ListOperationsRequest  = internal.ListOperationsRequest
	ListOperationsResponse = internal.ListOperationsResponse
	CancelOperationRequest = internal.CancelOperationRequest

	AuditLogEntry           = internal.AuditLogEntry
	ListAuditLogRequest     = internal.ListAuditLogRequest
	ListAuditLogResponse    = internal.ListAuditLogResponse
	GetAuditLogEntryRequest = internal.GetAuditLogEntryRequest
	ExportAuditLogRequest   = internal.ExportAuditLogRequest

	Quota             = internal.Quota
	QuotaUsage        = internal.QuotaUsage
	GetQuotaRequest   = internal.GetQuotaRequest
	SetQuotaRequest   = internal.SetQuotaRequest
	ResetUsageRequest = internal.ResetUsageRequest

	EventSubscription          = internal.EventSubscription
	EventSink                  = internal.EventSink
	HttpSink                   = internal.HttpSink
	KafkaSink                  = internal.KafkaSink
	SqsSink                    = internal.SqsSink
	CreateSubscriptionRequest  = internal.CreateSubscriptionRequest
	GetSubscriptionRequest     = internal.GetSubscriptionRequest
	UpdateSubscriptionRequest  = internal.UpdateSubscriptionRequest
	DeleteSubscriptionRequest  = internal.DeleteSubscriptionRequest
	DeleteSubscriptionResponse = internal.DeleteSubscriptionResponse
	ListSubscriptionsRequest   = internal.ListSubscriptionsRequest
	ListSubscriptionsResponse  = internal.ListSubscriptionsResponse
	TestSubscriptionRequest    = internal.TestSubscriptionRequest
	TestSubscriptionResponse   = internal.TestSubscriptionResponse
)

// ─── Policy ─────────────────────────────────────────────────────────────────

type (
	ValidateRequest            = internal.ValidateRequest
	ValidateResponse           = internal.ValidateResponse
	PolicyDiagnostic           = internal.PolicyDiagnostic
	SimulateAuthzRequest       = internal.SimulateAuthzRequest
	SimulateAuthzResponse      = internal.SimulateAuthzResponse
	GetEffectivePolicyRequest  = internal.GetEffectivePolicyRequest
	GetEffectivePolicyResponse = internal.GetEffectivePolicyResponse
	PolicyLayer                = internal.PolicyLayer
)
