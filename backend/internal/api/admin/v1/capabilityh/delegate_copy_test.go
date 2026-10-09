package capabilityh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// A Biscuit copy narrowed offline arrives as a Capability narrower than the
// stored record. Delegating from it narrows from what was presented: a child
// cannot get back an op the copy gave up.
func TestDelegate_CapabilityPath_NarrowsFromThePresentedCopy(t *testing.T) {
	record := mkParent(uuid.New(), limes.OpGet, limes.OpPut, limes.OpShare)
	store := &fakeStore{cap: &record}
	h := NewHandler(mkIssuer(t, store), store, nil, &denyAuthorizer{})

	copied := record
	copied.Caveats.Ops = []limes.Op{limes.OpGet, limes.OpShare} // put dropped offline
	ctx := auth.WithCapability(context.Background(), &copied)

	_, err := h.Delegate(ctx, &adminv1.CapabilityServiceDelegateRequest{
		ParentId:   record.ID.String(),
		TtlSeconds: 60,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(limes.OpGet), string(limes.OpPut)}},
	})
	if codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a child regained put the copy gave up: code = %v (%v)", codeOf(err), err)
	}

	resp, err := h.Delegate(ctx, &adminv1.CapabilityServiceDelegateRequest{
		ParentId:   record.ID.String(),
		TtlSeconds: 60,
	})
	if err != nil {
		t.Fatalf("inheriting delegate: %v", err)
	}
	if got := resp.GetCapability().GetCaveats().GetOps(); len(got) != len(copied.Caveats.Ops) {
		t.Fatalf("inherited ops = %v, want the copy's %v", got, copied.Caveats.Ops)
	}
}

// A copy with limits of its own cannot delegate: the child's use would reach
// the capability's counters and never the copy's.
func TestDelegate_CapabilityPath_RefusesACopyWithLimits(t *testing.T) {
	record := mkParent(uuid.New(), limes.OpGet, limes.OpShare)
	store := &fakeStore{cap: &record}
	h := NewHandler(mkIssuer(t, store), store, nil, &denyAuthorizer{})

	copied := record
	copied.Copies = []limes.CopyCeiling{{RevocationID: []byte("copy"), MaxRequests: 1}}
	_, err := h.Delegate(auth.WithCapability(context.Background(), &copied),
		&adminv1.CapabilityServiceDelegateRequest{ParentId: record.ID.String(), TtlSeconds: 60})
	if codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (%v)", codeOf(err), err)
	}
}
