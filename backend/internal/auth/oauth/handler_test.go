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

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
	cedar "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
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

type memRefresh struct {
	inserted, revoked, userRevoked, familyRevoked int
	getErr                                        error // when set, Get returns it (e.g. ErrTokenRevoked)
}

func (m *memRefresh) Insert(context.Context, authstore.RefreshToken) error { m.inserted++; return nil }
func (m *memRefresh) Get(_ context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	if m.getErr != nil {
		return authstore.RefreshToken{}, m.getErr
	}
	return authstore.RefreshToken{JTI: jti, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (m *memRefresh) Revoke(context.Context, uuid.UUID) error { m.revoked++; return nil }
func (m *memRefresh) RevokeForUser(context.Context, uuid.UUID) (int64, error) {
	m.userRevoked++
	return 1, nil
}
func (m *memRefresh) RevokeFamilyOf(context.Context, uuid.UUID) (int64, error) {
	m.familyRevoked++
	return 1, nil
}
func (m *memRefresh) PurgeExpired(context.Context, time.Time) (int64, error) { return 0, nil }

// memAudit records audit entries for the reuse-detection test.
type memAudit struct{ entries []admindomain.AuditEntry }

func (m *memAudit) Insert(_ context.Context, e admindomain.AuditEntry) error {
	m.entries = append(m.entries, e)
	return nil
}

func testHandler(t *testing.T, store Store, refresh authstore.RefreshTokenRepository, u authstore.User) *Handler {
	t.Helper()
	return testHandlerCfg(t, store, refresh, u, config.OAuthAS{
		Enabled: true, DynamicRegistration: true, AuthorizationCodeTTL: time.Minute,
		AllowedRedirectSchemes: []string{"https", "claude-desktop"},
	})
}

func testHandlerCfg(t *testing.T, store Store, refresh authstore.RefreshTokenRepository, u authstore.User, cfg config.OAuthAS) *Handler {
	t.Helper()
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	dec := &auth.RefreshDecoder{Verifier: &auth.JWTVerifier{Key: []byte(testSigningKey), ExpectedIssuer: "paladin", ExpectedAudience: auth.AudienceIAM}}
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

func TestToken_RateLimited(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	// capacity 1 → second token request is throttled before any DB work.
	h := testHandlerCfg(t, store, &memRefresh{}, u, config.OAuthAS{
		Enabled: true, TokenRateLimitPerMinute: 1,
		AllowedRedirectSchemes: []string{"https", "claude-desktop"},
	})
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"claude-desktop"},
		"code": {"nope"}, "redirect_uri": {"claude-desktop://cb"},
		"code_verifier": {strings.Repeat("a", 50)},
	}
	// First request consumes the single token (fails on bad code with 400).
	if rec := postForm(h, "/oauth/token", form); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("first request should not be rate-limited")
	}
	// Second request is throttled before processing.
	rec := postForm(h, "/oauth/token", form)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 must carry Retry-After")
	}
}

func TestToken_CORS(t *testing.T) {
	h := testHandlerCfg(t, newMemStore(), &memRefresh{}, sampleUser(), config.OAuthAS{
		Enabled: true, TokenEndpointAllowedOrigins: []string{"https://app.example.com"},
	})

	t.Run("preflight from allowed origin", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodOptions, "/oauth/token", nil)
		r.Header.Set("Origin", "https://app.example.com")
		h.handleToken(rec, r)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("preflight status = %d, want 204", rec.Code)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
			t.Errorf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
		}
		if rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Error("missing Allow-Methods")
		}
	})

	t.Run("disallowed origin gets no ACAO", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodOptions, "/oauth/token", nil)
		r.Header.Set("Origin", "https://evil.example.com")
		h.handleToken(rec, r)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("disallowed origin must not receive Access-Control-Allow-Origin")
		}
	})
}

func TestFullFlow_AuthorizeTokenRefresh(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	h := testHandler(t, store, &memRefresh{}, u)
	verifier := strings.Repeat("a", 50)

	// 1. /authorize POST allow → 302 with code.
	aform := url.Values{
		"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"},
		"scope":          {"paladin.read"},
		"code_challenge": {ComputeS256Challenge(verifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "action": {"allow"},
		"username": {"svc@acme"}, "password": {"hunter2hunter2"}, "tenant": {u.TenantID.String()},
	}
	arec := postForm(h, "/oauth/authorize", aform)
	if arec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d body=%s", arec.Code, arec.Body.String())
	}
	loc, _ := url.Parse(arec.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatal("authorize did not return a code")
	}

	// 2. /token authorization_code → access + refresh.
	trec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"claude-desktop"},
		"redirect_uri": {"claude-desktop://cb"}, "code_verifier": {verifier},
	})
	if trec.Code != http.StatusOK {
		t.Fatalf("token status = %d body=%s", trec.Code, trec.Body.String())
	}
	var tok tokenResponse
	_ = json.Unmarshal(trec.Body.Bytes(), &tok)
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("missing tokens: %+v", tok)
	}

	// 3. /token refresh_token → new access.
	rrec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"claude-desktop"},
		"refresh_token": {tok.RefreshToken},
	})
	if rrec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", rrec.Code, rrec.Body.String())
	}
}

// fakeAuthorizer gates the consent step for the Cedar wiring test.
type fakeAuthorizer struct{ allow bool }

func (f fakeAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	if f.allow {
		return cedar.DecisionAllow, nil
	}
	return cedar.DecisionDeny, nil
}

func TestAuthorize_CedarGate(t *testing.T) {
	verifier := strings.Repeat("a", 50)
	allowForm := func(u authstore.User) url.Values {
		return url.Values{
			"client_id": {"claude-desktop"}, "redirect_uri": {"claude-desktop://cb"}, "scope": {"paladin.read"},
			"code_challenge": {ComputeS256Challenge(verifier)}, "code_challenge_method": {"S256"},
			"state": {"st"}, "action": {"allow"},
			"username": {"svc@acme"}, "password": {"hunter2hunter2"}, "tenant": {u.TenantID.String()},
		}
	}

	t.Run("policy deny → access_denied", func(t *testing.T) {
		store := newMemStore()
		store.clients[publicClient().ClientID] = publicClient()
		u := sampleUser()
		h := testHandler(t, store, &memRefresh{}, u).WithAuthorizer(fakeAuthorizer{allow: false})
		rec := postForm(h, "/oauth/authorize", allowForm(u))
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusFound || loc.Query().Get("error") != "access_denied" {
			t.Fatalf("status=%d loc=%s, want 302 access_denied", rec.Code, rec.Header().Get("Location"))
		}
		if loc.Query().Get("code") != "" {
			t.Error("no code must be issued when policy denies")
		}
	})

	t.Run("policy allow → code issued", func(t *testing.T) {
		store := newMemStore()
		store.clients[publicClient().ClientID] = publicClient()
		u := sampleUser()
		h := testHandler(t, store, &memRefresh{}, u).WithAuthorizer(fakeAuthorizer{allow: true})
		rec := postForm(h, "/oauth/authorize", allowForm(u))
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusFound || loc.Query().Get("code") == "" {
			t.Fatalf("status=%d loc=%s, want 302 with code", rec.Code, rec.Header().Get("Location"))
		}
	})
}

func TestToken_RefreshReuseDetected(t *testing.T) {
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	u := sampleUser()
	// Get returns ErrTokenRevoked → the presented refresh was already rotated.
	refresh := &memRefresh{getErr: authstore.ErrTokenRevoked}
	audit := &memAudit{}
	h := testHandler(t, store, refresh, u).WithAudit(audit)

	// Mint a validly-signed IAM refresh token the decoder will accept.
	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(testSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	rt, _, err := iss.MintRefresh(issuer.RefreshClaims{Subject: u.UserID.String(), TenantID: u.TenantID, UserID: u.UserID, TokenID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatalf("mint refresh: %v", err)
	}

	rec := postForm(h, "/oauth/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"claude-desktop"}, "refresh_token": {rt},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 invalid_grant", rec.Code)
	}
	if refresh.familyRevoked != 1 {
		t.Errorf("reuse must revoke the token family; RevokeFamilyOf calls = %d", refresh.familyRevoked)
	}
	if len(audit.entries) != 1 {
		t.Fatalf("reuse must write exactly one audit entry, got %d", len(audit.entries))
	}
	e := audit.entries[0]
	if !strings.Contains(e.Action, "Reuse") || e.ErrorMessage == "" {
		t.Errorf("audit entry should name the reuse + carry an error message (is_error): %+v", e)
	}
}
