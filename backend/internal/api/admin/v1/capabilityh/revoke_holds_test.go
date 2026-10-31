package capabilityh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeHoldReleaser records each release it is asked for.
type fakeHoldReleaser struct {
	tenants, capabilities []uuid.UUID
	err                   error
}

func (f *fakeHoldReleaser) ReleaseRevoked(_ context.Context, tenant, capability uuid.UUID) (int64, error) {
	f.tenants = append(f.tenants, tenant)
	f.capabilities = append(f.capabilities, capability)
	return 1, f.err
}

// Revoke releases the revoked capability's holds in its owner's tenant —
// across tenants for an admin, in its own for a caller confined to it — and
// again on a repeat, which finishes a release that failed.
func TestRevokeReleasesTheCapabilitysHolds(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, limes.OpGet)
	cases := map[string]struct {
		caller context.Context
		result limes.RevokeResult
	}{
		"platform admin":         {callerCtx(uuid.New(), "platform.admin"), limes.RevokeResult{}},
		"tenant-confined caller": {callerCtx(owner), limes.RevokeResult{}},
		"a repeat":               {callerCtx(owner), limes.RevokeResult{AlreadyRevoked: true}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}, revokeResult: tc.result}}
			holds := &fakeHoldReleaser{}
			h := mkHandler(t, store, nil, &allowAuthorizer{}).WithHoldRelease(holds)
			if _, err := h.Revoke(withSlot(tc.caller), &adminv1.CapabilityServiceRevokeRequest{Id: target.ID.String()}); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if len(holds.capabilities) != 1 || holds.capabilities[0] != target.ID || holds.tenants[0] != owner {
				t.Errorf("released %v in %v, want %s in %s", holds.capabilities, holds.tenants, target.ID, owner)
			}
		})
	}
}

// A failed release fails the revoke, so the caller retries it; a revoke the
// store refused, or the caller may not make, releases nothing.
func TestRevokeHoldReleaseFailures(t *testing.T) {
	errStore := errors.New("pgstore: connection refused")
	holds := &fakeHoldReleaser{err: errStore}
	h := mkHandler(t, &recordingStore{}, nil, &allowAuthorizer{}).WithHoldRelease(holds)
	if _, err := h.Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String()}); !errors.Is(err, errStore) {
		t.Errorf("release failing: err = %v, want it", err)
	}

	cases := map[string]*Handler{
		"refused by the store": mkHandler(t, &recordingStore{revokeErr: limes.ErrNotFound}, nil, &allowAuthorizer{}),
		"denied":               mkHandler(t, &recordingStore{}, nil, &denyAuthorizer{}),
	}
	for name, h := range cases {
		holds := &fakeHoldReleaser{}
		if _, err := h.WithHoldRelease(holds).Revoke(adminCtx(), &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String()}); connect.CodeOf(err) == 0 {
			t.Errorf("%s: no error", name)
		}
		if len(holds.capabilities) != 0 {
			t.Errorf("%s: released %v, want nothing", name, holds.capabilities)
		}
	}
}
