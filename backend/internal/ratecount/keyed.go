package ratecount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// Key prefixes of the limiters keyed by what a caller sends: a login's
// address and subject, an OAuth client_id. Each counts apart from the others
// and from the tenants and tokens.
const (
	loginIPKeyPrefix       = "login-ip:"
	loginSubjectKeyPrefix  = "login-subject:"
	oauthTokenKeyPrefix    = "oauth-token:" // #nosec G101 -- a key prefix, not a credential
	oauthExchangeKeyPrefix = "oauth-exchange:"
)

// digestLengthEnd ends the length written before each part digest hashes.
const digestLengthEnd = ':'

// digest is parts as one fixed-length key: a SHA-256 of each part with its
// length, so no part can be moved into its neighbour to meet another key. A
// caller chooses these values; hashed, a login name or a client_id never
// reaches the store in clear, and a long one makes no longer key.
func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{digestLengthEnd})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// LoginIPKey is the key a client address's credential-bearing calls count
// under.
func LoginIPKey(ip string) string { return loginIPKeyPrefix + digest(ip) }

// LoginSubjectKey is the key one subject's credential-bearing calls from one
// address count under.
func LoginSubjectKey(subject, ip string) string {
	return loginSubjectKeyPrefix + digest(subject, ip)
}

// OAuthTokenKey is the key a client's /oauth/token and /oauth/revoke calls
// count under.
func OAuthTokenKey(clientID string) string { return oauthTokenKeyPrefix + digest(clientID) }

// OAuthExchangeKey is the key a client's token-exchange grants count under,
// apart from its other grants.
func OAuthExchangeKey(clientID string) string { return oauthExchangeKeyPrefix + digest(clientID) }

// KeyLimiter holds each key to a per-window ceiling on a Counter.
type KeyLimiter struct{ counter Counter }

// NewKeyLimiter wires the adapter.
func NewKeyLimiter(c Counter) *KeyLimiter { return &KeyLimiter{counter: c} }

// Allow counts one call under key when it fits capacity, and otherwise
// reports how long until one would. Only an admitted call is counted.
// capacity <= 0 sets no ceiling. An error is the store's, with nothing
// decided: the caller chooses whether to admit.
func (l *KeyLimiter) Allow(ctx context.Context, key string, capacity int) (allowed bool, retryAfter time.Duration, err error) {
	if capacity <= 0 {
		return true, 0, nil
	}
	c, err := l.counter.BumpIfFits(ctx, key, capacity)
	if err != nil {
		return false, 0, fmt.Errorf("ratecount: bump %s: %w", key, err)
	}
	if c.Admitted {
		return true, 0, nil
	}
	return false, ratelimit.RetryAfter(c.Current, c.Previous, c.Elapsed, capacity), nil
}
