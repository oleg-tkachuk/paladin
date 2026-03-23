package paladinapi

import (
	"context"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/utils"
)

// BucketHandler implements paladinapiconnect.BucketServiceHandler.
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
	msg := req.Msg
	authorizedTenantID := utils.TenantIDFromContext(ctx, "")
	requestedTenantID := msg.TenantId

	if requestedTenantID == "" {
		requestedTenantID = authorizedTenantID
	}

	// Authorization check: only SystemAdmin, DefaultTenant (local dev), or the tenant themselves.
	if authorizedTenantID != "" &&
		authorizedTenantID != utils.SystemAdminTenant &&
		authorizedTenantID != utils.DefaultTenant &&
		authorizedTenantID != requestedTenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("cannot access statistics of other tenants"))
	}

	objects, size, err := h.repo.GetBucketStats(ctx, requestedTenantID, msg.Name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&GetBucketStatsResponse{
		TotalObjects:   objects,
		TotalSizeBytes: size,
	}), nil
}
