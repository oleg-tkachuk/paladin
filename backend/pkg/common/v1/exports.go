// Package commonv1 re-exports the shared types under paladin.common.v1.
package commonv1

import internal "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/common/v1"

type (
	PageRequest         = internal.PageRequest
	PageResponse        = internal.PageResponse
	SortOrder           = internal.SortOrder
	Scope               = internal.Scope
	ScopeType           = internal.ScopeType
	PresignedUrl        = internal.PresignedUrl
	PresignedPostPolicy = internal.PresignedPostPolicy
	CompletionMode      = internal.CompletionMode
	ChecksumAlgorithm   = internal.ChecksumAlgorithm
)

const (
	SortOrder_ASC  = internal.SortOrder_SORT_ORDER_ASC
	SortOrder_DESC = internal.SortOrder_SORT_ORDER_DESC

	ScopeType_TENANT     = internal.ScopeType_SCOPE_TYPE_TENANT
	ScopeType_BACKEND    = internal.ScopeType_SCOPE_TYPE_BACKEND
	ScopeType_BUCKET     = internal.ScopeType_SCOPE_TYPE_BUCKET
	ScopeType_OBJECT_KEY = internal.ScopeType_SCOPE_TYPE_OBJECT_KEY

	CompletionMode_IMPLICIT = internal.CompletionMode_COMPLETION_MODE_IMPLICIT
	CompletionMode_EXPLICIT = internal.CompletionMode_COMPLETION_MODE_EXPLICIT

	ChecksumAlgorithm_CRC32C = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_CRC32C
	ChecksumAlgorithm_SHA256 = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256
	ChecksumAlgorithm_MD5    = internal.ChecksumAlgorithm_CHECKSUM_ALGORITHM_MD5
)
