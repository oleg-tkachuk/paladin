package oauth

import (
	"errors"
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// The RFC 7009 parameters and token type hints /oauth/revoke reads.
const (
	revokeParamToken        = "token"
	revokeParamTypeHint     = "token_type_hint"
	tokenTypeHintAccess     = "access_token"
	errUnsupportedTokenType = "unsupported_token_type"
)

// handleRevoke is the RFC 7009 revocation endpoint: a client gives up the
// grant behind a refresh token it holds, which ends every token of that
// grant, as revoking it on the connected applications page does.
//
// An access token cannot be revoked: it is a short-lived signed JWT that no
// plane looks up, so the answer is unsupported_token_type (§2.2.1) rather than
// a 200 that would claim it stopped working. A token this server cannot read
// or no longer holds is answered 200, as §2.2 requires: the client's aim, that
// the token not work, already holds.
func (h *Handler) handleRevoke(w http.ResponseWriter, r *http.Request) {
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
	clientID := r.PostForm.Get("client_id")
	if ok, retry := h.limiter.allow(clientID); !ok {
		secs := max(int(retry.Seconds()), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		h.jsonError(w, http.StatusTooManyRequests, "slow_down", "too many requests; retry later")
		return
	}
	client, ok := h.tokenClient(w, r, clientID)
	if !ok {
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	token := r.PostForm.Get(revokeParamToken)
	if token == "" {
		h.jsonError(w, http.StatusBadRequest, "invalid_request", "token required")
		return
	}
	if r.PostForm.Get(revokeParamTypeHint) == tokenTypeHintAccess {
		h.jsonError(w, http.StatusBadRequest, errUnsupportedTokenType, "access tokens expire on their own and cannot be revoked")
		return
	}

	jti, userID, tenantID, err := h.dec.DecodeRefresh(token)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	stored, err := h.refresh.GetAny(r.Context(), jti)
	if errors.Is(err, authstore.ErrNotFound) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		h.serverError(r.Context(), w, "refresh token lookup failed", err, zap.String("client_id", clientID))
		return
	}
	// Only the client a grant was issued to may end it (§2.1); a console
	// session's token is no client's.
	if stored.OAuth == nil || stored.OAuth.ClientID != client.ClientID {
		h.jsonError(w, http.StatusBadRequest, "invalid_grant", "token was not issued to this client")
		return
	}
	if _, err := h.refresh.RevokeFamilyOf(auth.WithActingTenant(r.Context(), stored.TenantID), jti); err != nil {
		h.serverError(r.Context(), w, "revocation failed", err, zap.String("client_id", clientID))
		return
	}
	// The refresh tokens are gone; what the grant's tokens were exchanged
	// for goes too, or it would outlive the grant until its token expired.
	// A failure is answered so the client retries: both steps repeat safely.
	if _, err := h.revokeSession(r.Context(), stored.TenantID, stored.FamilyID, noteGrantRevokedByClient); err != nil {
		h.serverError(r.Context(), w, "session capability revocation failed", err, zap.String("client_id", clientID))
		return
	}
	h.record(r.Context(), admindomain.AuditEntry{
		ActorSubject:  userID.String(),
		ActorTenantID: tenantID,
		Action:        actionGrantRevokedByClient,
		ResourceName:  clientResource + client.ClientID,
	}, grantDetail{Audience: stored.OAuth.Audience, Scopes: stored.OAuth.Scopes})
	w.WriteHeader(http.StatusOK)
}
