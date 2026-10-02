package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/capability/memstore"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// A key-bound capability is useless to whoever copies it off the wire: every
// request must carry a fresh DPoP proof signed by the key it is bound to. The
// tests run the real issuer, verifier and DPoP verifier, and the unary path
// through a real Connect server, so the method and procedure the interceptor
// checks are the ones Connect reports.

const dpopProcedure = "/auth.dpop.v1.Svc/Call"

type dpopFixture struct {
	issuer   *capability.Issuer
	verifier *capability.StandardVerifier
	key      ed25519.PrivateKey
	jkt      string
}

func newDPoPFixture(t *testing.T) *dpopFixture {
	t.Helper()
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
	})
	if err != nil {
		t.Fatal(err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := capability.KeyThumbprint(agentKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	return &dpopFixture{issuer: issuer, verifier: verifier, key: agentKey, jkt: jkt}
}

func (f *dpopFixture) mint(t *testing.T, jkt string) string {
	t.Helper()
	_, tok, err := f.issuer.Issue(context.Background(), capability.IssueRequest{
		IssuedBy:        capability.Principal{Subject: "op"},
		Subject:         capability.Principal{TenantID: uuid.New(), Subject: "agent"},
		Audience:        []string{capability.AudiencePlaneData},
		Caveats:         capability.Caveats{Ops: []capability.Op{capability.OpGet}},
		TTL:             time.Hour,
		ConfirmationJKT: jkt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *dpopFixture) proof(t *testing.T, url, token string) string {
	t.Helper()
	p, err := capability.NewDPoPProof(f.key, http.MethodPost, url, token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// serve mounts one unary procedure behind the interceptor and returns a
// client for it.
func serve(t *testing.T, i connect.Interceptor) (*connect.Client[emptypb.Empty, emptypb.Empty], string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(dpopProcedure, connect.NewUnaryHandler(dpopProcedure,
		func(context.Context, *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
			return connect.NewResponse(&emptypb.Empty{}), nil
		}, connect.WithInterceptors(i)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return connect.NewClient[emptypb.Empty, emptypb.Empty](srv.Client(), srv.URL+dpopProcedure), srv.URL
}

func call(c *connect.Client[emptypb.Empty, emptypb.Empty], token, proof string) error {
	req := connect.NewRequest(&emptypb.Empty{})
	req.Header().Set("X-Paladin-Capability", token)
	if proof != "" {
		req.Header().Set(capability.DPoPHeader, proof)
	}
	_, err := c.CallUnary(context.Background(), req)
	return err
}

func TestCapabilityDPoP_Unary(t *testing.T) {
	f := newDPoPFixture(t)
	dpop := &capability.DPoPVerifier{Replay: capability.NewMemoryReplayCache(0)}
	client, base := serve(t, CapabilityInterceptor(f.verifier, capability.AudiencePlaneData, nil, 0, "", WithDPoP(dpop)))
	bound := f.mint(t, f.jkt)

	if err := call(client, bound, f.proof(t, base+dpopProcedure, bound)); err != nil {
		t.Fatalf("bound capability with a valid proof refused: %v", err)
	}
	// The server checks the path only: the client may reach it through a
	// proxy under a host and prefix the server never sees.
	if err := call(client, bound, f.proof(t, "https://gw.example.com/api"+dpopProcedure, bound)); err != nil {
		t.Fatalf("proof addressed through a proxy refused: %v", err)
	}

	refused := map[string]string{
		"no proof":            "",
		"proof for elsewhere": f.proof(t, base+"/auth.dpop.v1.Svc/Other", bound),
		"proof for a token":   f.proof(t, base+dpopProcedure, "another-token"),
	}
	replayed := f.proof(t, base+dpopProcedure, bound)
	if err := call(client, bound, replayed); err != nil {
		t.Fatalf("first use of a proof refused: %v", err)
	}
	refused["replayed proof"] = replayed
	for name, proof := range refused {
		t.Run(name, func(t *testing.T) {
			err := call(client, bound, proof)
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("err = %v, want PermissionDenied", err)
			}
		})
	}

	if err := call(client, f.mint(t, ""), ""); err != nil {
		t.Fatalf("unbound capability needs no proof: %v", err)
	}
}

// A plane wired without a DPoP verifier cannot check a binding, and must not
// quietly treat a bound capability as a bearer token.
func TestCapabilityDPoP_BoundRefusedWithoutAVerifier(t *testing.T) {
	f := newDPoPFixture(t)
	client, base := serve(t, CapabilityInterceptor(f.verifier, capability.AudiencePlaneData, nil, 0, ""))
	bound := f.mint(t, f.jkt)
	err := call(client, bound, f.proof(t, base+dpopProcedure, bound))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	if err := call(client, f.mint(t, ""), ""); err != nil {
		t.Fatalf("unbound capability: %v", err)
	}
}

// The streaming handler is a separate copy of the gate.
func TestCapabilityDPoP_Streaming(t *testing.T) {
	f := newDPoPFixture(t)
	dpop := &capability.DPoPVerifier{Replay: capability.NewMemoryReplayCache(0)}
	i := CapabilityInterceptor(f.verifier, capability.AudiencePlaneData, nil, 0, "", WithDPoP(dpop))
	bound := f.mint(t, f.jkt)
	procedure := newStreamConn().Spec().Procedure

	run := func(proof string) error {
		conn := newStreamConn()
		conn.header.Set("X-Paladin-Capability", bound)
		if proof != "" {
			conn.header.Set(capability.DPoPHeader, proof)
		}
		var called bool
		var seen context.Context
		return i.WrapStreamingHandler(streamNext(&called, &seen))(context.Background(), conn)
	}
	if err := run(f.proof(t, "https://data.example.com"+procedure, bound)); err != nil {
		t.Fatalf("valid proof refused: %v", err)
	}
	err := run("")
	if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, capability.ErrDPoPRequired) {
		t.Fatalf("missing proof: err = %v, want PermissionDenied wrapping ErrDPoPRequired", err)
	}
}

// The Go SDK signs its own proofs — it does not import the capability module —
// so the two implementations are held together here, against the server.
func TestCapabilityDPoP_GoSDKInteroperates(t *testing.T) {
	f := newDPoPFixture(t)
	jkt, err := paladin.DPoPThumbprint(f.key.Public())
	if err != nil || jkt != f.jkt {
		t.Fatalf("SDK thumbprint = %q, %v; server computes %q", jkt, err, f.jkt)
	}
	dpop := &capability.DPoPVerifier{Replay: capability.NewMemoryReplayCache(0)}
	_, base := serve(t, CapabilityInterceptor(f.verifier, capability.AudiencePlaneData, nil, 0, "", WithDPoP(dpop)))

	bound := f.mint(t, f.jkt)
	sdk, err := paladin.New(base+"/", paladin.WithCapability(bound), paladin.WithDPoP(f.key))
	if err != nil {
		t.Fatal(err)
	}
	client := connect.NewClient[emptypb.Empty, emptypb.Empty](sdk.HTTPClient(), sdk.BaseURL()+dpopProcedure, sdk.ClientOptions()...)
	for range 2 { // a fresh proof per call: the second is not a replay
		if _, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); err != nil {
			t.Fatalf("SDK call with WithDPoP refused: %v", err)
		}
	}

	bare, err := paladin.New(base, paladin.WithCapability(bound))
	if err != nil {
		t.Fatal(err)
	}
	client = connect.NewClient[emptypb.Empty, emptypb.Empty](bare.HTTPClient(), bare.BaseURL()+dpopProcedure, bare.ClientOptions()...)
	if _, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bound capability without WithDPoP: err = %v, want PermissionDenied", err)
	}
}
