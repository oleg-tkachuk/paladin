package admin

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/systemh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1/paladinadminv1connect"
	"github.com/oleg-tkachuk/paladin/internal/platformstats"
)

// SystemServer adapts *systemh.Handler to the generated Connect
// interface. Authn/role gating lives inside the handler so the
// connectshim coverage test sees a gated call site.
type SystemServer struct {
	paladinadminv1connect.UnimplementedSystemServiceHandler
	H systemHandler
}

func NewSystemServer(h *systemh.Handler) *SystemServer {
	// H is an interface, so a nil *systemh.Handler assigned straight into it
	// would produce a NON-nil interface value and the `s.H == nil`
	// disabled-subsystem checks below would silently stop firing — the
	// RPCs would panic on a nil receiver instead of answering
	// CodeUnavailable. Leave the field zero instead.
	if h == nil {
		return &SystemServer{}
	}
	return &SystemServer{H: h}
}

func (s *SystemServer) GetConfig(ctx context.Context, _ *connect.Request[pb.GetConfigRequest]) (*connect.Response[pb.GetConfigResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system handler not wired"))
	}
	yamlBlob, sourcePath, err := s.H.MarshalRedacted(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetConfigResponse{
		Yaml:       yamlBlob,
		SourcePath: sourcePath,
	}), nil
}

func (s *SystemServer) GetDispatcherStats(ctx context.Context, _ *connect.Request[pb.GetDispatcherStatsRequest]) (*connect.Response[pb.GetDispatcherStatsResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system handler not wired"))
	}
	stats, available, err := s.H.DispatcherStats(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.GetDispatcherStatsResponse{Available: available}
	if stats != nil {
		out.Pending = stats.Pending
		out.Failed = stats.Failed
		out.OldestPendingSeconds = stats.OldestPendingSeconds
		out.Subscriptions = make([]*pb.SubscriptionDeliveryStat, len(stats.Subscriptions))
		for i, sub := range stats.Subscriptions {
			out.Subscriptions[i] = &pb.SubscriptionDeliveryStat{
				SubscriptionId: sub.SubscriptionID,
				TenantId:       sub.TenantID,
				Pending:        sub.Pending,
				Failed:         sub.Failed,
				LastError:      sub.LastError,
				LastStatusCode: sub.LastStatusCode,
				LastAttemptAt:  sub.LastAttemptAt,
			}
		}
	}
	return connect.NewResponse(out), nil
}

func (s *SystemServer) GetPlatformStats(ctx context.Context, _ *connect.Request[pb.GetPlatformStatsRequest]) (*connect.Response[pb.GetPlatformStatsResponse], error) {
	if s.H == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("system handler not wired"))
	}
	res, err := s.H.PlatformStats(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.GetPlatformStatsResponse{
		Tenants:     &pb.TenantStats{},
		Backends:    &pb.BackendStats{},
		Buckets:     &pb.BucketStats{},
		Collections: &pb.CollectionStats{},
		Users:       &pb.UserStats{},
		Rls: &pb.RLSStats{
			Available:     res.RLSAvailable,
			Objects:       &pb.ObjectStats{},
			Quotas:        &pb.QuotaStats{},
			Capabilities:  &pb.CapabilityStats{},
			ApiTokens:     &pb.APITokenStats{},
			Subscriptions: &pb.SubscriptionStats{},
		},
		CollectedAt: timestamppb.Now(),
	}
	if cp := res.ControlPlane; cp != nil {
		out.Tenants = &pb.TenantStats{
			Total:                 cp.Tenants.Total,
			Active:                cp.Tenants.Active,
			Trashed:               cp.Tenants.Trashed,
			SharedLayout:          cp.Tenants.SharedLayout,
			DedicatedLayout:       cp.Tenants.DedicatedLayout,
			WithoutDefaultBinding: cp.Tenants.WithoutDefaultBinding,
		}
		out.Backends = &pb.BackendStats{
			Total:       cp.Backends.Total,
			Enabled:     cp.Backends.Enabled,
			Disabled:    cp.Backends.Disabled,
			ReadOnly:    cp.Backends.ReadOnly,
			Maintenance: cp.Backends.Maintenance,
			ByKind:      cp.Backends.ByKind,
		}
		out.Buckets = &pb.BucketStats{
			Total:              cp.Buckets.Total,
			ByProvisionState:   cp.Buckets.ByProvisionState,
			ByBackend:          cp.Buckets.ByBackend,
			TenantOwned:        cp.Buckets.TenantOwned,
			Shared:             cp.Buckets.Shared,
			VersioningEnabled:  cp.Buckets.VersioningEnabled,
			ObjectLockEnabled:  cp.Buckets.ObjectLockEnabled,
			ReplicationEnabled: cp.Buckets.ReplicationOn,
		}
		out.Collections = &pb.CollectionStats{
			Total:     cp.Collections.Total,
			ByBackend: cp.Collections.ByBackend,
			Unbound:   cp.Collections.Unbound,
		}
		out.Users = &pb.UserStats{Total: cp.Users.Total, Disabled: cp.Users.Disabled}
	}
	if r := res.RLS; r != nil {
		oc := r.Objects
		out.Rls.Objects.States = statesToPB(oc.States)
		out.Rls.Objects.TotalCount = oc.TotalCount
		out.Rls.Objects.TotalBytes = oc.TotalBytes
		out.Rls.Objects.TenantsTruncated = oc.TenantsCut
		out.Rls.Objects.Tenants = make([]*pb.TenantObjectStats, len(oc.Tenants))
		for i, t := range oc.Tenants {
			name := res.TenantNames[t.TenantID]
			out.Rls.Objects.Tenants[i] = &pb.TenantObjectStats{
				TenantId:    t.TenantID,
				Slug:        name.Slug,
				DisplayName: name.DisplayName,
				States:      statesToPB(t.States),
				TotalCount:  t.TotalCount,
				TotalBytes:  t.TotalBytes,
			}
		}
		out.Rls.Quotas = &pb.QuotaStats{
			Total:            r.Quotas.Total,
			TenantScoped:     r.Quotas.TenantScoped,
			BucketScoped:     r.Quotas.BucketScoped,
			WithLimits:       r.Quotas.WithLimits,
			AtLimit:          r.Quotas.AtLimit,
			NearLimit:        r.Quotas.NearLimit,
			UsageObjectCount: r.Quotas.UsageObjectCount,
			UsageTotalBytes:  r.Quotas.UsageTotalBytes,
		}
		out.Rls.Capabilities = &pb.CapabilityStats{
			Total:           r.Capabilities.Total,
			Active:          r.Capabilities.Active,
			Expired:         r.Capabilities.Expired,
			Revoked:         r.Capabilities.Revoked,
			Delegated:       r.Capabilities.Delegated,
			ExpiringSoon:    r.Capabilities.ExpiringSoon,
			ByPrincipalKind: r.Capabilities.ByPrincipalKind,
		}
		out.Rls.ApiTokens = &pb.APITokenStats{
			Total:        r.APITokens.Total,
			Active:       r.APITokens.Active,
			Expired:      r.APITokens.Expired,
			Revoked:      r.APITokens.Revoked,
			ExpiringSoon: r.APITokens.ExpiringSoon,
			NeverUsed:    r.APITokens.NeverUsed,
		}
		out.Rls.Subscriptions = &pb.SubscriptionStats{
			Total:      r.Subscriptions.Total,
			Enabled:    r.Subscriptions.Enabled,
			Disabled:   r.Subscriptions.Disabled,
			WithFilter: r.Subscriptions.WithFilter,
			BySinkKind: r.Subscriptions.BySinkKind,
		}
	}
	return connect.NewResponse(out), nil
}

func statesToPB(in []platformstats.StateStat) []*pb.ObjectStateStat {
	out := make([]*pb.ObjectStateStat, len(in))
	for i, s := range in {
		out[i] = &pb.ObjectStateStat{State: s.State, Count: s.Count, Bytes: s.Bytes}
	}
	return out
}
