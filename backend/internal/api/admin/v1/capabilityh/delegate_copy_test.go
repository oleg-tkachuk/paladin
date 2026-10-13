package capabilityh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// Delegating narrows from what was presented: a child cannot get back an op
// its parent does not hold, and with no caveats it inherits the parent's.
func TestDelegate_CapabilityPath_NarrowsFromThePresentedParent(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := delegateHandler(m, &denyAuthorizer{})
	ctx := auth.WithCapability(context.Background(), parent)

	wider := delegateReq(parent)
	wider.Caveats = &adminv1.CapabilityCaveats{Ops: []string{string(limes.OpGet), string(limes.OpPut)}}
	if _, err := h.Delegate(ctx, wider); codeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("a child gained put the parent lacks: code = %v (%v)", codeOf(err), err)
	}

	resp, err := h.Delegate(ctx, delegateReq(parent))
	if err != nil {
		t.Fatalf("inheriting delegate: %v", err)
	}
	if got, want := resp.GetCapability().GetCaveats().GetOps(), parent.Capability().Caveats.Ops; len(got) != len(want) {
		t.Fatalf("inherited ops = %v, want the parent's %v", got, want)
	}
}

// copyRequestCeiling is the request limit a Biscuit copy sets of its own.
const copyRequestCeiling = 1

// A Biscuit copy cannot delegate, whether it only narrowed or set limits of
// its own: its attenuation is not in a token the issuer can extend, so a
// child would shed it. Its holder narrows offline instead.
func TestDelegate_CapabilityPath_RefusesABiscuitCopy(t *testing.T) {
	limit := limes.RequestCeiling{Max: copyRequestCeiling}
	cases := map[string]limes.Attenuation{
		"narrowed":        {Ops: []limes.Op{limes.OpGet, limes.OpShare}},
		"with own limits": {Requests: &limit},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			m := captest.NewMinter(t)
			parent := mintParent(t, m, limes.OpGet, limes.OpPut, limes.OpShare)
			copied, _ := m.Attenuated(t, parent, a)
			h := delegateHandler(m, &denyAuthorizer{})

			_, err := h.Delegate(auth.WithCapability(context.Background(), copied), delegateReq(parent))
			if codeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (%v)", codeOf(err), err)
			}
		})
	}
}
