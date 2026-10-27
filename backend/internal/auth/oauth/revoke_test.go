package oauth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// revokeFixture is a handler that knows the public client, and a refresh token
// signed for the sample user the way /oauth/token mints one.
func revokeFixture(t *testing.T, refresh *memRefresh) (*Handler, *memAudit, string) {
	t.Helper()
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	audit := &memAudit{}
	h := testHandler(t, store, refresh, u).WithAudit(audit)
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	rt, _, err := iss.MintRefresh(issuer.RefreshClaims{Subject: u.UserID.String(), TenantID: u.TenantID, UserID: u.UserID, TokenID: uuid.NewV7()})
	if err != nil {
		t.Fatalf("mint refresh: %v", err)
	}
	return h, audit, rt
}

func revokeForm(clientID, token string) url.Values {
	return url.Values{"client_id": {clientID}, "token": {token}}
}

// The client's own refresh token ends its whole grant, and the trail says
// the client did it.
func TestRevoke_EndsTheClientsGrant(t *testing.T) {
	refresh := &memRefresh{grant: &authstore.OAuthGrant{ClientID: publicClient().ClientID, Audience: "paladin-data", Scopes: []string{"paladin.read"}}}
	h, audit, rt := revokeFixture(t, refresh)

	rec := postForm(h, RevokePath, revokeForm(publicClient().ClientID, rt))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if refresh.familyRevoked != 1 {
		t.Errorf("RevokeFamilyOf calls = %d, want the grant's family revoked once", refresh.familyRevoked)
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != actionGrantRevokedByClient {
		t.Fatalf("audit = %+v, want one %s row", audit.entries, actionGrantRevokedByClient)
	}
	if got := audit.entries[0].ResourceName; got != clientResource+publicClient().ClientID {
		t.Errorf("audited resource = %s", got)
	}
}

// RFC 7009 §2.2: a token the server cannot use is answered 200, since the
// client's aim — that it not work — already holds. Nothing is revoked.
func TestRevoke_AnUnusableTokenIsAnsweredOK(t *testing.T) {
	cases := []struct {
		name    string
		refresh *memRefresh
		token   func(rt string) string
	}{
		{"not a token at all", &memRefresh{}, func(string) string { return "not-a-jwt" }},
		{"a token the store no longer holds", &memRefresh{getErr: authstore.ErrNotFound}, func(rt string) string { return rt }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, audit, rt := revokeFixture(t, tc.refresh)
			rec := postForm(h, RevokePath, revokeForm(publicClient().ClientID, tc.token(rt)))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if tc.refresh.familyRevoked != 0 || len(audit.entries) != 0 {
				t.Errorf("revoked %d families and audited %d rows for an unusable token", tc.refresh.familyRevoked, len(audit.entries))
			}
		})
	}
}

// What the endpoint refuses, and how — none of it revokes anything.
func TestRevoke_Refusals(t *testing.T) {
	ownGrant := &authstore.OAuthGrant{ClientID: publicClient().ClientID}
	cases := []struct {
		name     string
		refresh  *memRefresh
		form     func(rt string) url.Values
		status   int
		errorKey string
	}{
		{
			"an unknown client", &memRefresh{grant: ownGrant},
			func(rt string) url.Values { return revokeForm("nobody", rt) },
			http.StatusUnauthorized, "invalid_client",
		},
		{
			"no token", &memRefresh{grant: ownGrant},
			func(string) url.Values { return url.Values{"client_id": {publicClient().ClientID}} },
			http.StatusBadRequest, "invalid_request",
		},
		{
			"an access token", &memRefresh{grant: ownGrant},
			func(rt string) url.Values {
				f := revokeForm(publicClient().ClientID, rt)
				f.Set(revokeParamTypeHint, tokenTypeHintAccess)
				return f
			},
			http.StatusBadRequest, errUnsupportedTokenType,
		},
		{
			"another client's grant", &memRefresh{grant: &authstore.OAuthGrant{ClientID: "someone-else"}},
			func(rt string) url.Values { return revokeForm(publicClient().ClientID, rt) },
			http.StatusBadRequest, "invalid_grant",
		},
		{
			"a console session", &memRefresh{},
			func(rt string) url.Values { return revokeForm(publicClient().ClientID, rt) },
			http.StatusBadRequest, "invalid_grant",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, rt := revokeFixture(t, tc.refresh)
			rec := postForm(h, RevokePath, tc.form(rt))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"`+tc.errorKey+`"`) {
				t.Fatalf("status = %d body = %s, want %d %s", rec.Code, rec.Body.String(), tc.status, tc.errorKey)
			}
			if tc.refresh.familyRevoked != 0 {
				t.Errorf("a refused request revoked %d families", tc.refresh.familyRevoked)
			}
		})
	}
}

func TestRevoke_OnlyPost(t *testing.T) {
	h, _, _ := revokeFixture(t, &memRefresh{})
	rec := httptest.NewRecorder()
	h.handleRevoke(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, RevokePath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}
