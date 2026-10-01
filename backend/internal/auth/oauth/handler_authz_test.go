package oauth

// The guards on the token endpoint that decide whether a caller gets a token
// they should not have. The existing suite covers the happy path, PKCE
// mismatch, code single-use and refresh reuse; these are the ones underneath
// it, and every one of them was a surviving mutant.
//
// They share a shape: the failure is not an error, it is a successful token
// response. Nothing in the system objects to issuing a valid token to the
// wrong party — that is what the guard was for.

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// confidentialClient has a secret, so authenticateClient must verify it.
func confidentialClient(t *testing.T, secret string) Client {
	t.Helper()
	hash, err := auth.HashApiKeySecret(secret)
	if err != nil {
		t.Fatalf("hash secret: %v", err)
	}
	return Client{
		ClientID: "svc-client", ClientName: "Service",
		RedirectURIs:  []string{"https://svc.example/cb"},
		AllowedScopes: []string{"paladin.read"}, AllowedAudiences: []string{"paladin-data"},
		SecretHash: hash, Public: false,
	}
}

func seedCode(t *testing.T, store *memStore, code, clientID, redirectURI, verifier string, u authstore.User) {
	t.Helper()
	err := store.CreateCode(context.Background(), AuthCode{
		Code: code, ClientID: clientID, UserID: u.UserID, TenantID: u.TenantID,
		RedirectURI: redirectURI, CodeChallenge: ComputeS256Challenge(verifier),
		ChallengeMethod: PKCEMethodS256, Scopes: []string{"paladin.read"},
		Audience: "paladin-data", ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

// An authorization code is bound to the client it was issued to AND the
// redirect_uri it was issued for. The check is a disjunction, and as a
// conjunction it fires only when both differ — so a code intercepted by a
// second registered client redeems cleanly as long as it presents the same
// redirect_uri. That is the code-substitution attack the binding exists to
// stop, and it ends in a 200 with a valid token.
func TestToken_CodeIsBoundToBothClientAndRedirectURI(t *testing.T) {
	verifier := strings.Repeat("a", 50)
	u := sampleUser()

	cases := map[string]struct{ clientID, redirectURI string }{
		"wrong client, right redirect_uri": {"other-client", "claude-desktop://cb"},
		"right client, wrong redirect_uri": {"claude-desktop", "https://attacker.example/cb"},
		"both wrong":                       {"other-client", "https://attacker.example/cb"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newMemStore()
			store.clients[publicClient().ClientID] = publicClient()
			// A second registered client, so the request is well-formed and
			// only the binding stands between it and a token.
			other := publicClient()
			other.ClientID = "other-client"
			other.RedirectURIs = []string{"claude-desktop://cb", "https://attacker.example/cb"}
			store.clients[other.ClientID] = other

			h := testHandler(t, store, &memRefresh{}, u)
			seedCode(t, store, "bound", "claude-desktop", "claude-desktop://cb", verifier, u)

			rec := postForm(h, "/oauth/token", url.Values{
				"grant_type": {"authorization_code"}, "code": {"bound"},
				"client_id": {tc.clientID}, "redirect_uri": {tc.redirectURI},
				"code_verifier": {verifier},
			})
			if rec.Code == http.StatusOK {
				t.Fatalf("token issued for a code bound elsewhere (client=%q redirect=%q)", tc.clientID, tc.redirectURI)
			}
			if !strings.Contains(rec.Body.String(), "invalid_grant") {
				t.Errorf("body = %s, want invalid_grant", rec.Body.String())
			}
		})
	}
}

// A confidential client must present its secret. Three guards stand in the
// way and each survived: the public/no-secret short-circuit, the "is a secret
// present at all" check, and the comparison itself. Inverting the last one
// makes a *wrong* secret the accepted one.
func TestToken_ConfidentialClientMustPresentItsSecret(t *testing.T) {
	const secret = "correct-horse-battery-staple"
	verifier := strings.Repeat("a", 50)
	u := sampleUser()

	newFixture := func(t *testing.T) (*Handler, *memStore) {
		t.Helper()
		store := newMemStore()
		c := confidentialClient(t, secret)
		store.clients[c.ClientID] = c
		h := testHandler(t, store, &memRefresh{}, u)
		seedCode(t, store, "conf", c.ClientID, c.RedirectURIs[0], verifier, u)
		return h, store
	}

	base := func() url.Values {
		return url.Values{
			"grant_type": {"authorization_code"}, "code": {"conf"},
			"client_id": {"svc-client"}, "redirect_uri": {"https://svc.example/cb"},
			"code_verifier": {verifier},
		}
	}

	t.Run("no secret is refused", func(t *testing.T) {
		h, _ := newFixture(t)
		if rec := postForm(h, "/oauth/token", base()); rec.Code == http.StatusOK {
			t.Fatal("token issued to a confidential client that presented no secret")
		}
	})

	t.Run("wrong secret is refused", func(t *testing.T) {
		h, _ := newFixture(t)
		form := base()
		form.Set("client_secret", "not-the-secret")
		if rec := postForm(h, "/oauth/token", form); rec.Code == http.StatusOK {
			t.Fatal("token issued to a confidential client that presented the wrong secret")
		}
	})

	// The short-circuit is a disjunction for a reason: a client with no
	// secret on file has nothing to prove, whether or not it is flagged
	// public. As a conjunction such a client would be asked for a secret it
	// was never issued, and could never obtain a token at all.
	t.Run("a client with no secret on file needs none", func(t *testing.T) {
		store := newMemStore()
		c := confidentialClient(t, secret)
		c.SecretHash = nil // registered without one
		c.Public = false
		store.clients[c.ClientID] = c
		h := testHandler(t, store, &memRefresh{}, u)
		seedCode(t, store, "conf", c.ClientID, c.RedirectURIs[0], verifier, u)

		form := base()
		if rec := postForm(h, "/oauth/token", form); rec.Code != http.StatusOK {
			t.Fatalf("a client with no secret on file was refused: status=%d body=%s",
				rec.Code, rec.Body.String())
		}
	})

	t.Run("correct secret is accepted", func(t *testing.T) {
		h, _ := newFixture(t)
		form := base()
		form.Set("client_secret", secret)
		rec := postForm(h, "/oauth/token", form)
		if rec.Code != http.StatusOK {
			t.Fatalf("correct secret refused: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

// A disabled user must not be able to redeem a code that was issued while
// they were still active — deactivation has to take effect on the next
// exchange, not on the next login.
func TestToken_DisabledUserCannotRedeemACode(t *testing.T) {
	verifier := strings.Repeat("a", 50)
	u := sampleUser()
	u.Disabled = true

	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	h := testHandler(t, store, &memRefresh{}, u)
	seedCode(t, store, "disabled", "claude-desktop", "claude-desktop://cb", verifier, u)

	rec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {"disabled"},
		"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"},
		"code_verifier": {verifier},
	})
	if rec.Code == http.StatusOK {
		t.Fatal("a disabled user redeemed an authorization code")
	}
	if !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Errorf("body = %s, want invalid_grant", rec.Body.String())
	}
}

// registeredRedirect is exact string comparison, which is what RFC 6749 §3.1.2.3
// requires. Prefix or substring matching is the classic weakening, and it
// hands the code to whoever controls the path — so the near-misses matter
// more than the obvious mismatch.
func TestRegisteredRedirect_IsExact(t *testing.T) {
	c := Client{RedirectURIs: []string{"https://app.example/cb", "claude-desktop://cb"}}

	for _, want := range c.RedirectURIs {
		if got, ok := registeredRedirect(c, want); !ok || got != want {
			t.Errorf("registered %q: got (%q, %v)", want, got, ok)
		}
	}
	for _, bad := range []string{
		"",
		"https://app.example",                  // prefix of a registered one
		"https://app.example/cb/extra",         // registered one is a prefix of it
		"https://app.example/cb?next=/",        // query appended
		"https://app.example.attacker.test/cb", // registered host is a prefix of the host
		"https://attacker.test/?u=https://app.example/cb", // registered one as a substring
		"HTTPS://APP.EXAMPLE/CB",                          // case differs
	} {
		if _, ok := registeredRedirect(c, bad); ok {
			t.Errorf("unregistered %q was accepted", bad)
		}
	}
}

// scopesSubset is what stops a client asking for more than it was granted.
// The empty-want short-circuit is deliberate (no scope requested is not an
// escalation), and everything else must be contained.
func TestScopesSubset(t *testing.T) {
	allowed := []string{"paladin.read", "paladin.write"}

	for _, want := range [][]string{nil, {}, {"paladin.read"}, {"paladin.read", "paladin.write"}} {
		if !scopesSubset(want, allowed) {
			t.Errorf("scopesSubset(%v, %v) = false, want true", want, allowed)
		}
	}
	for _, want := range [][]string{
		{"paladin.admin"},
		{"paladin.read", "paladin.admin"},
		{"paladin.rea"},   // prefix of an allowed scope
		{"paladin.readx"}, // allowed scope is a prefix of it
		{""},
	} {
		if scopesSubset(want, allowed) {
			t.Errorf("scopesSubset(%v, %v) = true — that is a scope escalation", want, allowed)
		}
	}
	// Nothing allowed means nothing but the empty request is grantable.
	if scopesSubset([]string{"paladin.read"}, nil) {
		t.Error("a scope was granted from an empty allow-list")
	}
}

// audienceFor picks the token's audience. The resource indicator only wins
// when it is non-empty and registered; dropping that guard lets an unset
// resource match a client that happens to carry an empty allowed audience,
// minting a token for the empty audience.
func TestAudienceFor(t *testing.T) {
	c := Client{AllowedAudiences: []string{"paladin-data", "paladin-admin"}}

	if got := audienceFor(c, "paladin-admin"); got != "paladin-admin" {
		t.Errorf("registered resource: got %q, want paladin-admin", got)
	}
	if got := audienceFor(c, "paladin-mcp"); got != "paladin-data" {
		t.Errorf("unregistered resource must fall back to the first allowed: got %q", got)
	}
	if got := audienceFor(c, ""); got != "paladin-data" {
		t.Errorf("no resource must fall back to the first allowed: got %q", got)
	}
	// The `resource != ""` guard only shows itself when the empty entry is not
	// first: without it the loop matches the empty resource against the empty
	// allowed audience and returns that, instead of falling back to the real
	// one. The token would then carry no audience at all.
	if got := audienceFor(Client{AllowedAudiences: []string{"paladin-data", ""}}, ""); got != "paladin-data" {
		t.Errorf("empty resource matched an empty allowed audience: got %q, want paladin-data", got)
	}
	if got := audienceFor(Client{}, ""); got != auth.AudienceData {
		t.Errorf("no allowed audiences: got %q, want the %s default", got, auth.AudienceData)
	}
}
