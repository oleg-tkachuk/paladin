// Package ratecount counts requests in the sliding one-minute window the
// per-tenant and per-token rate limits share (ADR-0046). A Counter is the
// store the count lives in: Valkey, Postgres synced from memory, or this
// process alone. TenantStore and TokenLimiter adapt one to each limiter.
package ratecount

import (
	"context"
	"time"

	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
)

// Window is the width of one bucket and of the sliding window, shared with
// the Postgres limiters so every backend weights the same way.
const Window = ratelimit.Window

// Key prefixes keep a tenant and a token with the same UUID apart.
const (
	tenantKeyPrefix = "tenant:"
	tokenKeyPrefix  = "token:"
)

// TenantKey is the key a tenant's requests are counted under.
func TenantKey(id uuid.UUID) string { return tenantKeyPrefix + id.String() }

// TokenKey is the key an API token's requests are counted under.
func TokenKey(id uuid.UUID) string { return tokenKeyPrefix + id.String() }

// Counts is one key's window as its store saw it when a call was decided.
type Counts struct {
	// Current is the bucket under way, after the call's bump if it counted.
	Current int64
	// Previous is the bucket before it.
	Previous int64
	// BucketStart is when the current bucket began, on the store's clock.
	BucketStart time.Time
	// Elapsed is how far into the current bucket the call was decided.
	Elapsed time.Duration
	// Admitted reports whether BumpIfFits counted the call. Bump always
	// counts, and Read never does.
	Admitted bool
}

// Weighted is the trailing-window count the limiters compare to a capacity.
func (c Counts) Weighted() float64 {
	return ratelimit.Weighted(c.Current, c.Previous, c.Elapsed)
}

// ResetsAt is when the current bucket rolls over.
func (c Counts) ResetsAt() time.Time { return c.BucketStart.Add(Window) }

// Counter is a store of windows.
type Counter interface {
	// Bump counts one request against key whatever the count, the way the
	// tenant limit counts: a denied caller that keeps calling does not get
	// to spend the next window's budget on rejected calls.
	Bump(ctx context.Context, key string) (Counts, error)
	// BumpIfFits counts one request against key only when it fits capacity
	// over the trailing window, the way the token limit counts: a caller
	// that waits out its Retry-After gets in.
	BumpIfFits(ctx context.Context, key string, capacity int) (Counts, error)
	// Read returns key's window without counting anything.
	Read(ctx context.Context, key string) (Counts, error)
}
