package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// The endpoints Mount serves.
const (
	AuthorizePath = "/oauth/authorize"
	TokenPath     = "/oauth/token" // #nosec G101 -- an endpoint path, not a credential
	// RevokePath is the RFC 7009 revocation endpoint.
	RevokePath = "/oauth/revoke"
	// RegisterPath is the RFC 7591 dynamic client registration endpoint.
	RegisterPath = "/oauth/register"
)

// The grant types /oauth/token serves, and the RFC 8693 token type it
// exchanges.
const (
	GrantTypeAuthorizationCode = "authorization_code"
	GrantTypeRefreshToken      = "refresh_token"
	GrantTypeTokenExchange     = exchange.GrantTypeTokenExchange
	// TokenTypeAccessToken is the subject token type the exchange takes.
	TokenTypeAccessToken = exchange.TokenTypeAccessToken
)

// tokenExchange is the RFC 8693 grant of ADR-0041: limes' token endpoint,
// trading a paladin-mcp token for the capability that acts for its user,
// for the confidential clients the operator named.
type tokenExchange struct {
	handler http.Handler
	clients map[string]bool
	// sessions revokes the capabilities a grant's session was exchanged
	// for, when the session ends.
	sessions limes.BulkRevoker
}

// WithTokenExchange serves the token-exchange grant to clientIDs through ex
// (ADR-0041), and revokes through sessions what a grant's tokens were
// exchanged for when its session ends. Without it the grant type is
// unsupported.
func (h *Handler) WithTokenExchange(ex *exchange.Exchanger, clientIDs map[string]bool, sessions limes.BulkRevoker) (*Handler, error) {
	handler, err := exchange.NewHandler(exchange.HandlerConfig{
		Exchanger:    ex,
		Authenticate: h.exchangeClient,
		Logger:       slog.New(zapslog.NewHandler(h.log.Named("token-exchange").Core())),
	})
	if err != nil {
		return nil, fmt.Errorf("oauth: token exchange: %w", err)
	}
	h.exchange = &tokenExchange{handler: handler, clients: clientIDs, sessions: sessions}
	return h, nil
}

// tokenExchange answers the grant. The form is already parsed, which limes'
// handler reads as it stands.
func (h *Handler) tokenExchange(w http.ResponseWriter, r *http.Request) {
	h.exchange.handler.ServeHTTP(w, r)
}

// exchangeClient authenticates a client of the exchange by its secret, and
// lets it exchange when the operator named it. limes answers a client that
// fails authentication as invalid_client, one that passes but may not
// exchange as unauthorized_client (exchange.ErrUnauthorizedClient), and a
// client store that cannot be read as server_error (exchange.ErrServerError).
// A public client could present any agent's token, so it fails
// authentication. Its subject tokens must be paladin-mcp: issued to the MCP
// server it is.
func (h *Handler) exchangeClient(r *http.Request) (exchange.Client, error) {
	clientID := r.PostForm.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = u
	}
	client, err := h.store.GetClient(r.Context(), clientID)
	switch {
	case errors.Is(err, ErrClientNotFound):
		return exchange.Client{}, fmt.Errorf("client %q: %w", clientID, err)
	case err != nil:
		// The server's failure, which limes answers as server_error.
		logServerError(r.Context(), "client lookup failed", err, zap.String("client_id", clientID))
		return exchange.Client{}, fmt.Errorf("%w: client %q: %w", exchange.ErrServerError, clientID, err)
	}
	if client.Public || len(client.SecretHash) == 0 {
		return exchange.Client{}, fmt.Errorf("client %q: token exchange needs a confidential client", clientID)
	}
	if err := h.authenticateClient(r, client); err != nil {
		return exchange.Client{}, fmt.Errorf("client %q: %w", clientID, err)
	}
	if !h.exchange.clients[clientID] {
		return exchange.Client{}, fmt.Errorf("%w: %q", exchange.ErrUnauthorizedClient, clientID)
	}
	return exchange.Client{ID: clientID, SubjectAudiences: []string{auth.AudienceMCP}}, nil
}

// sessionRevocationActor is who a capability revoked with its session is
// recorded as revoked by: the authorization server, ending the grant.
const sessionRevocationActor = "oauth"

// revokeSession revokes every capability of tenant that a token of the
// grant's session, the refresh-token family, was exchanged for, batch by
// batch until none is left, and reports how many it revoked; why is the
// note on each. Without the exchange there is none to revoke.
func (h *Handler) revokeSession(ctx context.Context, tenant, family uuid.UUID, why string) (int64, error) {
	if h.exchange == nil || h.exchange.sessions == nil {
		return 0, nil
	}
	var revoked int64
	for {
		res, err := h.exchange.sessions.RevokeMatching(auth.WithActingTenant(ctx, tenant), limes.RevokeMatchingRequest{
			TenantID: tenant, Session: family.String(), Note: why, Actor: sessionRevocationActor,
		})
		if err != nil {
			return revoked, fmt.Errorf("oauth: revoke the capabilities of session %s: %w", family, err)
		}
		revoked += res.Revoked
		if !res.More {
			return revoked, nil
		}
	}
}
