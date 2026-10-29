package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
)

// A Biscuit narrowed offline by its holder must be enforced by the same gate
// that enforces a server-side delegation: the interceptor verifies and folds
// the attenuation in, and AssertCapabilityOp — where every data-plane handler
// checks its operation — refuses what the narrowed token no longer allows.
func TestCapabilityBiscuit_OfflineAttenuationIsEnforced(t *testing.T) {
	m := captest.NewMinter(t)
	v, _ := m.Mint(t, limes.Capability{
		Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, Subject: "agent"},
		Audience: []string{TokenPlaneData},
		Caveats:  limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpDelete, limes.OpShare}, ResourcePrefixes: []string{"corpus/"}},
	})
	sealed, err := m.Issuer.Biscuit(context.Background(), limes.BiscuitRequest{Capability: v})
	if err != nil {
		t.Fatal(err)
	}
	full := sealed.Token
	readOnly, err := biscuit.Attenuate(full, limes.Attenuation{
		Ops:              []limes.Op{limes.OpGet},
		ResourcePrefixes: []string{"corpus/public/"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The handler stands in for a data-plane RPC: it asserts the op and
	// resource the request names, as the real handlers do.
	const (
		headerTestOp       = "X-Test-Op"
		headerTestResource = "X-Test-Resource"
	)
	probe := &unarytest.Probe{OnCall: func(ctx context.Context) error {
		h := unary.Info(ctx).RequestHeader()
		return AssertCapabilityOp(ctx, limes.Op(h.Get(headerTestOp)), h.Get(headerTestResource))
	}}
	interceptors := []connect.ServerInterceptor{
		CapabilityEstablishingInterceptor(m.Verifier, TokenPlaneData, nil, 0, nil),
	}
	call := func(token string, op limes.Op, resource string) error {
		_, err := unarytest.CallProbe(context.Background(), probe, interceptors,
			HeaderCapability, token, headerTestOp, string(op), headerTestResource, resource)
		return err
	}

	const (
		privateObject = "corpus/private/x"
		publicObject  = "corpus/public/x"
	)
	if err := call(full, limes.OpDelete, privateObject); err != nil {
		t.Fatalf("full Biscuit refused what its capability allows: %v", err)
	}
	if err := call(readOnly, limes.OpGet, publicObject); err != nil {
		t.Fatalf("attenuated Biscuit refused inside its scope: %v", err)
	}
	for _, c := range []struct {
		op       limes.Op
		resource string
	}{
		{limes.OpDelete, publicObject}, // an op attenuated away
		{limes.OpGet, privateObject},   // a resource attenuated away
	} {
		if err := call(readOnly, c.op, c.resource); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s %s with the attenuated Biscuit: err = %v, want PermissionDenied", c.op, c.resource, err)
		}
	}
}
