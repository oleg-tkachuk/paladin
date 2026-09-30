// Package datav1connect re-exports Connect server/client constructors for
// paladin.data.v1 services.
package datav1connect

import internal "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/data/v1/paladindatav1connect"

type (
	ObjectServiceHandler           = internal.ObjectServiceHandler
	MultipartUploadServiceHandler  = internal.MultipartUploadServiceHandler
	PresignServiceHandler          = internal.PresignServiceHandler
	ObjectTagServiceHandler        = internal.ObjectTagServiceHandler
	BatchServiceHandler            = internal.BatchServiceHandler
	OperationServiceHandler        = internal.OperationServiceHandler
	StorageBootstrapServiceHandler = internal.StorageBootstrapServiceHandler
)

var (
	NewObjectServiceClient           = internal.NewObjectServiceClient
	NewMultipartUploadServiceClient  = internal.NewMultipartUploadServiceClient
	NewPresignServiceClient          = internal.NewPresignServiceClient
	NewObjectTagServiceClient        = internal.NewObjectTagServiceClient
	NewBatchServiceClient            = internal.NewBatchServiceClient
	NewOperationServiceClient        = internal.NewOperationServiceClient
	NewStorageBootstrapServiceClient = internal.NewStorageBootstrapServiceClient
)
