//go:build integration

package components

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

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"go.uber.org/zap"
)

const e2eSigningKey = "e2e-oauth-signing-key-at-least-32bytes!!"

// TestOAuthFlowE2E drives the full ADR-0009 auth-code + PKCE flow over real
// HTTP against real Postgres: /authorize (login+consent) → code → /token →
// access+refresh → refresh rotation. This is the runnable end-to-end coverage
// (the live-stack hurl variant needs a full compose stack + a password user
// the e2e harness doesn't provision).
func TestOAuthFlowE2E(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)

	// Seed tenant + a user with a known password.
	tenantID, userID := uuid.New(), uuid.New()
	const password = "correct horse battery staple"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1,'acme','acme')`, tenantID)
	mustExec(t, ctx, pool,
		`INSERT INTO users (id, tenant_id, subject, password_hash) VALUES ($1,$2,'svc@acme',$3)`,
		userID, tenantID, hash)

	iss, err := issuer.New(issuer.Config{Issuer: "paladin", SigningKey: []byte(e2eSigningKey), AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: time.Hour, ScopedTokenMaxTTL: time.Hour})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	dec := &auth.RefreshDecoder{Verifier: &auth.JWTVerifier{Key: []byte(e2eSigningKey), ExpectedIssuer: "paladin", ExpectedAudience: auth.AudienceIAM}}

	store := oauth.NewPgxStore(pool)
	if err := store.UpsertClient(ctx, oauth.Client{
		ClientID: "claude-desktop", RedirectURIs: []string{"https://app.example.com/cb"},
		AllowedScopes: []string{"paladin.read"}, AllowedAudiences: []string{"paladin-data"}, Public: true,
	}); err != nil {
		t.Fatalf("seed client: %v", err)
	}

	h := oauth.NewHandler(
		config.OAuthAS{Enabled: true, AuthorizationCodeTTL: time.Minute, AllowedRedirectSchemes: []string{"https"}},
		store, adapters.NewUserRepo(q), adapters.NewRefreshTokenRepo(q), iss, dec, zap.NewNop())
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Don't follow the final redirect (custom/app scheme) — capture the 302.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	verifier := strings.Repeat("e2e-verifier-padding", 3) // 57 chars, unreserved
	challenge := oauth.ComputeS256Challenge(verifier)

	// 1. /authorize — login + consent → 302 with code.
	authResp := postFormRaw(t, client, srv.URL+"/oauth/authorize", url.Values{
		"client_id": {"claude-desktop"}, "redirect_uri": {"https://app.example.com/cb"},
		"scope": {"paladin.read"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"state": {"xyz"}, "action": {"allow"},
		"username": {"svc@acme"}, "password": {password}, "tenant": {tenantID.String()},
	})
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302", authResp.StatusCode)
	}
	loc, _ := url.Parse(authResp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("authorize redirect missing code/state: %s", authResp.Header.Get("Location"))
	}

	// 2. /token — exchange code for tokens.
	tok := postToken(t, client, srv.URL, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"claude-desktop"},
		"redirect_uri": {"https://app.example.com/cb"}, "code_verifier": {verifier},
	})
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		t.Fatalf("token response missing tokens: %+v", tok)
	}

	// 3. /token — refresh rotation.
	tok2 := postToken(t, client, srv.URL, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {"claude-desktop"}, "refresh_token": {tok.RefreshToken},
	})
	if tok2.AccessToken == "" {
		t.Fatal("refresh did not return a new access token")
	}

	// 4. Reusing the original (now-rotated) code must fail — single-use.
	bad := postFormRaw(t, client, srv.URL+"/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"claude-desktop"},
		"redirect_uri": {"https://app.example.com/cb"}, "code_verifier": {verifier},
	})
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("replayed code status = %d, want 400", bad.StatusCode)
	}
	bad.Body.Close()
}

type e2eTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

func postToken(t *testing.T, c *http.Client, base string, form url.Values) e2eTokenResp {
	t.Helper()
	resp := postFormRaw(t, c, base+"/oauth/token", form)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/token status = %d", resp.StatusCode)
	}
	var out e2eTokenResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	return out
}

// formContentType is what http.Client.PostForm sends.
const formContentType = "application/x-www-form-urlencoded"

func postFormRaw(t *testing.T, c *http.Client, url string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	req.Header.Set("Content-Type", formContentType)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}
