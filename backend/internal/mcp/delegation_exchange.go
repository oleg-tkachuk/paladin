package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/delegation"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// delegationCacheMaxEntries bounds the delegated-capability cache. One entry
// is one caller's token, so this is a few hundred agents at once; past it
// the expired entries go, and then everything.
const delegationCacheMaxEntries = 1024

// delegationErrorBodyLimit bounds how much of a refusal is read for its error.
const delegationErrorBodyLimit = 1 << 12

// The RFC 6749 §5.2 and RFC 8707 errors the exchange answers with, as far as
// they are the caller's to hear.
var delegationRefusals = map[string]error{
	"invalid_grant":  exchange.ErrInvalidGrant,
	"invalid_target": exchange.ErrInvalidTarget,
	"invalid_scope":  exchange.ErrInvalidScope,
}

// DelegationClient exchanges a caller's paladin-mcp token for the capability
// that acts for its user on every plane they may reach (ADR-0041), at the
// Authorization Server's token endpoint, as the confidential client the
// operator registered. The MCP server holds no capability key and reads no
// user: the exchange is the Authorization Server's. Each capability is
// cached per token until paladin.TokenRefreshMargin before it expires. Safe
// for concurrent use.
type DelegationClient struct {
	httpc    *http.Client
	tokenURL string
	clientID string
	secret   string
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]delegated
}

type delegated struct {
	token   string
	expires time.Time
}

// NewDelegationClient posts to tokenURL over httpc as clientID.
func NewDelegationClient(httpc *http.Client, tokenURL, clientID, secret string) *DelegationClient {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &DelegationClient{
		httpc: httpc, tokenURL: tokenURL, clientID: clientID, secret: secret,
		now: time.Now, cache: map[string]delegated{},
	}
}

// Capability returns subject exchanged for its delegated capability. A
// refusal the caller should hear is exchange.ErrInvalidGrant,
// ErrInvalidTarget or ErrInvalidScope; any other error is the server's.
func (d *DelegationClient) Capability(ctx context.Context, subject string) (string, error) {
	key := digest(subject)
	d.mu.Lock()
	hit, ok := d.cache[key]
	d.mu.Unlock()
	if ok && d.now().Before(hit.expires.Add(-paladin.TokenRefreshMargin)) {
		return hit.token, nil
	}

	got, err := d.exchange(ctx, subject)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.cache) >= delegationCacheMaxEntries {
		d.evictLocked()
	}
	d.cache[key] = got
	return got.token, nil
}

// evictLocked drops the expired entries, or all of them when none has.
func (d *DelegationClient) evictLocked() {
	now := d.now()
	for k, v := range d.cache {
		if !now.Before(v.expires) {
			delete(d.cache, k)
		}
	}
	if len(d.cache) >= delegationCacheMaxEntries {
		clear(d.cache)
	}
}

func (d *DelegationClient) exchange(ctx context.Context, subject string) (delegated, error) {
	form := url.Values{
		"grant_type":         {exchange.GrantTypeTokenExchange},
		"subject_token":      {subject},
		"subject_token_type": {exchange.TokenTypeAccessToken},
		"audience":           delegation.Planes,
	}
	// The token URL is the operator's configuration (mcp.oauth.exchange), never
	// a caller's input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.tokenURL, strings.NewReader(form.Encode())) // #nosec G704 -- configured token endpoint
	if err != nil {
		return delegated{}, fmt.Errorf("mcp: token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Raw, not form-encoded: the AS compares the Basic credentials as sent.
	req.SetBasicAuth(d.clientID, d.secret)
	resp, err := d.httpc.Do(req) // #nosec G704 -- configured token endpoint
	if err != nil {
		return delegated{}, fmt.Errorf("mcp: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var refusal struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, delegationErrorBodyLimit)).Decode(&refusal)
		if sentinel, ok := delegationRefusals[refusal.Error]; ok {
			return delegated{}, fmt.Errorf("%w: %s", sentinel, refusal.Description)
		}
		return delegated{}, fmt.Errorf("mcp: token exchange: %d %s: %s", resp.StatusCode, refusal.Error, refusal.Description)
	}
	var body struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return delegated{}, fmt.Errorf("mcp: token exchange response: %w", err)
	}
	if body.AccessToken == "" || body.IssuedTokenType != exchange.IssuedTokenTypeCapability {
		return delegated{}, fmt.Errorf("mcp: token exchange issued no capability but %q", body.IssuedTokenType)
	}
	return delegated{token: body.AccessToken, expires: d.now().Add(time.Duration(body.ExpiresIn) * time.Second)}, nil
}

// capabilitySource is what the edge exchanges a caller's token through.
type capabilitySource interface {
	Capability(ctx context.Context, subject string) (string, error)
}

// delegationRefusedMessage is the log line of a token the exchange refused.
const delegationRefusedMessage = "mcp token exchange refused"

// delegateCalls hands every request the capability its caller's token is
// exchanged for, in place of the token: the bridge's calls then reach the
// planes as the user, narrowed by the capability, and the paladin-mcp token
// never leaves the server. A request that brings a capability of its own
// keeps it, and loses the token, which no plane accepts.
func delegateCalls(next http.Handler, d capabilitySource, resourceMetadataURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		own := r.Header.Get(paladin.HeaderCapability)
		capability := own
		if own == "" {
			var err error
			if capability, err = d.Capability(r.Context(), BearerToken(r)); err != nil {
				refuseDelegation(w, r, err, resourceMetadataURL)
				return
			}
		}
		r = r.Clone(r.Context())
		r.Header.Del(paladin.HeaderAuthorization)
		r.Header.Del(legacyTokenHeader)
		r.Header.Set(paladin.HeaderCapability, capability)
		next.ServeHTTP(w, r)
	})
}

// refuseDelegation answers an exchange the caller cannot be granted as RFC
// 6750 does: a token no longer good for its user is invalid_token; a grant
// that reaches nothing, or consents to nothing, is insufficient_scope. Any
// other failure is the server's.
func refuseDelegation(w http.ResponseWriter, r *http.Request, err error, resourceMetadataURL string) {
	log := logger.FromContext(r.Context())
	switch {
	case errors.Is(err, exchange.ErrInvalidGrant):
		log.Info(delegationRefusedMessage, zap.Error(err))
		writeChallenge(w, resourceMetadataURL, "invalid_token", "the access token no longer acts for its user")
	case errors.Is(err, exchange.ErrInvalidTarget), errors.Is(err, exchange.ErrInvalidScope):
		log.Info(delegationRefusedMessage, zap.Error(err))
		writeChallengeStatus(w, http.StatusForbidden, resourceMetadataURL, "insufficient_scope", "the grant reaches no plane the user may use")
	default:
		log.Error("mcp token exchange failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
