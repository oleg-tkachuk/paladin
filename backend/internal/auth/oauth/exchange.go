package oauth

import (
	"errors"
	"net/http"
	"time"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
)

// The endpoints Mount serves.
const (
	AuthorizePath = "/oauth/authorize"
	TokenPath     = "/oauth/token" // #nosec G101 -- an endpoint path, not a credential
	// RevokePath is the RFC 7009 revocation endpoint.
	RevokePath = "/oauth/revoke"
)

// The grant types /oauth/token serves, and the RFC 8693 token type it
// exchanges.
const (
	GrantTypeAuthorizationCode = "authorization_code"
	GrantTypeRefreshToken      = "refresh_token"
	GrantTypeTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange" // #nosec G101 -- an RFC 8693 identifier, not a credential
	// TokenTypeAccessToken is the only subject and issued token type.
	TokenTypeAccessToken = "urn:ietf:params:oauth:token-type:access_token" // #nosec G101 -- an RFC 8693 identifier, not a credential
)

// tokenExchange is the configuration of the RFC 8693 grant (ADR-0036).
type tokenExchange struct {
	// subjects verifies a subject token, and accepts only paladin-mcp.
	subjects auth.TokenVerifier
	// clients may call the grant: the MCP server.
	clients map[string]bool
}

// exchangeResponse is the RFC 8693 §2.2.1 success body. No refresh token:
// the exchanged token lives no longer than the one it was exchanged for.
type exchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	Scope           string `json:"scope,omitempty"`
}

// tokenExchange trades the caller's paladin-mcp token for a token to one
// plane. The result is still the user's — subject, tenant, roles, and the
// OAuth grant the agent holds, so ADR-0035's write rule applies downstream
// — with an `act` claim naming the client that relayed it. It expires with
// the subject token.
func (h *Handler) tokenExchange(w http.ResponseWriter, r *http.Request) {
	clientID := r.PostForm.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = u
	}
	client, ok := h.tokenClient(w, r, clientID)
	if !ok {
		return
	}
	// A public client could present any agent's token; the exchange is for
	// the one confidential client the operator named.
	if client.Public || len(client.SecretHash) == 0 {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", "token exchange needs a confidential client")
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	if !h.exchange.clients[client.ClientID] {
		h.jsonError(w, http.StatusBadRequest, "unauthorized_client", "the client may not exchange tokens")
		return
	}
	if r.PostForm.Get("subject_token_type") != TokenTypeAccessToken {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "subject_token_type must be "+TokenTypeAccessToken)
		return
	}

	subject, err := h.exchange.subjects.Verify(r.Context(), r.PostForm.Get("subject_token"))
	if err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "subject token is not a valid "+auth.AudienceMCP+" token")
		return
	}
	// paladin-mcp is issued only by /authorize, so it always carries a grant;
	// one without would leave the exchanged token unbounded by any consent.
	if !subject.ActsForOAuthClient() {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "subject token carries no OAuth grant")
		return
	}
	userID, err := uuid.Parse(subject.Subject)
	if err != nil {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "subject token names no user")
		return
	}
	u, ok := h.eligibleUser(w, r, userID, subject.TenantID, clientID)
	if !ok {
		return
	}

	audience := r.PostForm.Get("audience")
	if err := apiutil.AudienceEligible(u.Roles, audience); err != nil {
		desc := "audience must be a plane's"
		if errors.Is(err, apiutil.ErrAudienceRole) {
			desc = "the user may not be issued " + audience
		}
		h.jsonError(w, http.StatusBadRequest, "invalid_target", desc)
		return
	}
	ttl := subject.ExpiresAt.Sub(h.now())
	if subject.ExpiresAt.IsZero() || ttl <= 0 {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "subject token has expired")
		return
	}

	access, accessExp, err := h.iss.MintAccess(issuer.AccessClaims{
		Subject:       u.UserID.String(),
		TenantID:      u.TenantID,
		Audience:      audience,
		Roles:         u.Roles,
		Scopes:        u.Scopes,
		Kind:          auth.PrincipalKindUser,
		OAuthClientID: subject.OAuthClientID,
		OAuthScopes:   subject.OAuthScopes,
		Actor:         client.ClientID,
		TTL:           ttl,
	})
	if err != nil {
		h.serverError(r.Context(), w, "mint access failed", err, zap.String("client_id", client.ClientID))
		return
	}
	writeJSON(w, http.StatusOK, exchangeResponse{
		AccessToken:     access,
		IssuedTokenType: TokenTypeAccessToken,
		TokenType:       "Bearer",
		ExpiresIn:       int(accessExp.Sub(h.now()) / time.Second),
		Scope:           scopesJoin(subject.OAuthScopes),
	})
}
