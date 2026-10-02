package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/capability/memstore"
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
	signer, err := capability.NewEd25519Signer("k1", priv)
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New[any]()
	issuer, err := capability.NewIssuer(capability.IssuerConfig{Signer: signer, Store: store, IssuerName: "paladin-test"})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := capability.NewStandardVerifier(capability.VerifierConfig{
		Keys:           capability.NewStaticKeyResolver(map[string]ed25519.PublicKey{"k1": pub}),
		Revocations:    store,
		TrustedIssuers: []string{"paladin-test"},
		AcceptBiscuit:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cap, _, err := issuer.Issue(context.Background(), capability.IssueRequest{
		IssuedBy: capability.Principal{Subject: "op"},
		Subject:  capability.Principal{TenantID: uuid.New(), Subject: "agent"},
		Audience: []string{capability.AudiencePlaneData},
		Caveats:  capability.Caveats{Ops: []capability.Op{capability.OpGet, capability.OpDelete}, ResourcePrefixes: []string{"corpus/"}},
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	full, err := issuer.Biscuit(cap)
	if err != nil {
		t.Fatal(err)
	}
	readOnly, err := capability.Attenuate(full, capability.Attenuation{
		Ops:              []capability.Op{capability.OpGet},
		ResourcePrefixes: []string{"corpus/public/"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The handler stands in for a data-plane RPC: it asserts the op and
	// resource the request names, as the real handlers do.
	const procedure = "/auth.biscuit.v1.Svc/Call"
	mux := http.NewServeMux()
	mux.Handle(procedure, connect.NewUnaryHandler(procedure,
		func(ctx context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			op := capability.Op(req.Header().Get("X-Test-Op"))
			if err := AssertCapabilityOp(ctx, op, req.Header().Get("X-Test-Resource")); err != nil {
				return nil, err
			}
			return connect.NewResponse(&emptypb.Empty{}), nil
		}, connect.WithInterceptors(CapabilityEstablishingInterceptor(verifier, capability.AudiencePlaneData, nil, 0, "", nil))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+procedure)
	call := func(token, op, resource string) error {
		req := connect.NewRequest(&emptypb.Empty{})
		req.Header().Set("X-Paladin-Capability", token)
		req.Header().Set("X-Test-Op", op)
		req.Header().Set("X-Test-Resource", resource)
		_, err := client.CallUnary(context.Background(), req)
		return err
	}

	if err := call(full, "delete", "corpus/private/x"); err != nil {
		t.Fatalf("full Biscuit refused what its capability allows: %v", err)
	}
	if err := call(readOnly, "get", "corpus/public/x"); err != nil {
		t.Fatalf("attenuated Biscuit refused inside its scope: %v", err)
	}
	for _, c := range []struct{ op, resource string }{
		{"delete", "corpus/public/x"}, // an op attenuated away
		{"get", "corpus/private/x"},   // a resource attenuated away
	} {
		if err := call(readOnly, c.op, c.resource); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s %s with the attenuated Biscuit: err = %v, want PermissionDenied", c.op, c.resource, err)
		}
	}
}
