package admin

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// BillingServer and SystemServer both treat a nil handler as "subsystem
// not wired" and answer CodeUnavailable. Once H became an interface that
// contract acquired a trap: assigning a nil *billingh.Handler into an
// interface field yields a NON-nil interface, so `s.H == nil` reads false
// and the RPC panics on a nil receiver instead of refusing politely. The
// constructors leave the field zero to avoid it; these tests are what
// notices if that guard is ever dropped.
//
// The assertion is deliberately end-to-end through the RPC rather than a
// `srv.H == nil` field check — the field being nil is the mechanism, the
// CodeUnavailable answer is the contract.

func assertUnavailable(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error from a nil-handler server, got success", name)
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) {
		t.Fatalf("%s: expected a *connect.Error, got %v", name, err)
	}
	if connErr.Code() != connect.CodeUnavailable {
		t.Errorf("%s: expected CodeUnavailable, got %v (%v)", name, connErr.Code(), err)
	}
}

func TestBillingServer_NilHandler_Unavailable(t *testing.T) {
	srv := NewBillingServer(nil)
	ctx := context.Background()

	// A real tenant id, not the zero value: both RPCs parse tenant_id, and
	// an unparseable one is refused with InvalidArgument before the nil
	// check is reached. That would still fail this test if the guard were
	// dropped, but for the wrong reason — the request would never get far
	// enough to touch the nil handler. With a valid id, dropping the guard
	// means a nil-receiver panic, which is the failure actually being
	// guarded against.
	tenantID := uuid.New().String()

	_, err := srv.GetTenantSummary(ctx, &pb.GetTenantSummaryRequest{
		TenantId: tenantID,
	})
	assertUnavailable(t, "GetTenantSummary", err)

	_, err = srv.GetTenantTimeSeries(ctx, &pb.GetTenantTimeSeriesRequest{
		TenantId: tenantID,
	})
	assertUnavailable(t, "GetTenantTimeSeries", err)
}

func TestSystemServer_NilHandler_Unavailable(t *testing.T) {
	srv := NewSystemServer(nil)
	ctx := context.Background()

	_, err := srv.GetConfig(ctx, &pb.GetConfigRequest{})
	assertUnavailable(t, "GetConfig", err)

	_, err = srv.GetDispatcherStats(ctx, &pb.GetDispatcherStatsRequest{})
	assertUnavailable(t, "GetDispatcherStats", err)

	_, err = srv.GetPlatformStats(ctx, &pb.GetPlatformStatsRequest{})
	assertUnavailable(t, "GetPlatformStats", err)

	_, err = srv.ListPlatformStatsTenants(ctx, &pb.ListPlatformStatsTenantsRequest{
		Signal: pb.PlatformStatsSignal_PLATFORM_STATS_SIGNAL_QUOTA_AT_LIMIT,
	})
	assertUnavailable(t, "ListPlatformStatsTenants", err)
}
