package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
)

// A Biscuit narrowed offline by its holder must be enforced by the same gate
// that enforces a server-side delegation: the interceptor verifies and folds
// the attenuation in, and AssertCapabilityOp — where every data-plane handler
// checks its operation — refuses what the narrowed token no longer allows.
func TestCapabilityBiscuit_OfflineAttenuationIsEnforced(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewEd25519Signer("k1", priv)
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New[any]()
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: "paladin-test"})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewStandardVerifier(limes.VerifierConfig{
		Keys:           limes.NewStaticKeyResolver(map[string]ed25519.PublicKey{"k1": pub}),
		Revocations:    store,
		TrustedIssuers: []string{"paladin-test"},
		AcceptBiscuit:  true,

		BiscuitRevocations: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	cap, _, err := issuer.Issue(context.Background(), limes.IssueRequest{
		IssuedBy: limes.Principal{Subject: "op"},
		Subject:  limes.Principal{TenantID: uuid.New(), Subject: "agent"},
		Audience: []string{limes.AudiencePlaneData},
		Caveats:  limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpDelete}, ResourcePrefixes: []string{"corpus/"}},
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	full, err := issuer.Biscuit(cap)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := limes.Attenuate(full, limes.Attenuation{
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
		CapabilityEstablishingInterceptor(verifier, limes.AudiencePlaneData, nil, 0, "", nil),
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
