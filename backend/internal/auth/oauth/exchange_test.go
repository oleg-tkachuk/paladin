package oauth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/delegation"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

const (
	mcpResourceURL  = "https://mcp.example.test/mcp"
	exchangerID     = "paladin-mcp-server"
	exchangerSecret = "exchanger-secret"
	agentWithMCPID  = "mcp-agent"
)

// agentWithMCP is a public agent client that may be issued paladin-mcp.
// paladin-data comes first, so a resource that maps to nothing falls back
// to it rather than to paladin-mcp.
func agentWithMCP() Client {
	c := publicClient()
	c.ClientID = agentWithMCPID
	c.AllowedAudiences = []string{auth.AudienceData, auth.AudienceMCP}
	return c
}

func exchanger(t *testing.T) Client {
	t.Helper()
	hash, err := auth.HashApiKeySecret(exchangerSecret)
	if err != nil {
		t.Fatal(err)
	}
	return Client{ClientID: exchangerID, SecretHash: hash}
}

func mcpVerifier() *auth.JWTVerifier {
	return &auth.JWTVerifier{Key: []byte(testSigningKey), ExpectedIssuer: "paladin", ExpectedAudience: auth.AudienceMCP}
}

// The delegation issuer the exchange in these tests issues through, and how
// long its capabilities live.
const (
	delegationIssuerName = "paladin-capability/mcp"
	delegationTTL        = 15 * time.Minute
)

// exchangeFixture is a handler that maps the MCP resource and lets the
// exchanger exchange, plus the agent's paladin-mcp token for u and the
// verifier a plane checks the exchanged capability with.
type exchangeFixture struct {
	h        *Handler
	store    *memStore
	subject  string
	verifier *limes.Verifier
}

// withExchange serves the exchange on h to the exchanger client, over a
// delegation issuer of its own; the verifier checks what it issues.
func withExchange(t *testing.T, h *Handler) (*Handler, *limes.Verifier) {
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
	capIssuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: delegationIssuerName, DefaultTTL: delegationTTL})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{Keys: keys, Revocations: store, TrustedIssuers: []string{delegationIssuerName}})
	if err != nil {
		t.Fatal(err)
	}
	ex, err := delegation.NewExchanger(capIssuer, delegation.NewSubjectVerifier(mcpVerifier(), "paladin"), h.users, delegationTTL, limes.AbstractUnitCode, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	h, err = h.WithTokenExchange(ex, map[string]bool{exchangerID: true})
	if err != nil {
		t.Fatal(err)
	}
	return h, verifier
}

func newExchangeFixture(t *testing.T, u authstore.User) exchangeFixture {
	t.Helper()
	store := newMemStore()
	store.clients[agentWithMCPID] = agentWithMCP()
	store.clients[exchangerID] = exchanger(t)
	h, verifier := withExchange(t, testHandler(t, store, &memRefresh{}, u).WithMCPResource(mcpResourceURL))
	tok := authorizeAndExchange(t, h, u, agentWithMCP(), mcpResourceURL)
	return exchangeFixture{h: h, store: store, subject: tok.AccessToken, verifier: verifier}
}

func exchangeForm(subject string, planes ...string) url.Values {
	return url.Values{
		"grant_type": {GrantTypeTokenExchange}, "client_id": {exchangerID}, "client_secret": {exchangerSecret},
		"subject_token": {subject}, "subject_token_type": {TokenTypeAccessToken}, "audience": planes,
	}
}

// exchangeAnswer is the RFC 8693 §2.2.1 answer.
type exchangeAnswer struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
}

func decodeAnswer(t *testing.T, body []byte) exchangeAnswer {
	t.Helper()
	var a exchangeAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func oauthError(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body: %v (%s)", err, body)
	}
	return e.Error
}

// The MCP server's resource URL is the paladin-mcp audience; a client that
// may not hold it falls back as before.
func TestAuthorize_MCPResourceURLIssuesTheMCPAudience(t *testing.T) {
	f := newExchangeFixture(t, sampleUser())
	if got := accessAudience(t, f.subject); got != auth.AudienceMCP {
		t.Errorf("audience for %s = %q, want %q", mcpResourceURL, got, auth.AudienceMCP)
	}

	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u).WithMCPResource(mcpResourceURL)
	if got := accessAudience(t, authorizeAndExchange(t, h, u, publicClient(), mcpResourceURL).AccessToken); got != auth.AudienceData {
		t.Errorf("a client without paladin-mcp got %q, want its first audience %q", got, auth.AudienceData)
	}
}

// /authorize applies login's eligibility: a user with no admin role is not
// issued paladin-admin, whatever the client may hold.
func TestAuthorize_RefusesAnAudienceTheUserMayNotHold(t *testing.T) {
	store := newMemStore()
	store.clients[adminClient().ClientID] = adminClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)
	verifier := strings.Repeat("a", 50)
	rec := postForm(h, "/oauth/authorize", url.Values{
		"client_id": {adminClient().ClientID}, "redirect_uri": {adminClient().RedirectURIs[0]}, "scope": {grantScope},
		"resource":       {auth.AudienceAdmin},
		"code_challenge": {ComputeS256Challenge(verifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "action": {"allow"},
		"username": {u.Subject}, "password": {"hunter2hunter2"}, "tenant": {u.TenantID.String()},
	})
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if got := loc.Query().Get("error"); got != "invalid_target" {
		t.Errorf("error = %q, want invalid_target (location %s)", got, loc)
	}
	if loc.Query().Get("code") != "" {
		t.Error("a code was issued")
	}
}

// The exchanger gets a capability that acts for the agent's user, on the
// planes asked for, under the agent's grant, and no longer than its token.
func TestTokenExchange_IssuesACapabilityForTheUser(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	rec := postForm(f.h, "/oauth/token", exchangeForm(f.subject, auth.TokenPlaneData, auth.TokenPlaneIAM))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	a := decodeAnswer(t, rec.Body.Bytes())
	if a.IssuedTokenType != exchange.IssuedTokenTypeCapability || a.TokenType != exchange.TokenTypeBearer || a.ExpiresIn <= 0 {
		t.Errorf("answer = %+v", a)
	}
	v, err := f.verifier.Verify(t.Context(), limes.VerifyRequest{Token: a.AccessToken, Audience: auth.TokenPlaneIAM})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	c := v.Capability()
	if c.Subject.Subject != exchangerID {
		t.Errorf("held by %q, want the exchanging client %q, which the audit names as the relay", c.Subject.Subject, exchangerID)
	}
	if c.OnBehalfOf == nil || c.OnBehalfOf.Subject != u.UserID.String() || c.ClientID != agentWithMCPID {
		t.Errorf("acts for %+v as %q, want user %s as %q", c.OnBehalfOf, c.ClientID, u.UserID, agentWithMCPID)
	}
	if want, _ := auth.DelegatedOps([]string{grantScope}); !slices.Equal(c.Caveats.Ops, want) {
		t.Errorf("ops %v, want the grant's %v", c.Caveats.Ops, want)
	}
	subject, err := mcpVerifier().Verify(t.Context(), f.subject)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresAt.After(subject.ExpiresAt) {
		t.Errorf("the capability outlives its token: %v after %v", c.ExpiresAt, subject.ExpiresAt)
	}
}

// A client that authenticates in the Authorization header and fails is
// answered 401 with a challenge in its scheme (RFC 6749 §5.2); one that
// sends its secret in the form is answered 400.
func TestTokenExchange_ChallengesABasicClient(t *testing.T) {
	f := newExchangeFixture(t, sampleUser())
	form := exchangeForm(f.subject, auth.TokenPlaneData)
	form.Del("client_id")
	form.Del("client_secret")
	rec := postFormBasic(f.h, "/oauth/token", form, exchangerID, "wrong")
	if rec.Code != http.StatusUnauthorized || oauthError(t, rec.Body.Bytes()) != "invalid_client" ||
		!strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("status %d, challenge %q, body %s; want 401 invalid_client with a Basic challenge",
			rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body.String())
	}
}

// The client may authenticate with HTTP Basic instead of the form.
func TestTokenExchange_AcceptsBasicClientAuthentication(t *testing.T) {
	f := newExchangeFixture(t, sampleUser())
	form := exchangeForm(f.subject, auth.TokenPlaneData)
	form.Del("client_id")
	form.Del("client_secret")
	rec := postFormBasic(f.h, "/oauth/token", form, exchangerID, exchangerSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// The admin plane is the user's only with an admin role.
func TestTokenExchange_AdminNeedsAnAdminRole(t *testing.T) {
	user := sampleUser()
	f := newExchangeFixture(t, user)
	rec := postForm(f.h, "/oauth/token", exchangeForm(f.subject, auth.TokenPlaneAdmin))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "invalid_target" {
		t.Errorf("tenant.user → admin: status %d body %s, want 400 invalid_target", rec.Code, rec.Body.String())
	}

	admin := sampleUser()
	admin.Roles = []string{apiutil.RoleTenantAdmin}
	g := newExchangeFixture(t, admin)
	if rec := postForm(g.h, "/oauth/token", exchangeForm(g.subject, auth.TokenPlaneAdmin)); rec.Code != http.StatusOK {
		t.Errorf("tenant.admin → admin: status %d body %s, want 200", rec.Code, rec.Body.String())
	}
}

// The refusals, each from one changed input.
func TestTokenExchange_Refusals(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	mint := func(c issuer.AccessClaims) string {
		tok, _, err := iss.MintAccess(c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	planeToken := mint(issuer.AccessClaims{Subject: u.UserID.String(), TenantID: u.TenantID, Audience: auth.AudienceData, OAuthClientID: agentWithMCPID})
	noGrant := mint(issuer.AccessClaims{Subject: u.UserID.String(), TenantID: u.TenantID, Audience: auth.AudienceMCP})
	stranger := mint(issuer.AccessClaims{Subject: uuid.New().String(), TenantID: u.TenantID, Audience: auth.AudienceMCP, OAuthClientID: agentWithMCPID})
	otherTenant := mint(issuer.AccessClaims{Subject: u.UserID.String(), TenantID: uuid.New(), Audience: auth.AudienceMCP, OAuthClientID: agentWithMCPID})

	public := agentWithMCP()
	public.ClientID = "public-exchanger"
	f.store.clients[public.ClientID] = public
	unlisted := exchanger(t)
	unlisted.ClientID = "unlisted-exchanger"
	f.store.clients[unlisted.ClientID] = unlisted

	for _, tc := range []struct {
		name   string
		edit   func(url.Values)
		status int
		code   string
	}{
		{"a plane token as subject", func(v url.Values) { v.Set("subject_token", planeToken) }, http.StatusBadRequest, "invalid_grant"},
		{"a subject with no OAuth grant", func(v url.Values) { v.Set("subject_token", noGrant) }, http.StatusBadRequest, "invalid_grant"},
		{"a subject naming an unknown user", func(v url.Values) { v.Set("subject_token", stranger) }, http.StatusBadRequest, "invalid_grant"},
		{"a subject in another tenant", func(v url.Values) { v.Set("subject_token", otherTenant) }, http.StatusBadRequest, "invalid_grant"},
		{"garbage as subject", func(v url.Values) { v.Set("subject_token", "not-a-jwt") }, http.StatusBadRequest, "invalid_grant"},
		{"the mcp audience again", func(v url.Values) { v.Set("audience", auth.AudienceMCP) }, http.StatusBadRequest, "invalid_target"},
		{"a plane's canonical name", func(v url.Values) { v.Set("audience", auth.AudienceData) }, http.StatusBadRequest, "invalid_target"},
		{"no audience", func(v url.Values) { v.Del("audience") }, http.StatusBadRequest, "invalid_target"},
		{"another subject token type", func(v url.Values) {
			v.Set("subject_token_type", "urn:ietf:params:oauth:token-type:refresh_token")
		}, http.StatusBadRequest, "invalid_request"},
		{"a wrong secret", func(v url.Values) { v.Set("client_secret", "wrong") }, http.StatusBadRequest, "invalid_client"},
		{"no secret", func(v url.Values) { v.Del("client_secret") }, http.StatusBadRequest, "invalid_client"},
		{"an unknown client", func(v url.Values) { v.Set("client_id", "nobody") }, http.StatusBadRequest, "invalid_client"},
		{"a public client", func(v url.Values) { v.Set("client_id", public.ClientID) }, http.StatusBadRequest, "invalid_client"},
		{"a confidential client not allowed to exchange", func(v url.Values) { v.Set("client_id", unlisted.ClientID) }, http.StatusBadRequest, "unauthorized_client"},
		{"a client not allowed to exchange, with a wrong secret", func(v url.Values) {
			v.Set("client_id", unlisted.ClientID)
			v.Set("client_secret", "wrong")
		}, http.StatusBadRequest, "invalid_client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := exchangeForm(f.subject, auth.TokenPlaneData)
			tc.edit(form)
			rec := postForm(f.h, "/oauth/token", form)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.status, rec.Body.String())
			}
			if got := oauthError(t, rec.Body.Bytes()); got != tc.code {
				t.Errorf("error = %q, want %q", got, tc.code)
			}
		})
	}
}

func TestTokenExchange_UnsupportedUnlessConfigured(t *testing.T) {
	store := newMemStore()
	store.clients[exchangerID] = exchanger(t)
	h := testHandler(t, store, &memRefresh{}, sampleUser())
	rec := postForm(h, "/oauth/token", exchangeForm("x", auth.TokenPlaneData))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "unsupported_grant_type" {
		t.Errorf("status %d body %s, want 400 unsupported_grant_type", rec.Code, rec.Body.String())
	}
}

// A user disabled after consent is not exchanged for.
func TestTokenExchange_RefusesADisabledUser(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	u.Disabled = true
	disabled, _ := withExchange(t, testHandler(t, f.store, &memRefresh{}, u))
	rec := postForm(disabled, "/oauth/token", exchangeForm(f.subject, auth.TokenPlaneData))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "invalid_grant" {
		t.Errorf("status %d body %s, want 400 invalid_grant", rec.Code, rec.Body.String())
	}
}

// The exchanged token expires with its subject, not a full lifetime later.
func TestTokenExchange_ExpiresWithTheSubject(t *testing.T) {
	const subjectTTL = time.Minute
	u := sampleUser()
	f := newExchangeFixture(t, u)
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	short, subjectExp, err := iss.MintAccess(issuer.AccessClaims{
		Subject: u.UserID.String(), TenantID: u.TenantID, Audience: auth.AudienceMCP,
		OAuthClientID: agentWithMCPID, OAuthScopes: []string{grantScope}, TTL: subjectTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := postForm(f.h, "/oauth/token", exchangeForm(short, auth.TokenPlaneData))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	a := decodeAnswer(t, rec.Body.Bytes())
	v, err := f.verifier.Verify(t.Context(), limes.VerifyRequest{Token: a.AccessToken, Audience: auth.TokenPlaneData})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Capability().ExpiresAt; got.After(subjectExp) {
		t.Errorf("the capability expires %v, after its token's %v", got, subjectExp)
	}
	if a.ExpiresIn > int(subjectTTL/time.Second) {
		t.Errorf("expires_in = %d, want at most %d", a.ExpiresIn, int(subjectTTL/time.Second))
	}
}

// The exchange grant is capped on its own: the MCP server's sessions all count
// against one client, so the cap that guards a secret from guessing would
// refuse them. The other grants keep theirs.
func TestTokenExchange_HasItsOwnRateLimit(t *testing.T) {
	const (
		tokenLimit    = 1
		exchangeLimit = 3
	)
	u := sampleUser()
	f := newExchangeFixture(t, u)
	h := testHandlerCfg(t, f.store, &memRefresh{}, u, config.OAuthAS{
		Enabled: true, AuthorizationCodeTTL: time.Minute, AllowedRedirectSchemes: []string{"https", "desktop-agent"},
		TokenRateLimitPerMinute: tokenLimit, TokenExchangeRateLimitPerMinute: exchangeLimit,
	}).WithMCPResource(mcpResourceURL)
	h, _ = withExchange(t, h)

	for i := range exchangeLimit {
		if rec := postForm(h, "/oauth/token", exchangeForm(f.subject, auth.TokenPlaneData)); rec.Code != http.StatusOK {
			t.Fatalf("exchange %d: status %d, want 200 under its own cap (body %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := postForm(h, "/oauth/token", exchangeForm(f.subject, auth.TokenPlaneData)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("exchange past its cap: status %d, want 429", rec.Code)
	}

	refresh := url.Values{"grant_type": {GrantTypeRefreshToken}, "client_id": {exchangerID}, "refresh_token": {"x"}}
	postForm(h, "/oauth/token", refresh)
	if rec := postForm(h, "/oauth/token", refresh); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second refresh under a cap of %d: status %d, want 429", tokenLimit, rec.Code)
	}
}

// failingClients is a client store that cannot be read.
type failingClients struct {
	*memStore
	err error
}

func (f failingClients) GetClient(context.Context, string) (Client, error) { return Client{}, f.err }

// A client store that cannot be read is the server's failure: the client is
// told so, not that its credentials are wrong (RFC 6749 §5.2).
func TestTokenExchange_AStoreFailureIsTheServers(t *testing.T) {
	u := sampleUser()
	store := failingClients{memStore: newMemStore(), err: errors.New("pool closed")}
	h, _ := withExchange(t, testHandler(t, store, &memRefresh{}, u))
	rec := postForm(h, "/oauth/token", exchangeForm("subject", auth.TokenPlaneData))
	if rec.Code != http.StatusInternalServerError || oauthError(t, rec.Body.Bytes()) != "server_error" {
		t.Errorf("status %d body %s, want 500 server_error", rec.Code, rec.Body.String())
	}
}
