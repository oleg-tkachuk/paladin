package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// A key-bound capability is useless to whoever copies it off the wire: every
// request must carry a fresh DPoP proof signed by the key it is bound to. The
// tests run the real issuer, verifier and DPoP verifier, and the unary path
// through a real Connect server over HTTP, so the method and procedure the
// interceptor checks are the ones Connect reports.

// dpopProcedure is the contract RPC the unary tests call.
const dpopProcedure = unarytest.ProbeProcedure

// otherProcedure is a path the proofs below were not made for.
const otherProcedure = "/auth.dpop.v1.Svc/Other"

// dpopIssuer is the issuer the fixture's capabilities name and its verifiers
// trust.
const dpopIssuer = "paladin-test"

type dpopFixture struct {
	issuer *limes.Issuer
	// keys and store are what the verifiers check tokens against.
	keys  limes.KeyResolver
	store *memstore.Store
	// verifier checks proofs the way the planes do; bare has no DPoP
	// configured, as a plane wired without one.
	verifier *limes.Verifier
	bare     *limes.Verifier
	key      ed25519.PrivateKey
	jkt      string
}

// newProofVerifier is a DPoP verifier that matches the path only, as the
// planes do; nonces may be nil.
func newProofVerifier(t *testing.T, nonces dpop.Nonces) *dpop.Verifier {
	t.Helper()
	replay, err := dpop.NewMemoryReplayCache(dpop.MemoryReplayCacheConfig{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := dpop.NewVerifier(dpop.VerifierConfig{Replay: replay, MatchPathOnly: true, Nonces: nonces})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func newDPoPFixture(t *testing.T) *dpopFixture {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: dpopIssuer})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	config := limes.VerifierConfig{
		Keys:           keys,
		Revocations:    store,
		TrustedIssuers: []string{dpopIssuer},
	}
	bare, err := limes.NewVerifier(config)
	if err != nil {
		t.Fatal(err)
	}
	config.DPoP = newProofVerifier(t, nil)
	verifier, err := limes.NewVerifier(config)
	if err != nil {
		t.Fatal(err)
	}
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := dpop.Thumbprint(agentKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	return &dpopFixture{issuer: issuer, keys: keys, store: store, verifier: verifier, bare: bare, key: agentKey, jkt: jkt}
}

// mint issues a capability bound to jkt; unbound when jkt is empty. A
// key-bound capability verifies only with a proof of its key, so it is minted
// here rather than through captest.
func (f *dpopFixture) mint(t *testing.T, jkt string) string {
	t.Helper()
	tenant := uuid.New()
	res, err := f.issuer.Issue(context.Background(), limes.IssueRequest{
		IssuedBy: limes.Principal{Type: limes.PrincipalTypeUser, TenantID: tenant, Subject: "op"},
		Subject:  limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: "agent"},
		Audience: []string{TokenPlaneData},
		Caveats: limes.Caveats{
			Ops:      []limes.Op{limes.OpGet},
			Requests: limes.RequestCeiling{Unlimited: true},
			Budget:   limes.BudgetCeiling{Unlimited: true, UnitCode: captest.Unit},
		},
		TTL:             time.Hour,
		ConfirmationJKT: jkt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Token
}

func (f *dpopFixture) proof(t *testing.T, url, token string) string {
	t.Helper()
	p, err := dpop.Sign(context.Background(), f.key, dpop.SignRequest{
		Method: http.MethodPost, URL: url, Token: token, IssuedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// serve mounts the Probe behind the interceptor on a real HTTP server and
// returns a client for it and the server's base URL.
func serve(t *testing.T, i connect.ServerInterceptor) (paladiniamv1connect.HealthServiceClient, string) {
	t.Helper()
	server := connect.NewServer(i)
	paladiniamv1connect.RegisterHealthServiceHandler(server, &unarytest.Probe{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return paladiniamv1connect.NewHealthServiceClient(
		connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL))), srv.URL
}

// callUnder sends the capability as Authorization under scheme, with proof.
func callUnder(c paladiniamv1connect.HealthServiceClient, scheme limes.AuthScheme, token, proof string) error {
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set(paladin.HeaderAuthorization, string(scheme)+" "+token)
	if proof != "" {
		info.RequestHeader().Set(dpop.Header, proof)
	}
	_, err := c.GetVersion(ctx, &iamv1.GetVersionRequest{})
	return err
}

// RFC 9449's own scheme: a key-bound capability under DPoP is accepted with
// its proof, and a bearer one under DPoP is refused, its binding being the
// one the scheme claims.
func TestCapabilityDPoP_TheDPoPScheme(t *testing.T) {
	f := newDPoPFixture(t)
	client, base := serve(t, CapabilityInterceptor(f.verifier, TokenPlaneData, nil, 0))
	bound := f.mint(t, f.jkt)
	if err := callUnder(client, limes.AuthSchemeDPoP, bound, f.proof(t, base+dpopProcedure, bound)); err != nil {
		t.Fatalf("a bound capability under DPoP refused: %v", err)
	}
	bearer := f.mint(t, "")
	err := callUnder(client, limes.AuthSchemeDPoP, bearer, f.proof(t, base+dpopProcedure, bearer))
	// Over HTTP the cause stays on the server; the message carries it.
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), limes.ErrAuthSchemeMismatch.Error()) {
		t.Fatalf("a bearer capability under DPoP: err = %v, want PermissionDenied for %v", err, limes.ErrAuthSchemeMismatch)
	}
}

func call(c paladiniamv1connect.HealthServiceClient, token, proof string) error {
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set(HeaderCapability, token)
	if proof != "" {
		info.RequestHeader().Set(dpop.Header, proof)
	}
	_, err := c.GetVersion(ctx, &iamv1.GetVersionRequest{})
	return err
}

func TestCapabilityDPoP_Unary(t *testing.T) {
	f := newDPoPFixture(t)
	client, base := serve(t, CapabilityInterceptor(f.verifier, TokenPlaneData, nil, 0))
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
		"proof for elsewhere": f.proof(t, base+otherProcedure, bound),
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
// quietly treat a bound capability as a bearer token. limes counts that the
// deployment's fault (ErrProofUnsupported), not a refusal of the capability:
// FailedPrecondition, which no retry mends, rather than Unavailable.
func TestCapabilityDPoP_BoundRefusedWithoutAVerifier(t *testing.T) {
	f := newDPoPFixture(t)
	client, base := serve(t, CapabilityInterceptor(f.bare, TokenPlaneData, nil, 0))
	bound := f.mint(t, f.jkt)
	err := call(client, bound, f.proof(t, base+dpopProcedure, bound))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), proofUnchecked) {
		t.Fatalf("err = %v, want FailedPrecondition: %q", err, proofUnchecked)
	}
	if err := call(client, f.mint(t, ""), ""); err != nil {
		t.Fatalf("unbound capability: %v", err)
	}
}

// The streaming hook is separate from the unary one — only the unary path
// reads the request message — so the possession check is pinned on it too.
// It runs through connect.Server.Call, which gives the call its server-side
// headers; with no HTTP transport the method is the POST Connect uses.
func TestCapabilityDPoP_Streaming(t *testing.T) {
	f := newDPoPFixture(t)
	i := CapabilityInterceptor(f.verifier, TokenPlaneData, nil, 0)
	bound := f.mint(t, f.jkt)

	run := func(proof string) (context.Context, error) {
		h := &connect.Header{}
		h.Set(HeaderCapability, bound)
		if proof != "" {
			h.Set(dpop.Header, proof)
		}
		return callStream(context.Background(), []connect.ServerInterceptor{i}, h)
	}
	seen, err := run(f.proof(t, "https://data.example.com"+streamProcedure, bound))
	if err != nil {
		t.Fatalf("valid proof refused: %v", err)
	}
	if _, ok := CapabilityFromContext(seen); !ok {
		t.Error("the verified capability did not reach the stream handler")
	}
	_, err = run("")
	if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, dpop.ErrRequired) {
		t.Fatalf("missing proof: err = %v, want PermissionDenied wrapping dpop.ErrRequired", err)
	}
}

// The Go SDK signs its own proofs — it does not import limes —
// so the two implementations are held together here, against the server.
func TestCapabilityDPoP_GoSDKInteroperates(t *testing.T) {
	f := newDPoPFixture(t)
	jkt, err := paladin.DPoPThumbprint(f.key.Public())
	if err != nil || jkt != f.jkt {
		t.Fatalf("SDK thumbprint = %q, %v; server computes %q", jkt, err, f.jkt)
	}
	_, base := serve(t, CapabilityInterceptor(f.verifier, TokenPlaneData, nil, 0))

	bound := f.mint(t, f.jkt)
	sdk, err := paladin.New(base+"/", paladin.WithCapability(bound), paladin.WithDPoP(f.key))
	if err != nil {
		t.Fatal(err)
	}
	client := paladiniamv1connect.NewHealthServiceClient(sdk.Connect())
	for range 2 { // a fresh proof per call: the second is not a replay
		if _, err := client.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
			t.Fatalf("SDK call with WithDPoP refused: %v", err)
		}
	}

	bare, err := paladin.New(base, paladin.WithCapability(bound))
	if err != nil {
		t.Fatal(err)
	}
	client = paladiniamv1connect.NewHealthServiceClient(bare.Connect())
	if _, err := client.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("bound capability without WithDPoP: err = %v, want PermissionDenied", err)
	}
}

// countingNonces counts the proofs refused for their nonce.
type countingNonces struct {
	dpop.Nonces
	refused atomic.Int64
}

func (c *countingNonces) Valid(ctx context.Context, nonce string) (bool, error) {
	ok, err := c.Nonces.Valid(ctx, nonce)
	if err == nil && !ok {
		c.refused.Add(1)
	}
	return ok, err
}

// A server that asks for nonces challenges the SDK's first proof once; the
// SDK resends it with the nonce, keeps the one each response brings, and its
// next call goes through without a challenge.
func TestCapabilityDPoP_GoSDKAnswersANonceChallenge(t *testing.T) {
	f := newDPoPFixture(t)
	const nonceLifetime = time.Minute
	hmac, err := dpop.NewHMACNonces(dpop.HMACNoncesConfig{
		Keys:     dpop.SealNonceKeys([]byte(strings.Repeat("n", dpop.HMACNonceKeyBytes))),
		Lifetime: nonceLifetime,
	})
	if err != nil {
		t.Fatal(err)
	}
	nonces := &countingNonces{Nonces: hmac}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys:           f.keys,
		Revocations:    f.store,
		TrustedIssuers: []string{dpopIssuer},
		DPoP:           newProofVerifier(t, nonces),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, base := serve(t, CapabilityInterceptor(verifier, TokenPlaneData, nil, 0))
	sdk, err := paladin.New(base+"/", paladin.WithCapability(f.mint(t, f.jkt)), paladin.WithDPoP(f.key))
	if err != nil {
		t.Fatal(err)
	}
	client := paladiniamv1connect.NewHealthServiceClient(sdk.Connect())
	for range 2 {
		if _, err := client.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
			t.Fatalf("a call under nonces refused: %v", err)
		}
	}
	if got := nonces.refused.Load(); got != 1 {
		t.Errorf("%d proofs refused for their nonce, want 1: the first", got)
	}
}
