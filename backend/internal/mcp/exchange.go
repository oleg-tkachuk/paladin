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

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// exchangeCacheMaxEntries bounds the exchanged-token cache. One entry is a
// caller's token for one plane, so this is a few hundred agents at once;
// past it the expired entries go, and then everything.
const exchangeCacheMaxEntries = 1024

// exchangeErrorBodyLimit bounds how much of a refusal is read for its error.
const exchangeErrorBodyLimit = 1 << 12

// ErrExchangeRefused is what every ExchangeRefusal is.
var ErrExchangeRefused = errors.New("mcp: token exchange refused")

// The RFC 6749 / RFC 8693 errors the Authorization Server answers an
// exchange with, as far as they tell the agent something different.
const (
	oauthErrInvalidGrant  = "invalid_grant"
	oauthErrInvalidTarget = "invalid_target"
	oauthErrSlowDown      = "slow_down"
)

// ExchangeRefusal is the Authorization Server refusing to exchange a caller's
// token for Audience. Code is the OAuth error, Description its words.
type ExchangeRefusal struct {
	Audience    string
	Status      int
	Code        string
	Description string
}

func (e *ExchangeRefusal) Error() string {
	return fmt.Sprintf("%s for %s: %d %s: %s", ErrExchangeRefused, e.Audience, e.Status, e.Code, e.Description)
}

func (e *ExchangeRefusal) Is(target error) bool { return target == ErrExchangeRefused }

// connectCode is the Connect code a call fails with when its token could not
// be exchanged. The caller's own token, or what it may hold, is the cause of
// most refusals; an unauthenticated or unauthorized MCP client is the
// operator's to fix and reads as an internal failure.
func (e *ExchangeRefusal) connectCode() connect.Code {
	switch e.Code {
	case oauthErrInvalidGrant:
		return connect.CodeUnauthenticated
	case oauthErrInvalidTarget:
		return connect.CodePermissionDenied
	case oauthErrSlowDown:
		return connect.CodeResourceExhausted
	default:
		return connect.CodeInternal
	}
}

// Exchanger trades a caller's paladin-mcp token for one per plane at the
// Authorization Server (RFC 8693, ADR-0036), as the confidential client the
// operator registered. Each exchanged token is cached per subject token and
// audience until paladin.TokenRefreshMargin before it expires. Safe for
// concurrent use.
type Exchanger struct {
	httpc    *http.Client
	tokenURL string
	clientID string
	secret   string
	now      func() time.Time

	mu    sync.Mutex
	cache map[exchangeKey]exchanged
}

type exchangeKey struct {
	subject  string // sha256 of the subject token: the cache holds no caller token
	audience string
}

type exchanged struct {
	token   string
	expires time.Time
}

// NewExchanger builds an Exchanger that posts to tokenURL over httpc.
func NewExchanger(httpc *http.Client, tokenURL, clientID, secret string) *Exchanger {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &Exchanger{
		httpc: httpc, tokenURL: tokenURL, clientID: clientID, secret: secret,
		now: time.Now, cache: map[exchangeKey]exchanged{},
	}
}

// Token returns subject exchanged for audience.
func (e *Exchanger) Token(ctx context.Context, subject, audience string) (string, error) {
	key := exchangeKey{subject: digest(subject), audience: audience}
	e.mu.Lock()
	hit, ok := e.cache[key]
	e.mu.Unlock()
	if ok && e.now().Before(hit.expires.Add(-paladin.TokenRefreshMargin)) {
		return hit.token, nil
	}

	got, err := e.exchange(ctx, subject, audience)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cache) >= exchangeCacheMaxEntries {
		e.evictLocked()
	}
	e.cache[key] = got
	return got.token, nil
}

// Invalidate drops every cached token for audience: the plane refused one,
// and the SDK does not say whose.
func (e *Exchanger) Invalidate(audience string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k := range e.cache {
		if k.audience == audience {
			delete(e.cache, k)
		}
	}
}

// evictLocked drops the expired entries, or all of them when none has.
func (e *Exchanger) evictLocked() {
	now := e.now()
	for k, v := range e.cache {
		if !now.Before(v.expires) {
			delete(e.cache, k)
		}
	}
	if len(e.cache) >= exchangeCacheMaxEntries {
		clear(e.cache)
	}
}

func (e *Exchanger) exchange(ctx context.Context, subject, audience string) (exchanged, error) {
	form := url.Values{
		"grant_type":         {oauth.GrantTypeTokenExchange},
		"client_id":          {e.clientID},
		"subject_token":      {subject},
		"subject_token_type": {oauth.TokenTypeAccessToken},
		"audience":           {audience},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return exchanged{}, fmt.Errorf("mcp: token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Raw, not form-encoded: the AS compares the Basic credentials as sent.
	req.SetBasicAuth(e.clientID, e.secret)
	resp, err := e.httpc.Do(req)
	if err != nil {
		return exchanged{}, fmt.Errorf("mcp: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var refusal struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, exchangeErrorBodyLimit)).Decode(&refusal)
		r := &ExchangeRefusal{Audience: audience, Status: resp.StatusCode, Code: refusal.Error, Description: refusal.Description}
		// A Connect error, so the SDK keeps its code rather than reading an
		// unknown failure as the plane being unavailable.
		return exchanged{}, connect.NewError(r.connectCode(), r.Error()).WithCause(r)
	}
	var body struct {
		AccessToken     string `json:"access_token"`
		IssuedTokenType string `json:"issued_token_type"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return exchanged{}, fmt.Errorf("mcp: token exchange response: %w", err)
	}
	if body.AccessToken == "" || body.IssuedTokenType != oauth.TokenTypeAccessToken {
		return exchanged{}, fmt.Errorf("mcp: token exchange issued no access token for %s", audience)
	}
	return exchanged{
		token:   body.AccessToken,
		expires: e.now().Add(time.Duration(body.ExpiresIn) * time.Second),
	}, nil
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
