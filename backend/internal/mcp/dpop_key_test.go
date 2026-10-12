package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	datav1connect "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// rsaTestKeyBits sizes the RSA key the parser must refuse; the size is
// irrelevant to the refusal, so the smallest Go generates quickly.
const rsaTestKeyBits = 1024

func pemPKCS8(t *testing.T, key any) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: pemPrivateKey, Bytes: der}))
}

func TestParseBridgeKey(t *testing.T) {
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	p256, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	rsaKey, _ := rsa.GenerateKey(rand.Reader, rsaTestKeyBits)
	sec1, _ := x509.MarshalECPrivateKey(p256)

	for _, tc := range []struct {
		name        string
		pem         string
		unsupported bool
		fails       bool
	}{
		{name: "ed25519", pem: pemPKCS8(t, ed)},
		{name: "p-256", pem: pemPKCS8(t, p256)},
		{name: "p-384", pem: pemPKCS8(t, p384), unsupported: true, fails: true},
		{name: "rsa", pem: pemPKCS8(t, rsaKey), unsupported: true, fails: true},
		{name: "sec1, not pkcs#8", pem: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})), fails: true},
		{name: "not pem", pem: "not a key", fails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := ParseBridgeKey(tc.pem)
			if tc.fails {
				if err == nil {
					t.Fatal("parsed")
				}
				if got := errors.Is(err, ErrUnsupportedDPoPKey); got != tc.unsupported {
					t.Errorf("errors.Is(ErrUnsupportedDPoPKey) = %v: %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want, _ := paladin.DPoPThumbprint(key.Signer.Public())
			if key.JKT == "" || key.JKT != want {
				t.Errorf("JKT = %q, want %q", key.JKT, want)
			}
		})
	}
}

func newBridgeKey(t *testing.T) *BridgeKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParseBridgeKey(pemPKCS8(t, priv))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestGetBridgeKeyTool(t *testing.T) {
	key := newBridgeKey(t)
	cs := dialInProcess(t, mustClients(t)(NewClientsWithCapability(nil, "http://a", "http://d", "http://i", "", "", WithBridgeKey(key))))
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "paladin_get_bridge_key", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || !strings.Contains(toolText(res), key.JKT) {
		t.Fatalf("result = %s, want %s", toolText(res), key.JKT)
	}

	cs = dialInProcess(t, mustClients(t)(NewClients(nil, "http://a", "http://d", "http://i", "")))
	res, err = cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "paladin_get_bridge_key", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a bridge without a key answered %s", toolText(res))
	}
}

// objectPlane answers GetObject for whatever reaches it.
type objectPlane struct {
	datav1connect.UnimplementedObjectServiceHandler
}

func (objectPlane) GetObject(context.Context, *datav1.GetObjectRequest) (*datav1.Object, error) {
	return &datav1.Object{Name: "tenants/acme/collections/docs/objects/o1"}, nil
}

// capabilityPlane is a data plane behind the real capability interceptor,
// with DPoP checked the way the planes check it.
type capabilityPlane struct {
	issuer *limes.Issuer
	url    string
}

func newCapabilityPlane(t *testing.T) *capabilityPlane {
	t.Helper()
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
		DPoP:           &limes.DPoPVerifier{Replay: limes.NewMemoryReplayCache(0), MatchPathOnly: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := connect.NewServer(auth.CapabilityInterceptor(verifier, auth.TokenPlaneData, nil, 0, ""))
	datav1connect.RegisterObjectServiceHandler(server, objectPlane{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &capabilityPlane{issuer: issuer, url: ts.URL}
}

// issue mints a capability bound to the agent key jkt, which may share it.
func (p *capabilityPlane) issue(t *testing.T, jkt string) (limes.Capability, string) {
	t.Helper()
	c, tok, err := p.issuer.Issue(context.Background(), limes.IssueRequest{
		IssuedBy:        limes.Principal{Subject: "op"},
		Subject:         limes.Principal{Type: limes.PrincipalAgent, TenantID: uuid.New(), Subject: "agent"},
		Audience:        []string{auth.TokenPlaneData},
		Caveats:         limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpShare}},
		TTL:             time.Hour,
		ConfirmationJKT: jkt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, tok
}

func (p *capabilityPlane) getObject(t *testing.T, capability string, opts ...ClientOption) *mcpsdk.CallToolResult {
	t.Helper()
	c := mustClients(t)(NewClientsWithCapability(nil, p.url, p.url, p.url, "", capability, opts...))
	res, err := dialInProcess(t, c).CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: "paladin_get_object", Arguments: map[string]any{"name": "tenants/acme/collections/docs/objects/o1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The agent's key-bound capability, delegated to the bridge's key, passes
// through the bridge: the bridge proves possession on each call. The agent's
// own capability does not, since the bridge cannot sign for the agent's key.
func TestBridgeForwardsACapabilityDelegatedToIt(t *testing.T) {
	plane := newCapabilityPlane(t)
	bridge := newBridgeKey(t)
	_, agentKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	agentJKT, err := paladin.DPoPThumbprint(agentKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	parent, parentToken := plane.issue(t, agentJKT)
	_, child, err := plane.issuer.Delegate(context.Background(), limes.DelegateRequest{
		Parent:          parent,
		Subject:         parent.Subject,
		InheritCaveats:  true,
		ConfirmationJKT: bridge.JKT,
	})
	if err != nil {
		t.Fatal(err)
	}

	for range 2 { // a fresh proof per call: the second is not a replay
		if res := plane.getObject(t, child, WithBridgeKey(bridge)); res.IsError {
			t.Fatalf("delegated capability refused: %s", toolText(res))
		}
	}
	if res := plane.getObject(t, child); !res.IsError {
		t.Error("a bridge without the key forwarded a capability bound to it")
	}
	if res := plane.getObject(t, parentToken, WithBridgeKey(bridge)); !res.IsError {
		t.Error("the bridge forwarded a capability bound to the agent's key")
	}
}

// An unbound capability is unaffected by the bridge's key.
func TestBridgeKeyLeavesUnboundCapabilitiesAlone(t *testing.T) {
	plane := newCapabilityPlane(t)
	_, unbound := plane.issue(t, "")
	if res := plane.getObject(t, unbound, WithBridgeKey(newBridgeKey(t))); res.IsError {
		t.Fatalf("unbound capability refused: %s", toolText(res))
	}
}
