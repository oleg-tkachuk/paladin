package grpcapi

import (
	"context"

	"connectrpc.com/connect"
)

// BucketHandler implements grpcapiconnect.BucketServiceHandler.
// All RPCs return Unimplemented — bucket management requires a new domain service.
type BucketHandler struct{}

// NewBucketHandler creates a new BucketHandler.
func NewBucketHandler() *BucketHandler {
	return &BucketHandler{}
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
