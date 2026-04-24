// Package paladinv1connect re-exports the Connect RPC clients and handlers from
// internal/api/pb/v1/paladinv1connect so downstream Go modules can consume them
// without violating the Go internal-package rule.
package paladinv1connect

import internal "github.com/oleg-tkachuk/paladin/internal/api/pb/v1/paladinv1connect"

// ─── ObjectService ───────────────────────────────────────────────────────────

type ObjectServiceClient = internal.ObjectServiceClient

var NewObjectServiceClient = internal.NewObjectServiceClient

type ObjectServiceHandler = internal.ObjectServiceHandler

var NewObjectServiceHandler = internal.NewObjectServiceHandler

// ─── PresignService ──────────────────────────────────────────────────────────

type PresignServiceClient = internal.PresignServiceClient

var NewPresignServiceClient = internal.NewPresignServiceClient

type PresignServiceHandler = internal.PresignServiceHandler

var NewPresignServiceHandler = internal.NewPresignServiceHandler

// ─── CategoryService ─────────────────────────────────────────────────────────

type CategoryServiceClient = internal.CategoryServiceClient

var NewCategoryServiceClient = internal.NewCategoryServiceClient

type CategoryServiceHandler = internal.CategoryServiceHandler

var NewCategoryServiceHandler = internal.NewCategoryServiceHandler

// ─── MultipartUploadService ──────────────────────────────────────────────────

type MultipartUploadServiceClient = internal.MultipartUploadServiceClient

var NewMultipartUploadServiceClient = internal.NewMultipartUploadServiceClient

type MultipartUploadServiceHandler = internal.MultipartUploadServiceHandler

var NewMultipartUploadServiceHandler = internal.NewMultipartUploadServiceHandler

// ─── BucketService ───────────────────────────────────────────────────────────

type BucketServiceClient = internal.BucketServiceClient

var NewBucketServiceClient = internal.NewBucketServiceClient

type BucketServiceHandler = internal.BucketServiceHandler

var NewBucketServiceHandler = internal.NewBucketServiceHandler

// ─── TenantService ───────────────────────────────────────────────────────────

type TenantServiceClient = internal.TenantServiceClient

var NewTenantServiceClient = internal.NewTenantServiceClient

type TenantServiceHandler = internal.TenantServiceHandler

var NewTenantServiceHandler = internal.NewTenantServiceHandler

// ─── OperationService ────────────────────────────────────────────────────────

type OperationServiceClient = internal.OperationServiceClient

var NewOperationServiceClient = internal.NewOperationServiceClient

type OperationServiceHandler = internal.OperationServiceHandler

var NewOperationServiceHandler = internal.NewOperationServiceHandler

// ─── BatchService ────────────────────────────────────────────────────────────

type BatchServiceClient = internal.BatchServiceClient

var NewBatchServiceClient = internal.NewBatchServiceClient

type BatchServiceHandler = internal.BatchServiceHandler

var NewBatchServiceHandler = internal.NewBatchServiceHandler

// ─── PolicyService ───────────────────────────────────────────────────────────

type PolicyServiceClient = internal.PolicyServiceClient

var NewPolicyServiceClient = internal.NewPolicyServiceClient

type PolicyServiceHandler = internal.PolicyServiceHandler

var NewPolicyServiceHandler = internal.NewPolicyServiceHandler

// ─── SystemService ───────────────────────────────────────────────────────────

type SystemServiceClient = internal.SystemServiceClient

var NewSystemServiceClient = internal.NewSystemServiceClient

type SystemServiceHandler = internal.SystemServiceHandler

var NewSystemServiceHandler = internal.NewSystemServiceHandler
