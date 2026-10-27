package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	cedar "github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// AuditWriter records security-relevant OAuth events (currently refresh-token
// reuse) so they surface in the admin audit console. Optional — a nil writer
// just skips the audit row; the log + token revocation still happen.
type AuditWriter interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
}

// Authorizer is the slice of the Cedar engine the AS uses to gate consent
// (ActionAuthorizeOAuth). Optional — a nil Authorizer skips the policy check
// (scope-subset + authentication still apply), so the AS runs standalone.
type Authorizer interface {
	IsAuthorized(ctx context.Context, p *cedar.Principal, action cedar.Action, r *cedar.Resource, rc cedar.RequestContext) (cedar.Decision, error)
}

// UserResolver is the slice of the IAM user store the AS needs: look a user up
// for the login leg (by subject, tenant-disambiguated) and by id (refresh).
type UserResolver interface {
	GetByID(ctx context.Context, id uuid.UUID) (authstore.User, error)
	GetBySubject(ctx context.Context, tenantID uuid.UUID, subject string) (authstore.User, error)
	FindBySubjectGlobal(ctx context.Context, subject string) ([]authstore.User, error)
}

// Handler implements the Paladin IAM OAuth 2.1 Authorization Server endpoints
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
	limiter *tokenBucket
	// exchangeLimiter caps the token-exchange grant on its own.
	exchangeLimiter *tokenBucket
	corsOrigins     []string
	authz           Authorizer
	audit           AuditWriter
	skipConsent     map[string]bool
	exchange        *tokenExchange
	mcpResource     string
	log             *zap.Logger
	now             func() time.Time
}

// WithSkipConsent marks client_ids the operator pre-authorized (trusted
// first-party clients): /authorize goes straight to login, no consent screen.
// Never applies to dynamically-registered clients.
func (h *Handler) WithSkipConsent(clientIDs map[string]bool) *Handler {
	h.skipConsent = clientIDs
	return h
}

// WithMCPResource names the MCP server's resource URL (mcp.oauth.resource_url):
// a client that asks for it as its RFC 8707 resource is issued paladin-mcp
// (ADR-0036). Empty leaves only the audience names themselves.
func (h *Handler) WithMCPResource(resourceURL string) *Handler {
	h.mcpResource = resourceURL
	return h
}

// WithTokenExchange serves the RFC 8693 token-exchange grant to clientIDs,
// trading a paladin-mcp token that subjects verifies for one per plane
// (ADR-0036). subjects must accept only paladin-mcp. Without it the grant
// type is unsupported.
func (h *Handler) WithTokenExchange(subjects auth.TokenVerifier, clientIDs map[string]bool) *Handler {
	h.exchange = &tokenExchange{subjects: subjects, clients: clientIDs}
	return h
}

// WithAuthorizer enables Cedar gating of the consent step (ActionAuthorizeOAuth).
// Optional; nil leaves the AS on scope-subset + authentication only.
func (h *Handler) WithAuthorizer(a Authorizer) *Handler {
	h.authz = a
	return h
}

// WithAudit records refresh-token reuse events to the audit log (visible in
// the admin audit console). Optional.
func (h *Handler) WithAudit(a AuditWriter) *Handler {
	h.audit = a
	return h
}

// NewHandler builds the AS. now may be nil (defaults to time.Now).
func NewHandler(cfg config.OAuthAS, store Store, users UserResolver, refresh authstore.RefreshTokenRepository, iss *issuer.Issuer, dec *auth.RefreshDecoder, l *zap.Logger) *Handler {
	return &Handler{
		cfg:             cfg,
		store:           store,
		users:           users,
		refresh:         refresh,
		iss:             iss,
		dec:             dec,
		limiter:         newTokenBucket(cfg.TokenRateLimitPerMinute),
		exchangeLimiter: newTokenBucket(cfg.TokenExchangeRateLimitPerMinute),
		corsOrigins:     cfg.TokenEndpointAllowedOrigins,
		log:             l,
		now:             time.Now,
	}
}

// Mount registers the OAuth endpoints on the given mux.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc(AuthorizePath, h.handleAuthorize)
	mux.HandleFunc(TokenPath, h.handleToken)
	mux.HandleFunc(RevokePath, h.handleRevoke)
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
// the decision (POST). Paladin IAM has no browser session, so the AS authenticates
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
		if errors.Is(err, ErrClientNotFound) {
			h.jsonError(w, http.StatusBadRequest, "invalid_client", "unknown client_id")
			return
		}
		h.serverError(r.Context(), w, "client lookup failed", err, zap.String("client_id", p.ClientID))
		return
	}
	registered, ok := registeredRedirect(client, p.RedirectURI)
	if !ok {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "redirect_uri not registered for this client")
		return
	}
	// Every redirect below goes to the client's stored URI, never to the
	// request's copy of it.
	p.RedirectURI = registered
	// PKCE is mandatory (S256). Errors past this point redirect to the client.
	if p.CodeChallenge == "" || p.CodeChallengeMethod != PKCEMethodS256 {
		h.redirectError(w, r, registered, p.State, "invalid_request", "code_challenge with method=S256 is required")
		return
	}

	skip := h.skipConsent[client.ClientID]

	if r.Method == http.MethodGet {
		// When a front-end consent page is configured, hand off to it (the
		// request is already validated); otherwise server-render the built-in
		// form so the AS works standalone. Pre-authorized (skip_consent)
		// clients render a plain login — no per-user consent prompt.
		if h.cfg.ConsentURL != "" {
			h.redirectToConsentUI(w, r, p, "", skip)
			return
		}
		h.renderConsent(w, client, p, "", skip)
		return
	}

	// POST: the decision.
	if r.PostForm.Get("action") != "allow" {
		// Who refused is not known: the form is refused before it signs in.
		h.record(r.Context(), admindomain.AuditEntry{
			Action:       actionConsentDenied,
			ResourceName: clientResource + client.ClientID,
			ErrorMessage: "the user denied the request",
		}, nil)
		h.redirectError(w, r, registered, p.State, "access_denied", "the user denied the request")
		return
	}
	subject := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	tenantHint := r.PostForm.Get("tenant")
	u, err := h.authenticate(r.Context(), subject, password, tenantHint)
	if err != nil && !errors.Is(err, errBadCredentials) {
		logServerError(r.Context(), "user lookup failed", err, zap.String("client_id", client.ClientID))
		h.redirectError(w, r, registered, p.State, errServerError, "user lookup failed")
		return
	}
	if err != nil {
		// Bounce back to the consent UI with an error rather than leaking
		// which factor failed.
		if h.cfg.ConsentURL != "" {
			h.redirectToConsentUI(w, r, p, "invalid_credentials", skip)
			return
		}
		h.renderConsent(w, client, p, "Invalid credentials or tenant.", skip)
		return
	}

	scopes := splitScopes(p.Scope)
	if !scopesSubset(scopes, client.AllowedScopes) {
		h.redirectError(w, r, registered, p.State, "invalid_scope", "requested scope exceeds client grant")
		return
	}

	// Cedar gate (ADR-0009): the built-in policy permits AuthorizeOAuth for
	// any authenticated principal; a tenant policy may forbid specific
	// principals/clients/scopes via context.oauth_client_id / oauth_scopes.
	if h.authz != nil {
		dec, aerr := h.authz.IsAuthorized(r.Context(),
			&cedar.Principal{Subject: u.Subject, TenantID: u.TenantID, Roles: u.Roles},
			cedar.ActionAuthorizeOAuth,
			&cedar.Resource{TenantID: u.TenantID},
			cedar.RequestContext{Now: h.now(), OAuthClientID: client.ClientID, OAuthScopes: scopes},
		)
		if aerr != nil {
			// A policy engine that could not decide has not denied.
			logServerError(r.Context(), "policy evaluation failed", aerr, zap.String("client_id", client.ClientID))
			h.redirectError(w, r, registered, p.State, errServerError, "policy evaluation failed")
			return
		}
		if dec != cedar.DecisionAllow {
			h.record(r.Context(), admindomain.AuditEntry{
				ActorSubject:  u.UserID.String(),
				ActorTenantID: u.TenantID,
				Action:        actionConsentDenied,
				ResourceName:  clientResource + client.ClientID,
				ErrorMessage:  "authorization denied by policy",
			}, grantDetail{Scopes: scopes})
			h.redirectError(w, r, registered, p.State, "access_denied", "authorization denied by policy")
			return
		}
	}

	audience := audienceFor(client, h.resolveResource(p.Resource))
	// The same eligibility as a console login: paladin-admin only for a role
	// with work on the admin plane. paladin-mcp is any user's, since every
	// call through it is exchanged for a plane under this same rule.
	if audience != auth.AudienceMCP {
		if err := apiutil.AudienceEligible(u.Roles, audience); err != nil {
			h.redirectError(w, r, registered, p.State, "invalid_target", "the user may not be issued "+audience)
			return
		}
	}

	code, err := GenerateCode()
	if err != nil {
		logServerError(r.Context(), "could not issue code", err, zap.String("client_id", p.ClientID))
		h.redirectError(w, r, registered, p.State, "server_error", "could not issue code")
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
		logServerError(r.Context(), "could not persist code", err, zap.String("client_id", p.ClientID))
		h.redirectError(w, r, registered, p.State, "server_error", "could not persist code")
		return
	}

	h.record(r.Context(), admindomain.AuditEntry{
		ActorSubject:  u.UserID.String(),
		ActorTenantID: u.TenantID,
		Action:        actionConsentGranted,
		ResourceName:  clientResource + client.ClientID,
	}, grantDetail{Audience: audience, Scopes: scopes})
	redirectWithCode(w, r, registered, code, p.State)
}

// errBadCredentials is what authenticate returns for credentials that do not
// name an eligible user. Any other error is the store failing, which the
// caller must not answer as wrong credentials.
var errBadCredentials = errors.New("oauth: bad credentials")

// authenticate resolves the resource owner by subject (tenant-disambiguated)
// and verifies the password — the same posture as AuthService.Login.
func (h *Handler) authenticate(ctx context.Context, subject, password, tenantHint string) (authstore.User, error) {
	if subject == "" || password == "" {
		return authstore.User{}, fmt.Errorf("%w: missing credentials", errBadCredentials)
	}
	var (
		u   authstore.User
		err error
	)
	if tenantHint != "" {
		if tid, perr := uuid.Parse(tenantHint); perr == nil {
			u, err = h.users.GetBySubject(ctx, tid, subject)
		} else {
			err = fmt.Errorf("%w: invalid tenant hint", errBadCredentials)
		}
	} else {
		var matches []authstore.User
		matches, err = h.users.FindBySubjectGlobal(ctx, subject)
		switch {
		case err == nil && len(matches) == 1:
			u = matches[0]
		case err == nil && len(matches) > 1:
			return authstore.User{}, fmt.Errorf("%w: ambiguous subject; tenant required", errBadCredentials)
		case err == nil:
			return authstore.User{}, fmt.Errorf("%w: no such user", errBadCredentials)
		}
	}
	if errors.Is(err, authstore.ErrNotFound) {
		return authstore.User{}, fmt.Errorf("%w: no such user", errBadCredentials)
	}
	if err != nil {
		return authstore.User{}, err
	}
	if u.Disabled || len(u.PasswordHash) == 0 {
		return authstore.User{}, fmt.Errorf("%w: user not eligible", errBadCredentials)
	}
	if err := auth.CheckPassword(u.PasswordHash, password); err != nil {
		return authstore.User{}, fmt.Errorf("%w: invalid credentials", errBadCredentials)
	}
	return u, nil
}

// ─── /token ──────────────────────────────────────────────────────────────────

func (h *Handler) handleToken(w http.ResponseWriter, r *http.Request) {
	// CORS for browser-based public clients (no-op unless origins configured).
	h.applyCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		h.jsonError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "malformed form")
		return
	}
	// Rate-limit per client_id before any DB work, to blunt code/secret
	// brute-forcing (ADR-0009 hardening).
	limiter := h.limiter
	if r.PostForm.Get("grant_type") == GrantTypeTokenExchange {
		limiter = h.exchangeLimiter
	}
	if ok, retry := limiter.allow(r.PostForm.Get("client_id")); !ok {
		secs := int(retry.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		h.jsonError(w, http.StatusTooManyRequests, "slow_down", "too many token requests; retry later")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case GrantTypeAuthorizationCode:
		h.tokenAuthCode(w, r)
	case GrantTypeRefreshToken:
		h.tokenRefresh(w, r)
	case GrantTypeTokenExchange:
		if h.exchange == nil {
			h.unsupportedGrant(w)
			return
		}
		h.tokenExchange(w, r)
	default:
		h.unsupportedGrant(w)
	}
}

func (h *Handler) tokenAuthCode(w http.ResponseWriter, r *http.Request) {
	code := r.PostForm.Get("code")
	clientID := r.PostForm.Get("client_id")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")

	client, ok := h.tokenClient(w, r, clientID)
	if !ok {
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}

	ac, err := h.store.ConsumeCode(r.Context(), code)
	if err != nil {
		if errors.Is(err, ErrCodeInvalid) {
			h.jsonError(w, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or already used")
			return
		}
		h.serverError(r.Context(), w, "code redemption failed", err, zap.String("client_id", clientID))
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

	u, ok := h.eligibleUser(w, r, ac.UserID, ac.TenantID, clientID)
	if !ok {
		return
	}
	if h.issueTokens(r.Context(), w, u, authstore.OAuthGrant{
		ClientID: ac.ClientID, Audience: ac.Audience, Scopes: ac.Scopes,
	}, uuid.Nil()) {
		h.record(r.Context(), admindomain.AuditEntry{
			ActorSubject:  u.UserID.String(),
			ActorTenantID: u.TenantID,
			Action:        actionGrantRedeemed,
			ResourceName:  clientResource + ac.ClientID,
		}, grantDetail{Audience: ac.Audience, Scopes: ac.Scopes})
	}
}

func (h *Handler) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	clientID := r.PostForm.Get("client_id")
	refreshTok := r.PostForm.Get("refresh_token")

	client, ok := h.tokenClient(w, r, clientID)
	if !ok {
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
	if err != nil && !errors.Is(err, authstore.ErrTokenRevoked) && !errors.Is(err, authstore.ErrNotFound) {
		h.serverError(r.Context(), w, "refresh token lookup failed", err, zap.String("client_id", clientID))
		return
	}
	if err != nil {
		// A revoked-but-present token means a rotated refresh was replayed —
		// the RFC 6819 theft signal. Revoke the whole family (all the user's
		// refresh tokens), log, and record it for the audit console.
		if errors.Is(err, authstore.ErrTokenRevoked) {
			h.onRefreshReuse(r.Context(), jti, userID, tenantID)
		}
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token rejected")
		return
	}
	if h.now().After(stored.ExpiresAt) {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token rejected")
		return
	}
	// Only a token this endpoint issued, and only to the client presenting it
	// (RFC 6749 §6). A console login's token carries no grant to re-issue.
	grant := stored.OAuth
	if grant == nil || grant.ClientID != client.ClientID {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token was not issued to this client")
		return
	}
	u, ok := h.eligibleUser(w, r, userID, tenantID, clientID)
	if !ok {
		return
	}
	// Rotate: supersede the presented refresh before minting a fresh pair (RFC
	// 6749 §6), keeping the same family so the chain stays linked for
	// reuse-detection. Supersede writes only a token still live, so a revoke of
	// the grant or a concurrent refresh landing after the read above wins: this
	// rotation then mints nothing, rather than a live successor in a family
	// that was just ended. Losing that race is no replay; the family is left
	// as the winner put it.
	if err := h.refresh.Supersede(auth.WithActingTenant(r.Context(), stored.TenantID), jti); err != nil {
		if errors.Is(err, authstore.ErrAlreadyRotated) {
			h.jsonError(w, http.StatusBadRequest, "invalid_grant", "refresh token rejected")
			return
		}
		h.serverError(r.Context(), w, "rotation failed", err, zap.String("client_id", client.ClientID))
		return
	}
	h.issueTokens(r.Context(), w, u, *grant, stored.FamilyID)
}

// onRefreshReuse handles a detected refresh-token replay: revoke the token's
// family (RFC 6819 reuse detection — the compromised chain only), log a
// warning, and write an audit row (is_error → highlighted in the admin audit
// console). Best-effort: failures here never change the caller's already
// decided rejection.
func (h *Handler) onRefreshReuse(ctx context.Context, jti, userID, tenantID uuid.UUID) {
	revoked, err := h.refresh.RevokeFamilyOf(auth.WithActingTenant(ctx, tenantID), jti)
	if h.log != nil {
		h.log.Warn("oauth refresh token reuse detected; revoked the token family",
			zap.String("user_id", userID.String()),
			zap.Int64("revoked", revoked),
			zap.Error(err))
	}
	h.record(ctx, admindomain.AuditEntry{
		ActorSubject:  userID.String(),
		ActorTenantID: tenantID,
		Action:        actionRefreshReuse,
		ResourceName:  "users/" + userID.String(),
		ErrorMessage:  "rotated refresh token replayed; token family revoked",
	}, nil)
}

// tokenResponse is the RFC 6749 §5.1 success body.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// issueTokens mints an access token for the grant's audience + a rotating
// refresh token that records the grant, stores the refresh row, and writes
// the §5.1 JSON. false is a failure it has already answered.
func (h *Handler) issueTokens(ctx context.Context, w http.ResponseWriter, u authstore.User, grant authstore.OAuthGrant, familyID uuid.UUID) bool {
	if familyID == uuid.Nil() {
		familyID = uuid.NewV7()
	}
	access, accessExp, err := h.iss.MintAccess(issuer.AccessClaims{
		Subject:  u.UserID.String(),
		TenantID: u.TenantID,
		Audience: grant.Audience,
		Roles:    u.Roles,
		Scopes:   u.Scopes,
		Kind:     auth.PrincipalKindUser,
		// The grant rides in the token so every plane enforces the consent,
		// not only /authorize (ADR-0035).
		OAuthClientID: grant.ClientID,
		OAuthScopes:   grant.Scopes,
	})
	if err != nil {
		h.serverError(ctx, w, "mint access failed", err, zap.String("client_id", grant.ClientID))
		return false
	}
	tokenID := uuid.NewV7()
	refresh, refreshExp, err := h.iss.MintRefresh(issuer.RefreshClaims{
		Subject: u.UserID.String(), TenantID: u.TenantID, UserID: u.UserID, TokenID: tokenID,
	})
	if err != nil {
		h.serverError(ctx, w, "mint refresh failed", err, zap.String("client_id", grant.ClientID))
		return false
	}
	if err := h.refresh.Insert(auth.WithActingTenant(ctx, u.TenantID), r2RefreshToken(tokenID, familyID, u, grant, h.now(), refreshExp)); err != nil {
		h.serverError(ctx, w, "persist refresh failed", err, zap.String("client_id", grant.ClientID))
		return false
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(time.Until(accessExp).Seconds()),
		RefreshToken: refresh,
		Scope:        scopesJoin(grant.Scopes),
	})
	return true
}

func r2RefreshToken(jti, familyID uuid.UUID, u authstore.User, grant authstore.OAuthGrant, iat, exp time.Time) authstore.RefreshToken {
	return authstore.RefreshToken{
		JTI: jti, FamilyID: familyID, UserID: u.UserID, TenantID: u.TenantID, IssuedAt: iat, ExpiresAt: exp,
		OAuth: &grant,
	}
}

// authenticateClient enforces client authentication: public clients pass
// (PKCE is their proof), confidential clients must present a matching secret.
func (h *Handler) unsupportedGrant(w http.ResponseWriter) {
	supported := []string{GrantTypeAuthorizationCode, GrantTypeRefreshToken}
	if h.exchange != nil {
		supported = append(supported, GrantTypeTokenExchange)
	}
	h.jsonError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be one of "+strings.Join(supported, ", "))
}

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
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	// Scope is RFC 7591's: space-separated. ScopeList is this server's older
	// array form; the two are merged.
	Scope                   string   `json:"scope"`
	ScopeList               []string `json:"scope_list"`
	Audiences               []string `json:"audiences"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// registerResponse is the RFC 7591 §3.2.1 client information response.
type registerResponse struct {
	ClientID                string   `json:"client_id"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	Scope                   string   `json:"scope"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// The token endpoint authentication methods a client registers with.
const (
	authMethodNone        = "none"
	authMethodSecretBasic = "client_secret_basic"
)

// registrableAudiences are the audiences a client may register for. The
// IAM plane is not one: a client acts on a user's data, not their account.
var registrableAudiences = []string{auth.AudienceData, auth.AudienceAdmin, auth.AudienceMCP}

// defaultRegisteredScopes is the ceiling a client gets when it names none:
// both scopes, so the consent screen — where the user picks — decides.
var defaultRegisteredScopes = []string{auth.OAuthScopeRead, auth.OAuthScopeWrite}

// registeredAudiences is what a self-registering client may be issued: the
// audiences it named, each a registrable one, or by default the MCP server's
// when this server knows it — the client that registers itself is almost
// always an agent finding the MCP server — and else the data plane's.
func (h *Handler) registeredAudiences(requested []string) ([]string, bool) {
	if len(requested) == 0 {
		if h.mcpResource != "" {
			return []string{auth.AudienceMCP}, true
		}
		return []string{auth.AudienceData}, true
	}
	for _, a := range requested {
		if !slices.Contains(registrableAudiences, a) {
			return nil, false
		}
	}
	return requested, true
}

// registeredScopes merges the two spellings and falls back to the default.
func registeredScopes(req registerRequest) []string {
	scopes := append(splitScopes(req.Scope), req.ScopeList...)
	if len(scopes) == 0 {
		return slices.Clone(defaultRegisteredScopes)
	}
	slices.Sort(scopes)
	return slices.Compact(scopes)
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
	public := req.TokenEndpointAuthMethod == "" || req.TokenEndpointAuthMethod == authMethodNone
	audiences, ok := h.registeredAudiences(req.Audiences)
	if !ok {
		h.jsonError(w, http.StatusBadRequest, "invalid_client_metadata", "audiences must be among "+strings.Join(registrableAudiences, ", "))
		return
	}

	client := Client{
		ClientID:         "paladin-" + uuid.NewV7().String(),
		ClientName:       req.ClientName,
		RedirectURIs:     req.RedirectURIs,
		AllowedScopes:    registeredScopes(req),
		AllowedAudiences: audiences,
		Public:           public,
		SelfRegistered:   true,
	}
	method := authMethodNone
	if !public {
		method = authMethodSecretBasic
	}
	resp := registerResponse{
		ClientID:                client.ClientID,
		ClientIDIssuedAt:        h.now().Unix(),
		ClientName:              client.ClientName,
		RedirectURIs:            client.RedirectURIs,
		Scope:                   scopesJoin(client.AllowedScopes),
		GrantTypes:              []string{GrantTypeAuthorizationCode, GrantTypeRefreshToken},
		ResponseTypes:           []string{responseTypeCode},
		TokenEndpointAuthMethod: method,
	}
	if !public {
		secret, _, err := auth.GenerateApiKeySecret()
		if err != nil {
			h.serverError(r.Context(), w, "secret generation failed", err)
			return
		}
		hash, err := auth.HashApiKeySecret(secret)
		if err != nil {
			h.serverError(r.Context(), w, "secret hash failed", err)
			return
		}
		client.SecretHash = hash
		resp.ClientSecret = secret
	}
	if err := h.store.UpsertClient(r.Context(), client); err != nil {
		h.serverError(r.Context(), w, "registration failed", err)
		return
	}
	// Nobody is signed in: dynamic registration is open to any caller.
	h.record(r.Context(), admindomain.AuditEntry{
		Action:       actionClientRegistered,
		ResourceName: clientResource + client.ClientID,
	}, clientDetail{
		ClientName: client.ClientName, Audiences: client.AllowedAudiences,
		Scopes: client.AllowedScopes, Public: client.Public,
	})
	writeJSON(w, http.StatusCreated, resp)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// schemeHTTP is allowed for a native client's loopback redirect only (RFC
// 8252 §7.3): plain HTTP anywhere else would hand the code to the network.
const schemeHTTP = "http"

// loopbackHosts are the redirect hosts RFC 8252 counts as the device itself.
var loopbackHosts = []string{"localhost", "127.0.0.1", "::1"}

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
		if !strings.EqualFold(u.Scheme, s) {
			continue
		}
		if strings.EqualFold(u.Scheme, schemeHTTP) {
			return slices.Contains(loopbackHosts, strings.ToLower(u.Hostname()))
		}
		return true
	}
	return false
}

// registeredRedirect returns the client's registered URI equal to
// redirectURI, and whether there is one. The comparison is exact, as RFC 6749
// §3.1.2.3 requires.
func registeredRedirect(client Client, redirectURI string) (string, bool) {
	for _, u := range client.RedirectURIs {
		if u == redirectURI {
			return u, true
		}
	}
	return "", false
}

// resolveResource maps the MCP server's resource URL to its audience; any
// other resource indicator is already an audience name, or matches none.
func (h *Handler) resolveResource(resource string) string {
	if h.mcpResource != "" && resource == h.mcpResource {
		return auth.AudienceMCP
	}
	return resource
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

// redirectTo is base — always the client's registered redirect URI, never a
// value from the request — followed by params as its query. Appended after
// "?" (or "&" when base already has a query), nothing in params can move the
// redirect to another host; state is the caller's own value and goes there.
func redirectTo(base string, params url.Values) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + params.Encode()
}

func redirectWithCode(w http.ResponseWriter, r *http.Request, registered, code, state string) {
	params := url.Values{"code": {code}}
	if state != "" {
		params.Set("state", state)
	}
	http.Redirect(w, r, redirectTo(registered, params), http.StatusFound)
}

func (h *Handler) redirectError(w http.ResponseWriter, r *http.Request, registered, state, errCode, desc string) {
	if registered == "" {
		h.jsonError(w, http.StatusBadRequest, errCode, desc)
		return
	}
	params := url.Values{"error": {errCode}, "error_description": {desc}}
	if state != "" {
		params.Set("state", state)
	}
	http.Redirect(w, r, redirectTo(registered, params), http.StatusFound)
}

func (h *Handler) jsonError(w http.ResponseWriter, status int, errCode, desc string) {
	writeJSON(w, status, map[string]string{"error": errCode, "error_description": desc})
}

// tokenClient is the client a token request names, or false once it has
// answered: invalid_client for an unknown one, server_error for a store
// that could not say.
func (h *Handler) tokenClient(w http.ResponseWriter, r *http.Request, clientID string) (Client, bool) {
	client, err := h.store.GetClient(r.Context(), clientID)
	if err == nil {
		return client, true
	}
	if errors.Is(err, ErrClientNotFound) {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return Client{}, false
	}
	h.serverError(r.Context(), w, "client lookup failed", err, zap.String("client_id", clientID))
	return Client{}, false
}

// eligibleUser is the user a grant was issued to while it may still have
// one, or false once it has answered: invalid_grant for a user gone,
// disabled or moved to another tenant, server_error for a store that could
// not say.
func (h *Handler) eligibleUser(w http.ResponseWriter, r *http.Request, userID, tenantID uuid.UUID, clientID string) (authstore.User, bool) {
	u, err := h.users.GetByID(r.Context(), userID)
	if err != nil && !errors.Is(err, authstore.ErrNotFound) {
		h.serverError(r.Context(), w, "user lookup failed", err, zap.String("client_id", clientID))
		return authstore.User{}, false
	}
	if err != nil || u.Disabled || u.TenantID != tenantID {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "user no longer eligible")
		return authstore.User{}, false
	}
	return u, true
}

// serverError answers server_error and logs why. The endpoints are plain
// HTTP, outside the RPC interceptors that log a failed call, so without this
// a 500 left no trace: the client saw desc and the cause was gone.
func (h *Handler) serverError(ctx context.Context, w http.ResponseWriter, desc string, err error, fields ...zap.Field) {
	logServerError(ctx, desc, err, fields...)
	h.jsonError(w, http.StatusInternalServerError, errServerError, desc)
}

// logServerError logs a server error the endpoint answers with desc, for the
// paths that answer by redirect rather than JSON. Never pass a token, a code
// or a secret in fields.
func logServerError(ctx context.Context, desc string, err error, fields ...zap.Field) {
	logger.FromContext(ctx).Error(serverErrorMessage,
		append(fields, zap.String("reason", desc), zap.Error(err))...)
}

const (
	// errServerError is RFC 6749's error code for a failure of the server's own.
	errServerError = "server_error"
	// serverErrorMessage is the log line of every server error the OAuth
	// endpoints answer.
	serverErrorMessage = "oauth server error"
)

// applyCORS sets Access-Control-* headers on /oauth/token when the request's
// Origin is in the configured allow-list (or "*"). No-op when no origins are
// configured — server-to-server and native-form clients don't need CORS.
func (h *Handler) applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || len(h.corsOrigins) == 0 {
		return
	}
	allowed := ""
	for _, o := range h.corsOrigins {
		if o == "*" {
			allowed = "*"
			break
		}
		if o == origin {
			allowed = origin
			break
		}
	}
	if allowed == "" {
		return
	}
	hdr := w.Header()
	hdr.Set("Access-Control-Allow-Origin", allowed)
	if allowed != "*" {
		hdr.Add("Vary", "Origin")
	}
	hdr.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	hdr.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	hdr.Set("Access-Control-Max-Age", "600")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Sign in — {{.ClientName}}</title></head>
<body style="font-family:system-ui;max-width:28rem;margin:3rem auto">
{{if .SkipConsent}}
<h1>Sign in to {{.ClientName}}</h1>
<p>{{.ClientName}} is a trusted application.</p>
{{else}}
<h1>Authorize access</h1>
<p><strong>{{.ClientName}}</strong> ({{.ClientID}}) is requesting access{{if .Scopes}} to:</p>
<ul>{{range .Scopes}}<li>{{.}}</li>{{end}}</ul>{{else}}.</p>{{end}}
{{end}}
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
{{if .SkipConsent}}
  <input type="hidden" name="action" value="allow">
  <button type="submit">Sign in</button>
{{else}}
  <button name="action" value="allow" type="submit">Allow</button>
  <button name="action" value="deny" type="submit">Deny</button>
{{end}}
</form>
</body></html>`))

type consentView struct {
	authorizeParams
	ClientName  string
	Scopes      []string
	Error       string
	SkipConsent bool
}

// redirectToConsentUI hands the (already-validated) request off to the
// configured front-end consent page, forwarding the OAuth params as query
// string. The page collects credentials + the decision and POSTs them back to
// /oauth/authorize. errCode (optional) lets the page show "invalid credentials"
// on a retry.
func (h *Handler) redirectToConsentUI(w http.ResponseWriter, r *http.Request, p authorizeParams, errCode string, skip bool) {
	u, err := url.Parse(h.cfg.ConsentURL)
	if err != nil {
		h.serverError(r.Context(), w, "consent_url misconfigured", err)
		return
	}
	q := u.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("state", p.State)
	q.Set("code_challenge", p.CodeChallenge)
	q.Set("code_challenge_method", p.CodeChallengeMethod)
	if skip {
		// Pre-authorized client: tell the front-end page to render a plain
		// login (no per-user consent prompt). Advisory only — the decision is
		// re-derived server-side from config on the POST, never trusted from
		// this round-trip.
		q.Set("skip_consent", "1")
	}
	if p.Resource != "" {
		q.Set("resource", p.Resource)
	}
	if errCode != "" {
		q.Set("error", errCode)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (h *Handler) renderConsent(w http.ResponseWriter, client Client, p authorizeParams, errMsg string, skip bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = consentTmpl.Execute(w, consentView{
		authorizeParams: p,
		ClientName:      orDefault(client.ClientName, client.ClientID),
		Scopes:          splitScopes(p.Scope),
		Error:           errMsg,
		SkipConsent:     skip,
	})
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
