package grpcapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/domain"
)

// BucketHandler implements grpcapiconnect.BucketServiceHandler.
type BucketHandler struct {
	log  *zap.Logger
	repo domain.ObjectsService
}

// NewBucketHandler creates a new BucketHandler.
func NewBucketHandler(log *zap.Logger, repo domain.ObjectsService) *BucketHandler {
	return &BucketHandler{
		log:  log,
		repo: repo,
	}
}

func (h *BucketHandler) CreateBucket(ctx context.Context, req *connect.Request[CreateBucketRequest]) (*connect.Response[CreateBucketResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) DeleteBucket(ctx context.Context, req *connect.Request[DeleteBucketRequest]) (*connect.Response[DeleteBucketResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) ListBuckets(ctx context.Context, req *connect.Request[ListBucketsRequest]) (*connect.Response[ListBucketsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) GetBucketConfiguration(ctx context.Context, req *connect.Request[GetBucketConfigurationRequest]) (*connect.Response[GetBucketConfigurationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) UpdateBucketConfiguration(ctx context.Context, req *connect.Request[UpdateBucketConfigurationRequest]) (*connect.Response[UpdateBucketConfigurationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (h *BucketHandler) GetBucketStats(ctx context.Context, req *connect.Request[GetBucketStatsRequest]) (*connect.Response[GetBucketStatsResponse], error) {
	objects, size, err := h.repo.GetBucketStats(ctx, req.Msg.TenantId, req.Msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&GetBucketStatsResponse{
		TotalObjects:   objects,
		TotalSizeBytes: size,
	}), nil
}
