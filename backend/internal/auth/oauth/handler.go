package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/config"
)

// UserResolver is the slice of the IAM user store the AS needs: look a user up
// for the login leg (by subject, tenant-disambiguated) and by id (refresh).
type UserResolver interface {
	GetByID(ctx context.Context, id uuid.UUID) (authstore.User, error)
	GetBySubject(ctx context.Context, tenantID uuid.UUID, subject string) (authstore.User, error)
	FindBySubjectGlobal(ctx context.Context, subject string) ([]authstore.User, error)
}

// Handler implements the PALADIN IAM OAuth 2.1 Authorization Server endpoints
// (ADR-0009): /oauth/authorize, /oauth/token, /oauth/register. Tokens are
// minted by the same issuer.Issuer the Connect login path uses, so the bearer
// validates on the target plane (and at the MCP Resource Server, ADR-0008).
type Handler struct {
	cfg     config.OAuthAS
	store   Store
	users   UserResolver
	refresh authstore.RefreshTokenRepository
	iss     *issuer.Issuer
	dec     *auth.RefreshDecoder
	log     *zap.Logger
	now     func() time.Time
}

// NewHandler builds the AS. now may be nil (defaults to time.Now).
func NewHandler(cfg config.OAuthAS, store Store, users UserResolver, refresh authstore.RefreshTokenRepository, iss *issuer.Issuer, dec *auth.RefreshDecoder, l *zap.Logger) *Handler {
	return &Handler{cfg: cfg, store: store, users: users, refresh: refresh, iss: iss, dec: dec, log: l, now: time.Now}
}

// Mount registers the OAuth endpoints on the given mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/oauth/authorize", h.handleAuthorize)
	mux.HandleFunc("/oauth/token", h.handleToken)
	if h.cfg.DynamicRegistration {
		mux.HandleFunc("/oauth/register", h.handleRegister)
	}
}

// ─── /authorize ──────────────────────────────────────────────────────────────

// authorizeParams is the validated subset of the RFC 6749 §4.1.1 request we
// carry from the GET form render through the POST decision.
type authorizeParams struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

func parseAuthorizeParams(q url.Values) authorizeParams {
	return authorizeParams{
		ClientID:            q.Get("client_id"),
		RedirectURI:         q.Get("redirect_uri"),
		Scope:               q.Get("scope"),
		State:               q.Get("state"),
		CodeChallenge:       q.Get("code_challenge"),
		CodeChallengeMethod: NormalizeMethod(q.Get("code_challenge_method")),
		Resource:            q.Get("resource"),
	}
}

// handleAuthorize renders a combined login+consent screen (GET) and processes
// the decision (POST). PALADIN IAM has no browser session, so the AS authenticates
// the resource owner here with username+password and, on approval, issues a
// PKCE-bound single-use code redirected to the registered redirect_uri.
func (h *Handler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "malformed form")
		return
	}
	var p authorizeParams
	if r.Method == http.MethodGet {
		p = parseAuthorizeParams(r.URL.Query())
	} else {
		p = parseAuthorizeParams(r.PostForm)
	}

	// Validate the client + redirect_uri BEFORE we ever redirect, so a bad
	// redirect_uri is shown to the user, never followed (RFC 6749 §4.1.2.1).
	client, err := h.store.GetClient(r.Context(), p.ClientID)
	if err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
		return
	}
	if !redirectAllowed(client, p.RedirectURI) {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "redirect_uri not registered for this client")
		return
	}
	// PKCE is mandatory (S256). Errors past this point redirect to the client.
	if p.CodeChallenge == "" || p.CodeChallengeMethod != PKCEMethodS256 {
		h.redirectError(w, r, p, "invalid_request", "code_challenge with method=S256 is required")
		return
	}

	if r.Method == http.MethodGet {
		// When a front-end consent page is configured, hand off to it (the
		// request is already validated); otherwise server-render the built-in
		// form so the AS works standalone.
		if h.cfg.ConsentURL != "" {
			h.redirectToConsentUI(w, r, p, "")
			return
		}
		h.renderConsent(w, client, p, "")
		return
	}

	// POST: the decision.
	if r.PostForm.Get("action") != "allow" {
		h.redirectError(w, r, p, "access_denied", "the user denied the request")
		return
	}
	subject := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	tenantHint := r.PostForm.Get("tenant")
	u, err := h.authenticate(r.Context(), subject, password, tenantHint)
	if err != nil {
		// Bounce back to the consent UI with an error rather than leaking
		// which factor failed.
		if h.cfg.ConsentURL != "" {
			h.redirectToConsentUI(w, r, p, "invalid_credentials")
			return
		}
		h.renderConsent(w, client, p, "Invalid credentials or tenant.")
		return
	}

	scopes := splitScopes(p.Scope)
	if !scopesSubset(scopes, client.AllowedScopes) {
		h.redirectError(w, r, p, "invalid_scope", "requested scope exceeds client grant")
		return
	}
	audience := audienceFor(client, p.Resource)

	code, err := GenerateCode()
	if err != nil {
		h.redirectError(w, r, p, "server_error", "could not issue code")
		return
	}
	ttl := h.cfg.AuthorizationCodeTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if err := h.store.CreateCode(r.Context(), AuthCode{
		Code:            code,
		ClientID:        client.ClientID,
		UserID:          u.UserID,
		TenantID:        u.TenantID,
		RedirectURI:     p.RedirectURI,
		CodeChallenge:   p.CodeChallenge,
		ChallengeMethod: p.CodeChallengeMethod,
		Scopes:          scopes,
		Audience:        audience,
		ExpiresAt:       h.now().Add(ttl),
	}); err != nil {
		h.redirectError(w, r, p, "server_error", "could not persist code")
		return
	}

	redirectWithCode(w, r, p.RedirectURI, code, p.State)
}

// authenticate resolves the resource owner by subject (tenant-disambiguated)
// and verifies the password — the same posture as AuthService.Login.
func (h *Handler) authenticate(ctx context.Context, subject, password, tenantHint string) (authstore.User, error) {
	if subject == "" || password == "" {
		return authstore.User{}, errors.New("missing credentials")
	}
	var (
		u   authstore.User
		err error
	)
	if tenantHint != "" {
		if tid, perr := uuid.Parse(tenantHint); perr == nil {
			u, err = h.users.GetBySubject(ctx, tid, subject)
		} else {
			err = errors.New("invalid tenant hint")
		}
	} else {
		var matches []authstore.User
		matches, err = h.users.FindBySubjectGlobal(ctx, subject)
		switch {
		case err == nil && len(matches) == 1:
			u = matches[0]
		case err == nil && len(matches) > 1:
			return authstore.User{}, errors.New("ambiguous subject; tenant required")
		case err == nil:
			return authstore.User{}, errors.New("no such user")
		}
	}
	if err != nil {
		return authstore.User{}, err
	}
	if u.Disabled || len(u.PasswordHash) == 0 {
		return authstore.User{}, errors.New("user not eligible")
	}
	if err := auth.CheckPassword(u.PasswordHash, password); err != nil {
		return authstore.User{}, errors.New("invalid credentials")
	}
	return u, nil
}

// ─── /token ──────────────────────────────────────────────────────────────────

func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.jsonError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "malformed form")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		h.tokenAuthCode(w, r)
	case "refresh_token":
		h.tokenRefresh(w, r)
	default:
		h.jsonError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (h *Handler) tokenAuthCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")

	client, err := h.store.GetClient(r.Context(), clientID)
	if err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}

	ac, err := h.store.ConsumeCode(r.Context(), code)
	if err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or already used")
		return
	}
	// Bind the code to the presenting client + the original redirect_uri.
	if ac.ClientID != clientID || ac.RedirectURI != redirectURI {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "code does not match client/redirect_uri")
		return
	}
	if err := VerifyChallenge(ac.ChallengeMethod, ac.CodeChallenge, verifier, false); err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}

	u, err := h.users.GetByID(r.Context(), ac.UserID)
	if err != nil || u.Disabled {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "user no longer eligible")
		return
	}
	h.issueTokens(r.Context(), w, u, ac.Audience, scopesJoin(ac.Scopes))
}

func (h *Handler) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	clientID := r.PostForm.Get("client_id")
	refreshTok := r.PostForm.Get("refresh_token")

	client, err := h.store.GetClient(r.Context(), clientID)
	if err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}

	jti, userID, tenantID, err := h.dec.DecodeRefresh(refreshTok)
	if err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token invalid")
		return
	}
	stored, err := h.refresh.Get(r.Context(), jti)
	if err != nil || h.now().After(stored.ExpiresAt) {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token rejected")
		return
	}
	u, err := h.users.GetByID(r.Context(), userID)
	if err != nil || u.Disabled || u.TenantID != tenantID {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "user no longer eligible")
		return
	}
	// Rotate: revoke the presented refresh before minting a fresh pair (RFC 6749 §6).
	if err := h.refresh.Revoke(r.Context(), jti); err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "rotation failed")
		return
	}
	h.issueTokens(r.Context(), w, u, auth.AudienceData, "")
}

// tokenResponse is the RFC 6749 §5.1 success body.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// issueTokens mints an access token (aud-bound) + a rotating refresh token,
// stores the refresh row, and writes the §5.1 JSON.
func (h *Handler) issueTokens(ctx context.Context, w http.ResponseWriter, u authstore.User, audience, scope string) {
	access, accessExp, err := h.iss.MintAccess(issuer.AccessClaims{
		Subject:  u.UserID.String(),
		TenantID: u.TenantID,
		Audience: audience,
		Roles:    u.Roles,
		Scopes:   u.Scopes,
		Kind:     auth.PrincipalKindUser,
	})
	if err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "mint access failed")
		return
	}
	tokenID := uuid.Must(uuid.NewV7())
	refresh, refreshExp, err := h.iss.MintRefresh(issuer.RefreshClaims{
		Subject: u.UserID.String(), TenantID: u.TenantID, UserID: u.UserID, TokenID: tokenID,
	})
	if err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "mint refresh failed")
		return
	}
	if err := h.refresh.Insert(ctx, r2RefreshToken(tokenID, u, h.now(), refreshExp)); err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "persist refresh failed")
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(time.Until(accessExp).Seconds()),
		RefreshToken: refresh,
		Scope:        scope,
	})
}

func r2RefreshToken(jti uuid.UUID, u authstore.User, iat, exp time.Time) authstore.RefreshToken {
	return authstore.RefreshToken{JTI: jti, UserID: u.UserID, TenantID: u.TenantID, IssuedAt: iat, ExpiresAt: exp}
}

// authenticateClient enforces client authentication: public clients pass
// (PKCE is their proof), confidential clients must present a matching secret.
func (h *Handler) authenticateClient(r *http.Request, client Client) error {
	if client.Public || len(client.SecretHash) == 0 {
		return nil
	}
	secret := r.PostForm.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok && u == client.ClientID {
		secret = p
	}
	if secret == "" {
		return errors.New("client_secret required")
	}
	if err := auth.CheckApiKeySecret(client.SecretHash, secret); err != nil {
		return errors.New("invalid client_secret")
	}
	return nil
}

// ─── /register (RFC 7591 DCR) ────────────────────────────────────────────────

type registerRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	Scopes                  []string `json:"scope_list"`
	Audiences               []string `json:"audiences"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

type registerResponse struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.jsonError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "malformed JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		h.jsonError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect_uri is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !h.schemeAllowed(u) {
			h.jsonError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri scheme not allowed")
			return
		}
	}
	public := req.TokenEndpointAuthMethod == "" || req.TokenEndpointAuthMethod == "none"

	client := Client{
		ClientID:         "paladin-" + uuid.Must(uuid.NewV7()).String(),
		ClientName:       req.ClientName,
		RedirectURIs:     req.RedirectURIs,
		AllowedScopes:    req.Scopes,
		AllowedAudiences: req.Audiences,
		Public:           public,
	}
	resp := registerResponse{
		ClientID:     client.ClientID,
		ClientName:   client.ClientName,
		RedirectURIs: client.RedirectURIs,
	}
	if !public {
		secret, _, err := auth.GenerateApiKeySecret()
		if err != nil {
			h.jsonError(w, http.StatusInternalServerError, "server_error", "secret generation failed")
			return
		}
		hash, err := auth.HashApiKeySecret(secret)
		if err != nil {
			h.jsonError(w, http.StatusInternalServerError, "server_error", "secret hash failed")
			return
		}
		client.SecretHash = hash
		resp.ClientSecret = secret
	}
	if err := h.store.UpsertClient(r.Context(), client); err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (h *Handler) schemeAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return false
	}
	allowed := h.cfg.AllowedRedirectSchemes
	if len(allowed) == 0 {
		allowed = []string{"https"}
	}
	for _, s := range allowed {
		if strings.EqualFold(u.Scheme, s) {
			return true
		}
	}
	return false
}

func redirectAllowed(client Client, redirectURI string) bool {
	for _, u := range client.RedirectURIs {
		if u == redirectURI {
			return true
		}
	}
	return false
}

func audienceFor(client Client, resource string) string {
	// A resource indicator (RFC 8707) that matches an allowed audience wins;
	// otherwise the client's first allowed audience; otherwise paladin-data.
	for _, a := range client.AllowedAudiences {
		if a == resource && resource != "" {
			return a
		}
	}
	if len(client.AllowedAudiences) > 0 {
		return client.AllowedAudiences[0]
	}
	return auth.AudienceData
}

func splitScopes(s string) []string {
	f := strings.Fields(s)
	if f == nil {
		return []string{}
	}
	return f
}

func scopesJoin(s []string) string { return strings.Join(s, " ") }

func scopesSubset(want, allowed []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func redirectWithCode(w http.ResponseWriter, r *http.Request, redirectURI, code, state string) {
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set("code", code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *Handler) redirectError(w http.ResponseWriter, r *http.Request, p authorizeParams, errCode, desc string) {
	u, err := url.Parse(p.RedirectURI)
	if err != nil || p.RedirectURI == "" {
		h.jsonError(w, http.StatusBadRequest, errCode, desc)
		return
	}
	q := u.Query()
	q.Set("error", errCode)
	q.Set("error_description", desc)
	if p.State != "" {
		q.Set("state", p.State)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *Handler) jsonError(w http.ResponseWriter, status int, errCode, desc string) {
	writeJSON(w, status, map[string]string{"error": errCode, "error_description": desc})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Authorize {{.ClientName}}</title></head>
<body style="font-family:system-ui;max-width:28rem;margin:3rem auto">
<h1>Authorize access</h1>
<p><strong>{{.ClientName}}</strong> ({{.ClientID}}) is requesting access{{if .Scopes}} to:</p>
<ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>{{else}}.</p>{{end}}
{{if .Error}}<p style="color:#b00">{{.Error}}</p>{{end}}
<form method="post" action="/oauth/authorize">
  <input type="hidden" name="client_id" value="{{.ClientID}}">
  <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
  <input type="hidden" name="scope" value="{{.Scope}}">
  <input type="hidden" name="state" value="{{.State}}">
  <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
  <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
  <input type="hidden" name="resource" value="{{.Resource}}">
  <p><label>Username<br><input name="username" autocomplete="username"></label></p>
  <p><label>Password<br><input name="password" type="password" autocomplete="current-password"></label></p>
  <p><label>Tenant (optional)<br><input name="tenant" placeholder="tenant id"></label></p>
  <button name="action" value="allow" type="submit">Allow</button>
  <button name="action" value="deny" type="submit">Deny</button>
</form>
</body></html>`))

type consentView struct {
	authorizeParams
	ClientName string
	Scopes     []string
	Error      string
}

// redirectToConsentUI hands the (already-validated) request off to the
// configured front-end consent page, forwarding the OAuth params as query
// string. The page collects credentials + the decision and POSTs them back to
// /oauth/authorize. errCode (optional) lets the page show "invalid credentials"
// on a retry.
func (h *Handler) redirectToConsentUI(w http.ResponseWriter, r *http.Request, p authorizeParams, errCode string) {
	u, err := url.Parse(h.cfg.ConsentURL)
	if err != nil {
		h.jsonError(w, http.StatusInternalServerError, "server_error", "consent_url misconfigured")
		return
	}
	q := u.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", p.CodeChallengeMethod)
	if p.Resource != "" {
		q.Set("resource", p.Resource)
	}
	if errCode != "" {
		q.Set("error", errCode)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *Handler) renderConsent(w http.ResponseWriter, client Client, p authorizeParams, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = consentTmpl.Execute(w, consentView{
		authorizeParams: p,
		ClientName:      orDefault(client.ClientName, client.ClientID),
		Scopes:          splitScopes(p.Scope),
		Error:           errMsg,
	})
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
