package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// delegationCacheMaxEntries bounds the delegated-capability cache. One entry
// is one caller's token, so this is a few hundred agents at once; past it
// the expired entries go, and then everything.
const delegationCacheMaxEntries = 1024

// delegationPlanes is every plane a delegated capability is asked for; the
// policy keeps those the user may hold.
var delegationPlanes = []string{auth.TokenPlaneData, auth.TokenPlaneAdmin, auth.TokenPlaneIAM}

// DelegationExchanger exchanges a caller's paladin-mcp token for the
// capability that acts for its user on every plane they may reach
// (ADR-0041), in process, through limes' exchange. Each capability is
// cached per token until paladin.TokenRefreshMargin before it expires.
// Safe for concurrent use.
type DelegationExchanger struct {
	ex  *exchange.Exchanger
	now func() time.Time

	mu    sync.Mutex
	cache map[string]delegated
}

type delegated struct {
	token   string
	expires time.Time
}

// NewDelegationExchanger builds the exchange over the delegation issuer:
// subjects reads the callers' tokens, users finds their users, and each
// capability lives at most ttl.
func NewDelegationExchanger(issuer *limes.Issuer, subjects exchange.SubjectVerifier, users UserDirectory, ttl time.Duration, log *zap.Logger) (*DelegationExchanger, error) {
	ex, err := exchange.NewExchanger(exchange.Config{
		Issuer:   issuer,
		Subjects: subjects,
		Policy:   DelegationPolicy(users, ttl),
		Logger:   SDKLogger(log),
	})
	if err != nil {
		return nil, fmt.Errorf("mcp: delegation exchange: %w", err)
	}
	return &DelegationExchanger{ex: ex, now: time.Now, cache: map[string]delegated{}}, nil
}

// Capability returns subject exchanged for its delegated capability.
func (d *DelegationExchanger) Capability(ctx context.Context, subject string) (string, error) {
	key := digest(subject)
	d.mu.Lock()
	hit, ok := d.cache[key]
	d.mu.Unlock()
	if ok && d.now().Before(hit.expires.Add(-paladin.TokenRefreshMargin)) {
		return hit.token, nil
	}

	res, err := d.ex.Exchange(ctx, exchange.Request{
		Client:       exchange.Client{ID: auth.DelegationIssuerSubject, SubjectAudiences: []string{auth.AudienceMCP}},
		SubjectToken: subject,
		Audiences:    delegationPlanes,
	})
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.cache) >= delegationCacheMaxEntries {
		d.evictLocked()
	}
	d.cache[key] = delegated{token: res.Token, expires: res.Capability.ExpiresAt}
	return res.Token, nil
}

// evictLocked drops the expired entries, or all of them when none has.
func (d *DelegationExchanger) evictLocked() {
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

// delegationRefusedMessage is the log line of a token the exchange refused.
const delegationRefusedMessage = "mcp token exchange refused"

// delegateCalls hands every request the capability its caller's token is
// exchanged for, in place of the token: the bridge's calls then reach the
// planes as the user, narrowed by the capability, and the paladin-mcp token
// never leaves the server. A request that brings a capability of its own
// keeps it, and loses the token, which no plane accepts.
func delegateCalls(next http.Handler, d *DelegationExchanger, resourceMetadataURL string) http.Handler {
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
