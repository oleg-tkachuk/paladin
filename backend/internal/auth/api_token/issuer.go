package api_token

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Issuer mints API tokens. Persist-then-disclose ordering: insert the
// row, then return the plaintext to the caller. If the insert fails the
// caller never sees a token; if disclosure fails (transport problem
// outside this package's control) the row exists and is discoverable
// via ListByTenant — operator can revoke before any caller could use it.
type Issuer struct {
	store Store
	clock func() time.Time

	// MaxTTL caps Issue's TTL parameter. Default 1y. Service tokens
	// shouldn't live forever — if a customer needs a longer-lived
	// secret, they're using the wrong primitive (consider OIDC client
	// credentials with their IdP).
	MaxTTL time.Duration
}

// IssuerConfig is the wiring shape.
type IssuerConfig struct {
	Store  Store
	Now    func() time.Time
	MaxTTL time.Duration
}

// NewIssuer validates wiring and returns a ready Issuer.
func NewIssuer(cfg IssuerConfig) (*Issuer, error) {
	if cfg.Store == nil {
		return nil, errors.New("api_token: IssuerConfig.Store required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxTTL == 0 {
		cfg.MaxTTL = 365 * 24 * time.Hour
	}
	return &Issuer{
		store:  cfg.Store,
		clock:  cfg.Now,
		MaxTTL: cfg.MaxTTL,
	}, nil
}

// IssueRequest is the input shape for Issuer.Issue.
type IssueRequest struct {
	TenantID     uuid.UUID
	Name         string
	Scopes       []string
	Audience     []string
	TTL          time.Duration // 0 → MaxTTL
	RateLimitRPM int           // 0 → unlimited
	CreatedBy    string
}

// Issue mints a new token, persists the row, returns the typed Token
// (with Plaintext populated). Plaintext is shown ONCE — caller stashes
// it and ships it to the consuming service; the database never sees it.
func (i *Issuer) Issue(ctx context.Context, req IssueRequest) (*Token, error) {
	if req.TenantID == uuid.Nil {
		return nil, errors.New("api_token: TenantID required")
	}
	if req.Name == "" {
		return nil, errors.New("api_token: Name required")
	}
	if len(req.Audience) == 0 {
		return nil, errors.New("api_token: Audience required (at least one plane)")
	}
	ttl := req.TTL
	if ttl == 0 {
		ttl = i.MaxTTL
	}
	if ttl > i.MaxTTL {
		return nil, fmt.Errorf("api_token: TTL %s exceeds max %s", ttl, i.MaxTTL)
	}

	plaintext, prefix, hash, err := GenerateToken()
	if err != nil {
		return nil, err
	}

	if req.RateLimitRPM < 0 {
		return nil, errors.New("api_token: RateLimitRPM must be >= 0")
	}

	// Normalise nil → empty slice. The api_tokens.scopes / .audience
	// columns are `text[] NOT NULL`. pgx serialises a nil []string as
	// SQL NULL (not '{}'), which trips the NOT NULL constraint even
	// though `scopes` carries a `DEFAULT '{}'::text[]` — DEFAULT only
	// applies when the column is omitted from the INSERT, not when an
	// explicit NULL is supplied. Callers that send an empty `repeated
	// string scopes = N` over Connect end up here with a nil slice
	// because protobuf reflection materialises an unset repeated field
	// as nil, and we'd rather fix it once at the persistence boundary
	// than push the contract onto every Connect handler.
	scopes := req.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	audience := req.Audience
	if audience == nil {
		audience = []string{}
	}

	now := i.clock().UTC()
	tok := Token{
		ID:           uuid.New(),
		TenantID:     req.TenantID,
		Name:         req.Name,
		Prefix:       prefix,
		Scopes:       scopes,
		Audience:     audience,
		ExpiresAt:    now.Add(ttl),
		RateLimitRPM: req.RateLimitRPM,
		CreatedBy:    req.CreatedBy,
		CreatedAt:    now,
		Plaintext:    plaintext,
	}

	if err := i.store.Insert(ctx, tok, hash); err != nil {
		// Caller never sees the plaintext on a persistence failure;
		// scrub from the in-memory struct in case the caller logs it
		// from the partial value.
		tok.Plaintext = ""
		return nil, fmt.Errorf("api_token: persist: %w", err)
	}
	return &tok, nil
}
