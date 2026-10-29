package oauth

import (
	"context"
	"encoding/json"
	"uuid"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// The actions of the audit rows the Authorization Server writes. Its
// endpoints are plain HTTP, outside the audit interceptor, so it writes the
// rows that change who may reach the platform itself: a client registering,
// a user granting or refusing one, and a grant's first tokens. Refreshing a
// grant and exchanging its token write none, as the interceptor writes none
// for a console session's refresh: they run every few minutes for every
// agent, and would bury the rows the trail is for.
const (
	actionClientRegistered = "iam.OAuthClientRegistered"
	actionConsentGranted   = "iam.OAuthConsentGranted"
	actionConsentDenied    = "iam.OAuthConsentDenied"
	actionGrantRedeemed    = "iam.OAuthGrantRedeemed"
	actionRefreshReuse     = "iam.RefreshTokenReuseDetected"
	// actionGrantRevokedByClient is a client giving up a grant it holds at
	// /oauth/revoke, as an agent does when it disconnects.
	actionGrantRevokedByClient = "iam.OAuthGrantRevokedByClient"
	// noteGrantRevokedByClient and noteRefreshReuse are why a session's
	// capabilities were revoked, as their revocation records say.
	noteGrantRevokedByClient = "the client revoked its OAuth grant"
	noteRefreshReuse         = "a rotated refresh token of the session was replayed"
)

// clientResource names a client in the audit log, as OAuthClientService does.
const clientResource = "oauthClients/"

// grantDetail is what an audit row records of a grant.
type grantDetail struct {
	Audience string   `json:"audience,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// clientDetail is what an audit row records of a registration.
type clientDetail struct {
	ClientName string   `json:"client_name,omitempty"`
	Audiences  []string `json:"audiences"`
	Scopes     []string `json:"scopes"`
	Public     bool     `json:"public"`
}

// record writes an audit row of the Authorization Server's, with the
// caller's address, and logs a row that could not be written. A nil writer
// writes nothing.
func (h *Handler) record(ctx context.Context, e admindomain.AuditEntry, detail any) {
	if h.audit == nil {
		return
	}
	e.EntryID = uuid.NewV7()
	e.At = h.now().UTC()
	e.ActorAudience = auth.AudienceIAM
	if a, ok := clientip.FromContext(ctx); ok {
		e.SourceIP = a.String()
	}
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			e.RequestJSON = b
		}
	}
	if err := h.audit.Insert(ctx, e); err != nil {
		logger.FromContext(ctx).Error("audit entry not written",
			zap.String("action", e.Action), zap.Error(err))
	}
}
