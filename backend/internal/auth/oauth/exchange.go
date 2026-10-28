package oauth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"

	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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

// tokenExchange answers the grant. The form is already parsed, which limes'
// handler reads as it stands.
func (h *Handler) tokenExchange(w http.ResponseWriter, r *http.Request) {
	h.exchange.handler.ServeHTTP(w, r)
}

// exchangeClient authenticates a client of the exchange: one of the
// operator's, confidential, by its secret. A public client could present
// any agent's token. Its subject tokens must be paladin-mcp: issued to the
// MCP server it is.
func (h *Handler) exchangeClient(r *http.Request) (exchange.Client, error) {
	clientID := r.PostForm.Get("client_id")
	if u, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = u
	}
	if !h.exchange.clients[clientID] {
		return exchange.Client{}, fmt.Errorf("%w: %q", errExchangeClient, clientID)
	}
	client, err := h.store.GetClient(r.Context(), clientID)
	if err != nil {
		if !errors.Is(err, ErrClientNotFound) {
			logServerError(r.Context(), "client lookup failed", err, zap.String("client_id", clientID))
		}
		return exchange.Client{}, fmt.Errorf("client %q: %w", clientID, err)
	}
	if client.Public || len(client.SecretHash) == 0 {
		return exchange.Client{}, fmt.Errorf("client %q: token exchange needs a confidential client", clientID)
	}
	if err := h.authenticateClient(r, client); err != nil {
		return exchange.Client{}, fmt.Errorf("client %q: %w", clientID, err)
	}
	return exchange.Client{ID: clientID, SubjectAudiences: []string{auth.AudienceMCP}}, nil
}
