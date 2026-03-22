// Package connectapi re-exports the protobuf-generated types and Connect RPC
// service clients, making them available to external consumers of this module.
//
// The generated code lives in internal/api/connect/paladinapi/paladinapiconnect (driven by the
// protoc-gen-connect-go plugin). This thin wrapper lets downstream Go modules
// import the same types without violating the Go internal-package rule.
package connectapi

import (
	internal "github.com/oleg-tkachuk/paladin/internal/api/connect/paladinapi/paladinapiconnect"
)

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

// ─── BulkService ─────────────────────────────────────────────────────────────

type BulkServiceClient = internal.BulkServiceClient

var NewBulkServiceClient = internal.NewBulkServiceClient

type BulkServiceHandler = internal.BulkServiceHandler

var NewBulkServiceHandler = internal.NewBulkServiceHandler

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
