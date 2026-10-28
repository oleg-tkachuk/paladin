package oauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
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
}

// errExchangeClient is a client the exchange does not serve.
var errExchangeClient = errors.New("the client may not exchange tokens")

// WithTokenExchange serves the token-exchange grant to clientIDs through ex
// (ADR-0041). Without it the grant type is unsupported.
func (h *Handler) WithTokenExchange(ex *exchange.Exchanger, clientIDs map[string]bool) (*Handler, error) {
	handler, err := exchange.NewHandler(exchange.HandlerConfig{
		Exchanger:    ex,
		Authenticate: h.exchangeClient,
		Logger:       slog.New(zapslog.NewHandler(h.log.Named("token-exchange").Core())),
	})
	if err != nil {
		return nil, fmt.Errorf("oauth: token exchange: %w", err)
	}
	h.exchange = &tokenExchange{handler: handler, clients: clientIDs}
	return h, nil
}

// tokenExchange answers the grant. The client is authenticated here, before
// limes' handler, so the two refusals RFC 6749 §5.2 tells apart stay apart:
// a client that fails authentication is invalid_client (401); one that
// passes but may not exchange is unauthorized_client (400). limes' handler
// reads the form as it stands, already parsed, and takes the client from
// the request's context.
func (h *Handler) tokenExchange(w http.ResponseWriter, r *http.Request) {
	clientID := r.PostForm.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = u
	}
	client, ok := h.tokenClient(w, r, clientID)
	if !ok {
		return
	}
	// A public client could present any agent's token.
	if client.Public || len(client.SecretHash) == 0 {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", "token exchange needs a confidential client")
		return
	}
	if err := h.authenticateClient(r, client); err != nil {
		h.jsonError(w, http.StatusUnauthorized, "invalid_client", err.Error())
		return
	}
	if !h.exchange.clients[client.ClientID] {
		logger.FromContext(r.Context()).Warn("token exchange refused", zap.String("client_id", client.ClientID), zap.Error(errExchangeClient))
		h.jsonError(w, http.StatusBadRequest, "unauthorized_client", errExchangeClient.Error())
		return
	}
	h.exchange.handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), exchangeClientKey{}, client.ClientID)))
}

// exchangeClientKey carries the client tokenExchange authenticated.
type exchangeClientKey struct{}

// exchangeClient is the client tokenExchange authenticated, for limes'
// handler. Its subject tokens must be paladin-mcp: issued to the MCP server
// it is.
func (h *Handler) exchangeClient(r *http.Request) (exchange.Client, error) {
	clientID, _ := r.Context().Value(exchangeClientKey{}).(string)
	if clientID == "" {
		return exchange.Client{}, errExchangeClient
	}
	return exchange.Client{ID: clientID, SubjectAudiences: []string{auth.AudienceMCP}}, nil
}
