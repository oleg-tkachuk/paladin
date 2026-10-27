package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// downStore is a client store whose lookups fail as a database that cannot
// be reached does: an error that is not ErrClientNotFound or ErrCodeInvalid.
type downStore struct {
	*memStore
	getClient, consume bool
}

func (s downStore) GetClient(ctx context.Context, id string) (Client, error) {
	if s.getClient {
		return Client{}, errStoreDown
	}
	return s.memStore.GetClient(ctx, id)
}

func (s downStore) ConsumeCode(ctx context.Context, code string) (AuthCode, error) {
	if s.consume {
		return AuthCode{}, errStoreDown
	}
	return s.memStore.ConsumeCode(ctx, code)
}

// downUsers is a user store that cannot be reached.
type downUsers struct{}

func (downUsers) GetByID(context.Context, uuid.UUID) (authstore.User, error) {
	return authstore.User{}, errStoreDown
}
func (downUsers) GetBySubject(context.Context, uuid.UUID, string) (authstore.User, error) {
	return authstore.User{}, errStoreDown
}
func (downUsers) FindBySubjectGlobal(context.Context, string) ([]authstore.User, error) {
	return nil, errStoreDown
}

// downRefresh is a refresh-token store whose reads fail.
type downRefresh struct{ memRefresh }

func (*downRefresh) Get(context.Context, uuid.UUID) (authstore.RefreshToken, error) {
	return authstore.RefreshToken{}, errStoreDown
}

// brokenPolicy is a policy engine that cannot decide.
type brokenPolicy struct{}

func (brokenPolicy) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, errStoreDown
}

const outageVerifier = "outage-verifier-0123456789-0123456789-0123456789"

func outageCode(u authstore.User) AuthCode {
	return AuthCode{
		Code: "thecode", ClientID: publicClient().ClientID, UserID: u.UserID, TenantID: u.TenantID,
		RedirectURI: "desktop-agent://cb", CodeChallenge: ComputeS256Challenge(outageVerifier),
		ChallengeMethod: PKCEMethodS256, Scopes: []string{"paladin.read"}, Audience: "paladin-data",
		ExpiresAt: time.Now().Add(time.Minute),
	}
}

func consentForm(u authstore.User) url.Values {
	return url.Values{
		"client_id": {publicClient().ClientID}, "redirect_uri": {"desktop-agent://cb"}, "scope": {"paladin.read"},
		"code_challenge": {ComputeS256Challenge(outageVerifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "action": {"allow"},
		"username": {u.Subject}, "password": {"hunter2hunter2"}, "tenant": {u.TenantID.String()},
	}
}

func codeForm() url.Values {
	return url.Values{"grant_type": {"authorization_code"}, "code": {"thecode"}, "client_id": {publicClient().ClientID},
		"redirect_uri": {"desktop-agent://cb"}, "code_verifier": {outageVerifier}}
}

// An outage of a store or of the policy engine is the server failing, and is
// answered and logged as one. It read as a client error before — an unknown
// client, wrong credentials, a policy denial, a spent grant — so an outage
// looked like a wave of bad requests and left no line.
func TestOAuthAnswersAnOutageAsAServerError(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *Handler, store *memStore, u authstore.User)
		path  string
		form  func(u authstore.User) url.Values
		// redirected is a server error the client receives on its redirect,
		// once its redirect URI is known.
		redirected bool
	}{
		{name: "authorize: client lookup", path: "/oauth/authorize", form: consentForm,
			setup: func(h *Handler, s *memStore, _ authstore.User) { h.store = downStore{memStore: s, getClient: true} }},
		{name: "authorize: user lookup", path: "/oauth/authorize", form: consentForm, redirected: true,
			setup: func(h *Handler, _ *memStore, _ authstore.User) { h.users = downUsers{} }},
		{name: "authorize: policy engine", path: "/oauth/authorize", form: consentForm, redirected: true,
			setup: func(h *Handler, _ *memStore, _ authstore.User) { h.authz = brokenPolicy{} }},
		{name: "token: client lookup", path: "/oauth/token", form: func(authstore.User) url.Values { return codeForm() },
			setup: func(h *Handler, s *memStore, _ authstore.User) { h.store = downStore{memStore: s, getClient: true} }},
		{name: "token: code redemption", path: "/oauth/token", form: func(authstore.User) url.Values { return codeForm() },
			setup: func(h *Handler, s *memStore, _ authstore.User) { h.store = downStore{memStore: s, consume: true} }},
		{name: "token: user lookup", path: "/oauth/token", form: func(authstore.User) url.Values { return codeForm() },
			setup: func(h *Handler, s *memStore, u authstore.User) {
				_ = s.CreateCode(context.Background(), outageCode(u))
				h.users = downUsers{}
			}},
		{name: "refresh: token lookup", path: "/oauth/token",
			form: func(u authstore.User) url.Values {
				return url.Values{"grant_type": {"refresh_token"}, "client_id": {publicClient().ClientID}, "refresh_token": {mintRefresh(t, u.UserID, u.TenantID)}}
			},
			setup: func(h *Handler, _ *memStore, _ authstore.User) { h.refresh = &downRefresh{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := newMemStore()
			store.clients[publicClient().ClientID] = publicClient()
			u := sampleUser()
			h := testHandler(t, store, &memRefresh{}, u).WithAuthorizer(fakeAuthorizer{allow: true})
			c.setup(h, store, u)
			core, logs := observer.New(zap.ErrorLevel)
			ctx := logger.WithContext(t.Context(), zap.New(core))
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, c.path, strings.NewReader(c.form(u).Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			if c.path == "/oauth/authorize" {
				h.handleAuthorize(rec, req)
			} else {
				h.handleToken(rec, req)
			}

			if c.redirected {
				loc, _ := url.Parse(rec.Header().Get("Location"))
				if rec.Code != http.StatusFound || loc.Query().Get("error") != errServerError {
					t.Fatalf("status = %d, location = %s; want a redirect with %s", rec.Code, loc, errServerError)
				}
			} else if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), errServerError) {
				t.Fatalf("status = %d, body = %s; want 500 %s", rec.Code, rec.Body.String(), errServerError)
			}
			if logs.FilterMessage(serverErrorMessage).Len() != 1 {
				t.Errorf("logged %v, want one server-error line", logs.All())
			}
		})
	}
}
