package oauth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

// exchangeFixture is a handler that maps the MCP resource and lets the
// exchanger exchange, plus the agent's paladin-mcp token for u.
type exchangeFixture struct {
	h       *Handler
	store   *memStore
	subject string
}

func newExchangeFixture(t *testing.T, u authstore.User) exchangeFixture {
	t.Helper()
	store := newMemStore()
	store.clients[agentWithMCPID] = agentWithMCP()
	store.clients[exchangerID] = exchanger(t)
	h := testHandler(t, store, &memRefresh{}, u).
		WithMCPResource(mcpResourceURL).
		WithTokenExchange(mcpVerifier(), map[string]bool{exchangerID: true})
	tok := authorizeAndExchange(t, h, u, agentWithMCP(), mcpResourceURL)
	return exchangeFixture{h: h, store: store, subject: tok.AccessToken}
}

func exchangeForm(subject, audience string) url.Values {
	return url.Values{
		"grant_type": {GrantTypeTokenExchange}, "client_id": {exchangerID}, "client_secret": {exchangerSecret},
		"subject_token": {subject}, "subject_token_type": {TokenTypeAccessToken}, "audience": {audience},
	}
}

type exchangedClaims struct {
	Aud      string `json:"aud"`
	Sub      string `json:"sub"`
	Exp      int64  `json:"exp"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
	Act      struct {
		Sub string `json:"sub"`
	} `json:"act"`
}

func decodeExchanged(t *testing.T, token string) exchangedClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c exchangedClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatal(err)
	}
	return c
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

func TestTokenExchange_IssuesTheUsersTokenForAPlane(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	rec := postForm(f.h, "/oauth/token", exchangeForm(f.subject, auth.AudienceData))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.IssuedTokenType != TokenTypeAccessToken || resp.TokenType != "Bearer" || resp.ExpiresIn <= 0 {
		t.Errorf("response = %+v", resp)
	}
	if resp.Scope != grantScope {
		t.Errorf("scope = %q, want the agent's %q", resp.Scope, grantScope)
	}
	got := decodeExchanged(t, resp.AccessToken)
	want := decodeExchanged(t, f.subject)
	if got.Aud != auth.AudienceData || got.Sub != u.UserID.String() {
		t.Errorf("aud, sub = %q, %q; want %q, %q", got.Aud, got.Sub, auth.AudienceData, u.UserID)
	}
	if got.ClientID != agentWithMCPID || got.Scope != grantScope {
		t.Errorf("grant = %q %q, want the agent's %q %q", got.ClientID, got.Scope, agentWithMCPID, grantScope)
	}
	if got.Act.Sub != exchangerID {
		t.Errorf("act.sub = %q, want %q", got.Act.Sub, exchangerID)
	}
	if got.Exp > want.Exp {
		t.Errorf("exchanged exp %d outlives the subject's %d", got.Exp, want.Exp)
	}
}

// The client may authenticate with HTTP Basic instead of the form.
func TestTokenExchange_AcceptsBasicClientAuthentication(t *testing.T) {
	f := newExchangeFixture(t, sampleUser())
	form := exchangeForm(f.subject, auth.AudienceData)
	form.Del("client_id")
	form.Del("client_secret")
	rec := postFormBasic(f.h, "/oauth/token", form, exchangerID, exchangerSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTokenExchange_AdminNeedsAnAdminRole(t *testing.T) {
	user := sampleUser()
	f := newExchangeFixture(t, user)
	rec := postForm(f.h, "/oauth/token", exchangeForm(f.subject, auth.AudienceAdmin))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "invalid_target" {
		t.Errorf("tenant.user → admin: status %d body %s, want 400 invalid_target", rec.Code, rec.Body.String())
	}

	admin := sampleUser()
	admin.Roles = []string{apiutil.RoleTenantAdmin}
	g := newExchangeFixture(t, admin)
	if rec := postForm(g.h, "/oauth/token", exchangeForm(g.subject, auth.AudienceAdmin)); rec.Code != http.StatusOK {
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
		{"no audience", func(v url.Values) { v.Del("audience") }, http.StatusBadRequest, "invalid_target"},
		{"another subject token type", func(v url.Values) {
			v.Set("subject_token_type", "urn:ietf:params:oauth:token-type:refresh_token")
		}, http.StatusBadRequest, "invalid_request"},
		{"a wrong secret", func(v url.Values) { v.Set("client_secret", "wrong") }, http.StatusUnauthorized, "invalid_client"},
		{"no secret", func(v url.Values) { v.Del("client_secret") }, http.StatusUnauthorized, "invalid_client"},
		{"an unknown client", func(v url.Values) { v.Set("client_id", "nobody") }, http.StatusUnauthorized, "invalid_client"},
		{"a public client", func(v url.Values) { v.Set("client_id", public.ClientID) }, http.StatusUnauthorized, "invalid_client"},
		{"a confidential client not allowed to exchange", func(v url.Values) { v.Set("client_id", unlisted.ClientID) }, http.StatusBadRequest, "unauthorized_client"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := exchangeForm(f.subject, auth.AudienceData)
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
	rec := postForm(h, "/oauth/token", exchangeForm("x", auth.AudienceData))
	if rec.Code != http.StatusBadRequest || oauthError(t, rec.Body.Bytes()) != "unsupported_grant_type" {
		t.Errorf("status %d body %s, want 400 unsupported_grant_type", rec.Code, rec.Body.String())
	}
}

// A user disabled after consent is not exchanged for.
func TestTokenExchange_RefusesADisabledUser(t *testing.T) {
	u := sampleUser()
	f := newExchangeFixture(t, u)
	u.Disabled = true
	disabled := testHandler(t, f.store, &memRefresh{}, u).
		WithTokenExchange(mcpVerifier(), map[string]bool{exchangerID: true})
	rec := postForm(disabled, "/oauth/token", exchangeForm(f.subject, auth.AudienceData))
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
	rec := postForm(f.h, "/oauth/token", exchangeForm(short, auth.AudienceData))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exchangeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := decodeExchanged(t, resp.AccessToken).Exp; got > subjectExp.Unix() {
		t.Errorf("exchanged exp %d outlives the subject's %d", got, subjectExp.Unix())
	}
	if resp.ExpiresIn > int(subjectTTL/time.Second) {
		t.Errorf("expires_in = %d, want at most %d", resp.ExpiresIn, int(subjectTTL/time.Second))
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
	}).WithMCPResource(mcpResourceURL).WithTokenExchange(mcpVerifier(), map[string]bool{exchangerID: true})

	for i := range exchangeLimit {
		if rec := postForm(h, "/oauth/token", exchangeForm(f.subject, auth.AudienceData)); rec.Code != http.StatusOK {
			t.Fatalf("exchange %d: status %d, want 200 under its own cap (body %s)", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := postForm(h, "/oauth/token", exchangeForm(f.subject, auth.AudienceData)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("exchange past its cap: status %d, want 429", rec.Code)
	}

	refresh := url.Values{"grant_type": {GrantTypeRefreshToken}, "client_id": {exchangerID}, "refresh_token": {"x"}}
	postForm(h, "/oauth/token", refresh)
	if rec := postForm(h, "/oauth/token", refresh); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second refresh under a cap of %d: status %d, want 429", tokenLimit, rec.Code)
	}
}
