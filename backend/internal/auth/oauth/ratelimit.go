package oauth

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// RateLimiter holds each key to a per-window ceiling. ratecount.KeyLimiter is
// one, on the store middleware.rate_limit.backend names, so every replica of
// the AS counts a client against one ceiling (ADR-0046).
type RateLimiter interface {
	// Allow counts one call under key when it fits capacity, and otherwise
	// reports how long until one would. An error decides nothing.
	Allow(ctx context.Context, key string, capacity int) (allowed bool, retryAfter time.Duration, err error)
}

// defaultTokenRatePerMinute is a client's ceiling when the configuration
// sets none.
const defaultTokenRatePerMinute = 60

// perMinute is a configured ceiling, or the default for none.
func perMinute(configured int) int {
	if configured <= 0 {
		return defaultTokenRatePerMinute
	}
	return configured
}

// OAuth error codes and descriptions a throttled call is answered with.
const (
	errCodeSlowDown               = "slow_down"
	errCodeTemporarilyUnavailable = "temporarily_unavailable"
	msgTooManyRequests            = "too many requests; retry later"
	msgLimitUndecided             = "the request rate could not be checked; retry later"
)

// throttled answers a call its client may not make yet, and reports whether
// it did. It runs before any database work, to blunt code and secret
// brute-forcing (ADR-0009 hardening). A limit that cannot be decided refuses
// the call: these calls carry client secrets and codes.
func (h *Handler) throttled(w http.ResponseWriter, r *http.Request, key, clientID string, capacity int) bool {
	ok, retryAfter, err := h.rate.Allow(r.Context(), key, capacity)
	if err != nil {
		h.log.Error("oauth rate limit could not be decided; refusing the call",
			zap.String("path", r.URL.Path), zap.String("client_id", clientID), zap.Error(err))
		h.jsonError(w, http.StatusServiceUnavailable, errCodeTemporarilyUnavailable, msgLimitUndecided)
		return true
	}
	if ok {
		return false
	}
	secs := max(int(math.Ceil(retryAfter.Seconds())), 1)
	h.log.Warn("oauth call refused by its client's rate limit",
		zap.String("path", r.URL.Path), zap.String("client_id", clientID), zap.Int("retry_after_seconds", secs))
	w.Header().Set(paladin.HeaderRetryAfter, strconv.Itoa(secs))
	h.jsonError(w, http.StatusTooManyRequests, errCodeSlowDown, msgTooManyRequests)
	return true
}
