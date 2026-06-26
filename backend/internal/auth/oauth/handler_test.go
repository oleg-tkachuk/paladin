package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

const testSigningKey = "test-signing-key-at-least-32-bytes-long!!"

// ─── in-memory fakes ─────────────────────────────────────────────────────────

type memStore struct {
	clients map[string]Client
	codes   map[string]AuthCode // keyed by plaintext code
}

func newMemStore() *memStore {
	return &memStore{clients: map[string]Client{}, codes: map[string]AuthCode{}}
}
func (m *memStore) UpsertClient(_ context.Context, c Client) error {
	m.clients[c.ClientID] = c
	return nil
}
func (m *memStore) GetClient(_ context.Context, id string) (Client, error) {
	c, ok := m.clients[id]
	if !ok {
		return Client{}, ErrClientNotFound
	}
	return c, nil
}
func (m *memStore) CreateCode(_ context.Context, c AuthCode) error { m.codes[c.Code] = c; return nil }
func (m *memStore) ConsumeCode(_ context.Context, code string) (AuthCode, error) {
	c, ok := m.codes[code]
	if !ok || time.Now().After(c.ExpiresAt) {
		return AuthCode{}, ErrCodeInvalid
	}
	delete(m.codes, code) // single-use
	return c, nil
}
func (m *memStore) PurgeExpiredCodes(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

type memUsers struct{ u authstore.User }

func (m memUsers) GetByID(_ context.Context, id uuid.UUID) (authstore.User, error) {
	if id == m.u.UserID {
		return m.u, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}
func (m memUsers) GetBySubject(_ context.Context, _ uuid.UUID, subject string) (authstore.User, error) {
	if subject == m.u.Subject {
		return m.u, nil
	}
	return authstore.User{}, authstore.ErrNotFound
}
func (m memUsers) FindBySubjectGlobal(_ context.Context, subject string) ([]authstore.User, error) {
	if subject == m.u.Subject {
		return []authstore.User{m.u}, nil
	}
	return nil, nil
}

type memRefresh struct{ inserted, revoked int }

func (m *memRefresh) Insert(context.Context, authstore.RefreshToken) error { m.inserted++; return nil }
func (m *memRefresh) Get(_ context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	return authstore.RefreshToken{JTI: jti, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (m *memRefresh) Revoke(context.Context, uuid.UUID) error                 { m.revoked++; return nil }
func (m *memRefresh) RevokeForUser(context.Context, uuid.UUID) (int64, error) { return 0, nil }
func (m *memRefresh) PurgeExpired(context.Context, time.Time) (int64, error)  { return 0, nil }

func testHandler(t *testing.T, store Store, refresh authstore.RefreshTokenRepository, u authstore.User) *Handler {
	t.Helper()
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	dec := &auth.RefreshDecoder{Verifier: &auth.JWTVerifier{Key: []byte(testSigningKey), ExpectedIssuer: "paladin", ExpectedAudience: auth.AudienceIAM}}
	cfg := config.OAuthAS{Enabled: true, DynamicRegistration: true, AuthorizationCodeTTL: time.Minute, AllowedRedirectSchemes: []string{"https", "claude-desktop"}}
	return NewHandler(cfg, store, memUsers{u: u}, refresh, iss, dec, zap.NewNop())
}

func sampleUser() authstore.User {
	hash, _ := auth.HashPassword("hunter2hunter2")
	return authstore.User{UserID: uuid.New(), TenantID: uuid.New(), Subject: "svc@acme", PasswordHash: hash, Roles: []string{"tenant.user"}}
}

func publicClient() Client {
	return Client{ClientID: "claude-desktop", ClientName: "Claude Desktop", RedirectURIs: []string{"claude-desktop://cb"}, AllowedScopes: []string{"paladin.read"}, AllowedAudiences: []string{"paladin-data"}, Public: true}
}

// ─── tests ───────────────────────────────────────────────────────────────────

func TestToken_AuthCodeHappyPath(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	refresh := &memRefresh{}
	h := testHandler(t, store, refresh, u)

	verifier := strings.Repeat("a", 50)
	_ = store.CreateCode(context.Background(), AuthCode{
		Code: "thecode", ClientID: "claude-desktop", UserID: u.UserID, TenantID: u.TenantID,
		RedirectURI: "claude-desktop://cb", CodeChallenge: ComputeS256Challenge(verifier),
		ChallengeMethod: PKCEMethodS256, Scopes: []string{"paladin.read"}, Audience: "paladin-data",
		ExpiresAt: time.Now().Add(time.Minute),
	})

	form := url.Values{"grant_type": {"authorization_code"}, "code": {"thecode"}, "client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "code_verifier": {verifier}}
	rec := postForm(h, "/oauth/token", form)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp tokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.AccessToken == "" || resp.RefreshToken == "" || resp.TokenType != "Bearer" {
		t.Fatalf("bad token response: %+v", resp)
	}
	if refresh.inserted != 1 {
		t.Errorf("refresh row not stored")
	}
}

func TestToken_PKCEMismatch(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)

	_ = store.CreateCode(context.Background(), AuthCode{
		Code: "c2", ClientID: "claude-desktop", RedirectURI: "claude-desktop://cb",
		CodeChallenge: ComputeS256Challenge(strings.Repeat("a", 50)), ChallengeMethod: PKCEMethodS256,
		Audience: "paladin-data", UserID: u.UserID, TenantID: u.TenantID, ExpiresAt: time.Now().Add(time.Minute),
	})
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"c2"}, "client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "code_verifier": {strings.Repeat("b", 50)}}
	rec := postForm(h, "/oauth/token", form)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_grant") {
		t.Fatalf("status = %d body=%s, want 400 invalid_grant", rec.Code, rec.Body.String())
	}
}

func TestToken_CodeSingleUse(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)
	verifier := strings.Repeat("a", 50)
	_ = store.CreateCode(context.Background(), AuthCode{
		Code: "c3", ClientID: "claude-desktop", RedirectURI: "claude-desktop://cb",
		CodeChallenge: ComputeS256Challenge(verifier), ChallengeMethod: PKCEMethodS256,
		Audience: "paladin-data", UserID: u.UserID, TenantID: u.TenantID, ExpiresAt: time.Now().Add(time.Minute),
	})
	form := url.Values{"grant_type": {"authorization_code"}, "code": {"c3"}, "client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "code_verifier": {verifier}}
	if rec := postForm(h, "/oauth/token", form); rec.Code != http.StatusOK {
		t.Fatalf("first exchange failed: %d", rec.Code)
	}
	if rec := postForm(h, "/oauth/token", form); rec.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400 (single-use)", rec.Code)
	}
}

func TestAuthorize_GetRendersConsent(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	h := testHandler(t, store, &memRefresh{}, sampleUser())

	q := url.Values{"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "scope": {"paladin.read"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}, "state": {"st"}}
	rec := httptest.NewRecorder()
	h.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Authorize access") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorize_PostAllowIssuesCode(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)

	form := url.Values{
		"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "scope": {"paladin.read"},
		"code_challenge": {ComputeS256Challenge(strings.Repeat("a", 50))}, "code_challenge_method": {"S256"},
		"state": {"st"}, "action": {"allow"}, "username": {"svc@acme"}, "password": {"hunter2hunter2"},
		"tenant": {u.TenantID.String()},
	}
	rec := postForm(h, "/oauth/authorize", form)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s, want 302", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("code") == "" || loc.Query().Get("state") != "st" {
		t.Fatalf("redirect missing code/state: %s", rec.Header().Get("Location"))
	}
}

func TestAuthorize_PostDenyRedirectsError(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	h := testHandler(t, store, &memRefresh{}, sampleUser())
	form := url.Values{
		"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}, "action": {"deny"},
	}
	rec := postForm(h, "/oauth/authorize", form)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Query().Get("error") != "access_denied" {
		t.Fatalf("status=%d loc=%s, want 302 access_denied", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAuthorize_UnknownClient(t *testing.T) {
	h := testHandler(t, newMemStore(), &memRefresh{}, sampleUser())
	q := url.Values{"client_id": {"nope"}, "redirect_uri": {"https://x"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}}
	rec := httptest.NewRecorder()
	h.handleAuthorize(rec, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_client") {
		t.Fatalf("status=%d body=%s, want 400 invalid_client", rec.Code, rec.Body.String())
	}
}

func TestRegister_DCR(t *testing.T) {
	store := newMemStore()
	h := testHandler(t, store, &memRefresh{}, sampleUser())
	body := `{"client_name":"Cursor","redirect_uris":["https://cursor.sh/cb"],"token_endpoint_auth_method":"none"}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	h.handleRegister(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var resp registerResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.HasPrefix(resp.ClientID, "paladin-") || resp.ClientSecret != "" {
		t.Fatalf("bad DCR response (public client must have no secret): %+v", resp)
	}
	if _, ok := store.clients[resp.ClientID]; !ok {
		t.Error("registered client not persisted")
	}
}

func postForm(h *Handler, path string, form url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	switch path {
	case "/oauth/token":
		h.handleToken(rec, r)
	case "/oauth/authorize":
		h.handleAuthorize(rec, r)
	}
	return rec
}
