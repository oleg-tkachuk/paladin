// Package grpcapi re-exports the protobuf-generated types and gRPC service
// clients, making them available to external consumers of this module.
//
// The generated code lives in internal/api/grpc (driven by the proto
// go_package option).  This thin wrapper lets downstream Go modules import
// the same types without violating the Go internal-package rule.
package grpcapi

import (
	internal "github.com/oleg-tkachuk/paladin/internal/api/grpc"
)

// ─── Type aliases ────────────────────────────────────────────────────────────

// Enums
type ObjectStatus = internal.ObjectStatus

const (
	ObjectStatus_OBJECT_STATUS_UNSPECIFIED = internal.ObjectStatus_OBJECT_STATUS_UNSPECIFIED
	ObjectStatus_OBJECT_STATUS_PENDING     = internal.ObjectStatus_OBJECT_STATUS_PENDING
	ObjectStatus_OBJECT_STATUS_UPLOADING   = internal.ObjectStatus_OBJECT_STATUS_UPLOADING
	ObjectStatus_OBJECT_STATUS_AVAILABLE   = internal.ObjectStatus_OBJECT_STATUS_AVAILABLE
	ObjectStatus_OBJECT_STATUS_ARCHIVED    = internal.ObjectStatus_OBJECT_STATUS_ARCHIVED
	ObjectStatus_OBJECT_STATUS_DELETED     = internal.ObjectStatus_OBJECT_STATUS_DELETED
)

type ChecksumAlgorithm = internal.ChecksumAlgorithm
type SortOrder = internal.SortOrder
type Permission = internal.Permission

// Messages
type Object = internal.Object
type ObjectIdentifier = internal.ObjectIdentifier
type PresignedUrl = internal.PresignedUrl
type ObjectFilter = internal.ObjectFilter
type Category = internal.Category
type CompletedPart = internal.CompletedPart

// ─── ObjectService ───────────────────────────────────────────────────────────

type ObjectServiceClient = internal.ObjectServiceClient

var NewObjectServiceClient = internal.NewObjectServiceClient

type UploadObjectRequest = internal.UploadObjectRequest
type UploadObjectResponse = internal.UploadObjectResponse
type GetObjectMetadataRequest = internal.GetObjectMetadataRequest
type GetObjectMetadataResponse = internal.GetObjectMetadataResponse
type DownloadObjectRequest = internal.DownloadObjectRequest
type DownloadObjectResponse = internal.DownloadObjectResponse
type CompleteObjectRequest = internal.CompleteObjectRequest
type CompleteObjectResponse = internal.CompleteObjectResponse
type DeleteObjectRequest = internal.DeleteObjectRequest
type DeleteObjectResponse = internal.DeleteObjectResponse
type ListObjectsRequest = internal.ListObjectsRequest
type ListObjectsResponse = internal.ListObjectsResponse

// ─── PresignService ──────────────────────────────────────────────────────────

type PresignServiceClient = internal.PresignServiceClient

var NewPresignServiceClient = internal.NewPresignServiceClient

type GenerateUploadUrlRequest = internal.GenerateUploadUrlRequest
type GenerateUploadUrlResponse = internal.GenerateUploadUrlResponse
type GenerateDownloadUrlRequest = internal.GenerateDownloadUrlRequest
type GenerateDownloadUrlResponse = internal.GenerateDownloadUrlResponse

// ─── CategoryService ─────────────────────────────────────────────────────────

type CategoryServiceClient = internal.CategoryServiceClient

var NewCategoryServiceClient = internal.NewCategoryServiceClient

type CreateCategoryRequest = internal.CreateCategoryRequest
type CreateCategoryResponse = internal.CreateCategoryResponse
type GetCategoryRequest = internal.GetCategoryRequest
type GetCategoryResponse = internal.GetCategoryResponse
type ListCategoriesRequest = internal.ListCategoriesRequest
type ListCategoriesResponse = internal.ListCategoriesResponse
type UpdateCategoryRequest = internal.UpdateCategoryRequest
type UpdateCategoryResponse = internal.UpdateCategoryResponse
type DeleteCategoryRequest = internal.DeleteCategoryRequest
type DeleteCategoryResponse = internal.DeleteCategoryResponse

// ─── MultipartUploadService ──────────────────────────────────────────────────

type MultipartUploadServiceClient = internal.MultipartUploadServiceClient

var NewMultipartUploadServiceClient = internal.NewMultipartUploadServiceClient

type InitiateMultipartUploadRequest = internal.InitiateMultipartUploadRequest
type InitiateMultipartUploadResponse = internal.InitiateMultipartUploadResponse
type CompleteMultipartUploadRequest = internal.CompleteMultipartUploadRequest
type CompleteMultipartUploadResponse = internal.CompleteMultipartUploadResponse
