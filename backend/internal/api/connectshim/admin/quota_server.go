package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/quotah"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1/paladinadminv1connect"
)

type QuotaServer struct {
	paladinadminv1connect.UnimplementedQuotaServiceHandler
	H quotaHandler
}

func NewQuotaServer(h *quotah.Handler) *QuotaServer { return &QuotaServer{H: h} }

func (s *QuotaServer) GetQuota(ctx context.Context, req *connect.Request[pb.GetQuotaRequest]) (*connect.Response[pb.Quota], error) {
	scope, err := parseQuotaName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var q *admindomain.Quota
	if scope.tenantID != uuid.Nil {
		q, err = s.H.GetTenantQuota(ctx, scope.tenantID)
	} else {
		q, err = s.H.GetBucketQuota(ctx, scope.backendID, scope.bucketName)
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(quotaToProto(q)), nil
}

func (s *QuotaServer) SetQuota(ctx context.Context, req *connect.Request[pb.SetQuotaRequest]) (*connect.Response[pb.Quota], error) {
	m := req.Msg
	scope, err := parseQuotaName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	src := m.GetQuota()
	q := admindomain.Quota{
		ResourceVersion:  rv,
		TenantID:         scope.tenantID,
		BackendID:        scope.backendID,
		BucketName:       scope.bucketName,
		MaxTotalBytes:    src.GetMaxTotalBytes(),
		MaxObjectCount:   src.GetMaxObjectCount(),
		MaxBytesPerDay:   src.GetMaxBytesPerDay(),
		MaxObjectsPerDay: src.GetMaxObjectsPerDay(),
	}
	out, err := s.H.SetQuota(ctx, q, m.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(quotaToProto(out)), nil
}

func (s *QuotaServer) ResetUsage(ctx context.Context, req *connect.Request[pb.ResetUsageRequest]) (*connect.Response[pb.Quota], error) {
	scope, err := parseQuotaName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Need quota_id to reset; load first.
	var q *admindomain.Quota
	if scope.tenantID != uuid.Nil {
		q, err = s.H.GetTenantQuota(ctx, scope.tenantID)
	} else {
		q, err = s.H.GetBucketQuota(ctx, scope.backendID, scope.bucketName)
	}
	if err != nil {
		return nil, err
	}
	if err := s.H.ResetUsage(ctx, q.QuotaID); err != nil {
		return nil, err
	}
	return connect.NewResponse(quotaToProto(q)), nil
}

var _ paladinadminv1connect.QuotaServiceHandler = (*QuotaServer)(nil)

// quotaScope decodes either form of Quota.name.
type quotaScope struct {
	tenantID   uuid.UUID
	backendID  string
	bucketName string
}

func parseQuotaName(name string) (quotaScope, error) {
	parts := strings.Split(name, "/")
	switch {
	case len(parts) == 3 && parts[0] == "tenants" && parts[2] == "quota":
		id, err := uuid.Parse(parts[1])
		if err != nil {
			return quotaScope{}, fmt.Errorf("invalid tenant id in %q: %w", name, err)
		}
		return quotaScope{tenantID: id}, nil
	case len(parts) == 5 && parts[0] == "storageBackends" && parts[2] == "buckets" && parts[4] == "quota":
		return quotaScope{backendID: parts[1], bucketName: parts[3]}, nil
	}
	return quotaScope{}, fmt.Errorf("invalid quota name %q", name)
}
