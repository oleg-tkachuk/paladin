package mcp

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/delegation"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// The exchange in these tests: the IAM issuer of callers' tokens, the
// delegation issuer of their capabilities, and the MCP server's client.
const (
	callerIssuer     = "paladin"
	callerKey        = "caller-test-signing-key-32-bytes-long!"
	delegationIssuer = "paladin-capability/mcp"
	delegationTTL    = 15 * time.Minute
	bridgeClientID   = "paladin-mcp-server"
	bridgeSecret     = "bridge-secret"
	callerClientID   = "agent"
)

// errNotTheBridge is a token request from any client but the bridge.
var errNotTheBridge = errors.New("not the bridge")

// usersByID is a user directory holding a fixed set of users.
type usersByID map[uuid.UUID]authstore.User

func (u usersByID) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if user, ok := u[id]; ok {
		return user, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}

// delegationWorld is the Authorization Server's token endpoint running
// limes' exchange over one user, the bridge's client of it, and the
// verifier a plane would check the capabilities with.
type delegationWorld struct {
	user     authstore.User
	client   *DelegationClient
	verifier *limes.Verifier
	tokens   *issuer.Issuer
	calls    *int
}

func newDelegationWorld(t *testing.T) delegationWorld {
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
	capIssuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: delegationIssuer, DefaultTTL: delegationTTL})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{Keys: keys, Revocations: store, TrustedIssuers: []string{delegationIssuer}})
	if err != nil {
		t.Fatal(err)
	}

	user := authstore.User{UserID: uuid.New(), TenantID: uuid.New()}
	subjects := delegation.NewSubjectVerifier(&auth.JWTVerifier{
		Key: []byte(callerKey), ExpectedIssuer: callerIssuer, ExpectedAudience: auth.AudienceMCP,
	}, callerIssuer)
	ex, err := delegation.NewExchanger(capIssuer, subjects, usersByID{user.UserID: user}, delegationTTL, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := exchange.NewHandler(exchange.HandlerConfig{
		Exchanger: ex,
		Authenticate: func(r *http.Request) (exchange.Client, error) {
			if id, secret, ok := r.BasicAuth(); !ok || id != bridgeClientID || secret != bridgeSecret {
				return exchange.Client{}, errNotTheBridge
			}
			return exchange.Client{ID: bridgeClientID, SubjectAudiences: []string{auth.AudienceMCP}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		endpoint.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	tokens, err := issuer.New(issuer.Config{
		Issuer: callerIssuer, SigningKey: []byte(callerKey),
		AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return delegationWorld{
		user: user, verifier: verifier, tokens: tokens, calls: calls,
		client: NewDelegationClient(srv.Client(), srv.URL, bridgeClientID, bridgeSecret),
	}
}

// token is a paladin-mcp token for subject in tenant, granted to an agent
// under scopes.
func (w delegationWorld) token(t *testing.T, subject string, tenant uuid.UUID, scopes ...string) string {
	t.Helper()
	tok, _, err := w.tokens.MintAccess(issuer.AccessClaims{
		Subject: subject, TenantID: tenant, Audience: auth.AudienceMCP,
		OAuthClientID: callerClientID, OAuthScopes: scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// A caller's token is exchanged for a capability a plane accepts as acting
// for the user, on each plane the user may hold; asking again before it
// expires gets the same one.
func TestDelegationExchangerIssuesForTheCaller(t *testing.T) {
	w := newDelegationWorld(t)
	subject := w.token(t, w.user.UserID.String(), w.user.TenantID, auth.OAuthScopeRead)

	got, err := w.client.Capability(t.Context(), subject)
	if err != nil {
		t.Fatalf("Capability: %v", err)
	}
	for _, plane := range []string{auth.TokenPlaneData, auth.TokenPlaneIAM} {
		v, err := w.verifier.Verify(t.Context(), limes.VerifyRequest{Token: got, Audience: plane})
		if err != nil {
			t.Fatalf("%s: Verify: %v", plane, err)
		}
		if o := v.Capability().OnBehalfOf; o == nil || o.Subject != w.user.UserID.String() {
			t.Errorf("%s: acts for %+v, want the user", plane, o)
		}
	}
	if _, err := w.verifier.Verify(t.Context(), limes.VerifyRequest{Token: got, Audience: auth.TokenPlaneAdmin}); err == nil {
		t.Error("a user without an admin role got the admin plane")
	}

	again, err := w.client.Capability(t.Context(), subject)
	if err != nil || again != got || *w.calls != 1 {
		t.Errorf("a second exchange gave another capability (%v)", err)
	}
}

// A token that does not act for a user who may still act is refused as
// invalid_grant, and is not cached.
func TestDelegationExchangerRefusesAGoneUser(t *testing.T) {
	w := newDelegationWorld(t)
	gone := w.token(t, uuid.New().String(), w.user.TenantID, auth.OAuthScopeRead)
	for range 2 {
		if _, err := w.client.Capability(t.Context(), gone); !errors.Is(err, exchange.ErrInvalidGrant) {
			t.Errorf("err = %v, want ErrInvalidGrant", err)
		}
	}
	if len(w.client.cache) != 0 || *w.calls != 2 {
		t.Errorf("a refusal was cached: %d entries, %d exchanges", len(w.client.cache), *w.calls)
	}
}

// The edge sends the bridge's calls with the delegated capability alone;
// a caller's own capability is kept; a refused exchange is answered as RFC
// 6750 says, before the request reaches the server.
func TestDelegationEdge(t *testing.T) {
	w := newDelegationWorld(t)
	var seen http.Header
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		rw.WriteHeader(http.StatusOK)
	})
	edge := delegateCalls(next, w.client, "")
	send := func(token, own string) *httptest.ResponseRecorder {
		seen = nil
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
		r.Header.Set(paladin.HeaderAuthorization, "Bearer "+token)
		if own != "" {
			r.Header.Set(paladin.HeaderCapability, own)
		}
		edge.ServeHTTP(rec, r)
		return rec
	}

	read := w.token(t, w.user.UserID.String(), w.user.TenantID, auth.OAuthScopeRead)
	if rec := send(read, ""); rec.Code != http.StatusOK || seen.Get(paladin.HeaderAuthorization) != "" {
		t.Fatalf("status %d, Authorization %q; want the call sent without the token", rec.Code, seen.Get(paladin.HeaderAuthorization))
	}
	if _, err := w.verifier.Verify(t.Context(), limes.VerifyRequest{Token: seen.Get(paladin.HeaderCapability), Audience: auth.TokenPlaneData}); err != nil {
		t.Errorf("the call carries no delegated capability: %v", err)
	}

	if rec := send(read, "own-capability"); rec.Code != http.StatusOK || seen.Get(paladin.HeaderCapability) != "own-capability" ||
		seen.Get(paladin.HeaderAuthorization) != "" {
		t.Errorf("a caller's own capability: status %d, headers %v", rec.Code, seen)
	}

	for _, tc := range []struct {
		name   string
		token  string
		status int
	}{
		{"a user who is gone", w.token(t, uuid.New().String(), w.user.TenantID, auth.OAuthScopeRead), http.StatusUnauthorized},
		{"a grant consenting to nothing", w.token(t, w.user.UserID.String(), w.user.TenantID, "openid"), http.StatusForbidden},
	} {
		rec := send(tc.token, "")
		if rec.Code != tc.status || seen != nil {
			t.Errorf("%s: status %d, reached the server %v; want %d before it", tc.name, rec.Code, seen != nil, tc.status)
		}
		if !slices.ContainsFunc(rec.Result().Header.Values("WWW-Authenticate"), func(v string) bool { return v != "" }) {
			t.Errorf("%s: no challenge", tc.name)
		}
	}
}

// Behind the whole edge, the session is bound to the caller's token and the
// server sees the delegated capability in its place.
func TestRequireBearerDelegates(t *testing.T) {
	w := newDelegationWorld(t)
	var seen http.Header
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		rw.WriteHeader(http.StatusOK)
	})
	verifier := &auth.JWTVerifier{Key: []byte(callerKey), ExpectedIssuer: callerIssuer, ExpectedAudience: auth.AudienceMCP}
	edge := RequireBearer(next, verifier, "", OnlyMCPAudience(), WithDelegation(w.client))

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
	r.Header.Set(paladin.HeaderAuthorization, "Bearer "+w.token(t, w.user.UserID.String(), w.user.TenantID, auth.OAuthScopeRead))
	edge.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || seen.Get(paladin.HeaderAuthorization) != "" || seen.Get(paladin.HeaderCapability) == "" {
		t.Errorf("status %d, headers %v; want the capability in the token's place", rec.Code, seen)
	}
}
