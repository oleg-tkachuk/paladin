package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
)

const auditCallerIP = "203.0.113.7"

func auditRequest(t *testing.T, path string, body string, contentType string) *http.Request {
	t.Helper()
	ctx := clientip.WithAddr(context.Background(), netip.MustParseAddr(auditCallerIP))
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	return req
}

// The Authorization Server writes a row for each change to who may reach the
// platform, naming the client and, where one has signed in, the user.
func TestOAuthWritesTheRowsThatChangeAccess(t *testing.T) {
	const form = "application/x-www-form-urlencoded"
	cases := []struct {
		name   string
		run    func(h *Handler, store *memStore, u authstore.User) *httptest.ResponseRecorder
		action string
		// signedIn is a row naming the user who acted.
		signedIn bool
		refused  bool
	}{
		{name: "a client registers itself", action: actionClientRegistered,
			run: func(h *Handler, _ *memStore, _ authstore.User) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.handleRegister(rec, auditRequest(t, "/oauth/register",
					`{"client_name":"agent","redirect_uris":["https://agent.example/cb"],"token_endpoint_auth_method":"none"}`, "application/json"))
				return rec
			}},
		{name: "a user grants a client", action: actionConsentGranted, signedIn: true,
			run: func(h *Handler, _ *memStore, u authstore.User) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.handleAuthorize(rec, auditRequest(t, "/oauth/authorize", consentForm(u).Encode(), form))
				return rec
			}},
		{name: "a user refuses a client", action: actionConsentDenied, refused: true,
			run: func(h *Handler, _ *memStore, u authstore.User) *httptest.ResponseRecorder {
				f := consentForm(u)
				f.Set("action", "deny")
				rec := httptest.NewRecorder()
				h.handleAuthorize(rec, auditRequest(t, "/oauth/authorize", f.Encode(), form))
				return rec
			}},
		{name: "a policy refuses a client", action: actionConsentDenied, signedIn: true, refused: true,
			run: func(h *Handler, _ *memStore, u authstore.User) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				h.WithAuthorizer(fakeAuthorizer{allow: false}).
					handleAuthorize(rec, auditRequest(t, "/oauth/authorize", consentForm(u).Encode(), form))
				return rec
			}},
		{name: "a client redeems its grant", action: actionGrantRedeemed, signedIn: true,
			run: func(h *Handler, s *memStore, u authstore.User) *httptest.ResponseRecorder {
				_ = s.CreateCode(context.Background(), outageCode(u))
				rec := httptest.NewRecorder()
				h.handleToken(rec, auditRequest(t, "/oauth/token", codeForm().Encode(), form))
				return rec
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newMemStore()
			store.clients[publicClient().ClientID] = publicClient()
			u := sampleUser()
			audit := &memAudit{}
			h := testHandler(t, store, &memRefresh{}, u).WithAudit(audit).WithAuthorizer(fakeAuthorizer{allow: true})
			rec := c.run(h, store, u)
			if rec.Code >= http.StatusInternalServerError {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if len(audit.entries) != 1 {
				t.Fatalf("rows = %d, want 1: %+v", len(audit.entries), audit.entries)
			}
			e := audit.entries[0]
			if e.Action != c.action || !strings.HasPrefix(e.ResourceName, clientResource) || e.SourceIP != auditCallerIP {
				t.Errorf("row = %+v", e)
			}
			if signedIn := e.ActorSubject == u.UserID.String() && e.ActorTenantID == u.TenantID; signedIn != c.signedIn {
				t.Errorf("actor = %q in %s, signed in %v, want %v", e.ActorSubject, e.ActorTenantID, signedIn, c.signedIn)
			}
			if (e.ErrorMessage != "") != c.refused {
				t.Errorf("error message %q, refused %v", e.ErrorMessage, c.refused)
			}
			if c.action == actionGrantRedeemed || c.action == actionConsentGranted {
				var d grantDetail
				if err := json.Unmarshal(e.RequestJSON, &d); err != nil || d.Audience == "" || len(d.Scopes) == 0 {
					t.Errorf("grant detail = %s (%v)", e.RequestJSON, err)
				}
			}
		})
	}
}

// A grant whose tokens could not be issued writes no row: nothing was
// granted. A refresh writes none either, as a console session's does not.
func TestOAuthWritesNoRowForWhatChangedNothing(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	audit := &memAudit{}
	h := testHandler(t, store, &memRefresh{}, u).WithAudit(audit)

	h.users = downUsers{}
	_ = store.CreateCode(context.Background(), outageCode(u))
	h.handleToken(httptest.NewRecorder(), auditRequest(t, "/oauth/token", codeForm().Encode(), "application/x-www-form-urlencoded"))

	h.users = memUsers{u: u}
	refresh := &memRefresh{grant: &authstore.OAuthGrant{ClientID: publicClient().ClientID, Audience: "paladin-data"}}
	h.refresh = refresh
	rec := httptest.NewRecorder()
	h.handleToken(rec, auditRequest(t, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {publicClient().ClientID},
		"refresh_token": {mintRefresh(t, u.UserID, u.TenantID)},
	}.Encode(), "application/x-www-form-urlencoded"))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(audit.entries) != 0 {
		t.Errorf("rows = %+v, want none", audit.entries)
	}
}
