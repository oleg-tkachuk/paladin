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
)

const (
	grantScope    = "paladin.read"
	otherClientID = "other-agent"
)

// adminClient may be issued paladin-admin as well as paladin-data, so a
// grant for the second audience is one a rotation could lose.
func adminClient() Client {
	c := publicClient()
	c.AllowedAudiences = []string{auth.AudienceData, auth.AudienceAdmin}
	return c
}

func otherClient() Client {
	c := publicClient()
	c.ClientID = otherClientID
	return c
}

// authorizeAndExchange runs /authorize and the code exchange for client,
// asking for resource, and returns the token response.
func authorizeAndExchange(t *testing.T, h *Handler, u authstore.User, client Client, resource string) tokenResponse {
	t.Helper()
	verifier := strings.Repeat("a", 50)
	redirect := client.RedirectURIs[0]
	arec := postForm(h, "/oauth/authorize", url.Values{
		"client_id": {client.ClientID}, "redirect_uri": {redirect}, "scope": {grantScope},
		"resource":       {resource},
		"code_challenge": {ComputeS256Challenge(verifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "action": {"allow"},
		"username": {"svc@acme"}, "password": {"hunter2hunter2"}, "tenant": {u.TenantID.String()},
	})
	loc, _ := url.Parse(arec.Header().Get("Location"))
	code := loc.Query().Get("code")
	if arec.Code != http.StatusFound || code == "" {
		t.Fatalf("authorize: status=%d location=%s", arec.Code, arec.Header().Get("Location"))
	}
	trec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client.ClientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier},
	})
	if trec.Code != http.StatusOK {
		t.Fatalf("token: status=%d body=%s", trec.Code, trec.Body.String())
	}
	var tok tokenResponse
	if err := json.Unmarshal(trec.Body.Bytes(), &tok); err != nil {
		t.Fatalf("token body: %v", err)
	}
	return tok
}

func refreshAs(h *Handler, clientID, refreshToken string) (int, tokenResponse) {
	rec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refreshToken},
	})
	var tok tokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	return rec.Code, tok
}

// accessClaims reads the claims of an access token this handler minted.
type grantClaims struct {
	Aud      string `json:"aud"`
	ClientID string `json:"client_id"`
	Scope    string `json:"scope"`
}

func accessClaims(t *testing.T, token string) grantClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("access token is not a JWT: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims grantClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("claims: %v", err)
	}
	return claims
}

func accessAudience(t *testing.T, token string) string {
	t.Helper()
	return accessClaims(t, token).Aud
}

func TestToken_RefreshReissuesTheGrant(t *testing.T) {
	store := newMemStore()
	store.clients[adminClient().ClientID] = adminClient()
	u := sampleUser()
	u.Roles = []string{apiutil.RoleTenantAdmin}
	h := testHandler(t, store, &memRefresh{}, u)

	first := authorizeAndExchange(t, h, u, adminClient(), auth.AudienceAdmin)
	if got := accessAudience(t, first.AccessToken); got != auth.AudienceAdmin {
		t.Fatalf("code exchange audience = %q, want %q", got, auth.AudienceAdmin)
	}

	status, rotated := refreshAs(h, adminClient().ClientID, first.RefreshToken)
	if status != http.StatusOK {
		t.Fatalf("refresh status = %d", status)
	}
	if got := accessAudience(t, rotated.AccessToken); got != auth.AudienceAdmin {
		t.Errorf("rotated audience = %q, want %q: a rotation must not move the grant", got, auth.AudienceAdmin)
	}
	if rotated.Scope != grantScope {
		t.Errorf("rotated scope = %q, want %q", rotated.Scope, grantScope)
	}
}

func TestToken_RefreshRefusesAnotherClient(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	store.clients[otherClientID] = otherClient()
	u := sampleUser()
	refresh := &memRefresh{}
	h := testHandler(t, store, refresh, u)

	tok := authorizeAndExchange(t, h, u, publicClient(), "")
	issued := refresh.inserted

	status, _ := refreshAs(h, otherClientID, tok.RefreshToken)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 invalid_grant", status)
	}
	if refresh.inserted != issued {
		t.Error("a refused refresh must mint nothing")
	}
}

func TestToken_RefreshRefusesAConsoleToken(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	refresh := &memRefresh{}
	h := testHandler(t, store, refresh, u)

	// A console login's refresh token: same issuer and signature, no grant.
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	rt, _, err := iss.MintRefresh(issuer.RefreshClaims{Subject: u.UserID.String(), TenantID: u.TenantID, UserID: u.UserID, TokenID: uuid.NewV7()})
	if err != nil {
		t.Fatalf("mint refresh: %v", err)
	}

	status, _ := refreshAs(h, publicClient().ClientID, rt)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 invalid_grant", status)
	}
	if refresh.inserted != 0 {
		t.Error("a refused refresh must mint nothing")
	}
}

// The access token names the client and the consented scopes, so every
// plane enforces the consent, not only /authorize (ADR-0035). A rotation
// re-issues them.
func TestToken_AccessTokenCarriesTheGrant(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)

	first := authorizeAndExchange(t, h, u, publicClient(), "")
	_, rotated := refreshAs(h, publicClient().ClientID, first.RefreshToken)
	for name, tok := range map[string]string{"code exchange": first.AccessToken, "rotation": rotated.AccessToken} {
		c := accessClaims(t, tok)
		if c.ClientID != publicClient().ClientID || c.Scope != grantScope {
			t.Errorf("%s: client_id=%q scope=%q, want %q %q", name, c.ClientID, c.Scope, publicClient().ClientID, grantScope)
		}
	}
}
